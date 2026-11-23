// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

// Package retention deletes old data on a schedule: scan history, SonarQube
// analyses and deliveries after the configured number of days, and audit
// events after a fixed longer period. The latest analysis of every service is
// always kept, however old.
//
// A scan is kept however old (it is "current") only when it is the latest of
// its service, ref, artifact and kind and its ref is the service's dashboard
// branch, was protected, or is the ref the dashboards show for a service
// without a chosen branch. Every other scan expires with the retention period,
// including the latest scan of a branch nobody has chosen. The compressed
// original report of a scan that is not current is cleared after a fixed 30
// days.
//
// Work is bounded: every delete statement removes at most BatchSize rows and
// commits on its own, so no statement runs long or locks many rows, and one
// database connection is used for the whole run.
package retention

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const (
	// BatchSize is the most rows one delete statement removes.
	BatchSize = 1000
	// AuditRetention is the fixed age after which audit events are deleted,
	// or the scan retention when that is longer.
	AuditRetention = 365 * 24 * time.Hour

	// RawReportRetention is the fixed age after which the stored original
	// report of a scan that is not current is cleared.
	RawReportRetention = 30 * 24 * time.Hour

	// AnalyzeThreshold is the number of scans deleted and original reports
	// cleared in one run from which the run ends with ANALYZE. Autovacuum
	// analyzes a table only after a fraction of it changed and some time
	// later; reads of the scans and findings after a large clean-up were two to
	// five times slower until then. Below the threshold the planner's
	// statistics stay close enough and autovacuum is left to do it.
	AnalyzeThreshold = 10000

	// firstRunDelay is the wait between start and the first run.
	firstRunDelay = time.Minute
	// runInterval is the time between runs.
	runInterval = 24 * time.Hour
)

// advisoryLockKey is the PostgreSQL advisory lock that lets one instance
// run the job at a time. It differs from the SonarQube sync's key.
const advisoryLockKey int64 = 0x53434e5254 // "SCNRT"

// SettingsSource gives the current settings; *settings.Service satisfies it.
// The retention period is read again at the start of every run.
type SettingsSource interface {
	Current() settings.Snapshot
}

// Result is what one run did.
type Result struct {
	// Skipped is true when another instance held the lock.
	Skipped         bool
	Scans           int64
	RawReports      int64
	SonarAnalyses   int64
	SonarDeliveries int64
	AuditEvents     int64
	// Analyzed is true when the run ended with ANALYZE of scans and findings.
	Analyzed        bool
	FailedSteps     int
	ScanCutoff      time.Time
	RawReportCutoff time.Time
	AuditCutoff     time.Time
	Duration        time.Duration
	RetentionDays   int
}

// Retention is the scheduled deletion job.
type Retention struct {
	pool *pgxpool.Pool
	cfg  SettingsSource
	log  *slog.Logger

	now        func() time.Time
	batch      int32
	analyzeAt  int64
	firstDelay time.Duration
	interval   time.Duration
}

// New returns the job. Start it with go r.Run(ctx).
func New(pool *pgxpool.Pool, cfg SettingsSource, log *slog.Logger) *Retention {
	return &Retention{
		pool: pool, cfg: cfg, log: log,
		now: time.Now, batch: BatchSize, analyzeAt: AnalyzeThreshold, firstDelay: firstRunDelay, interval: runInterval,
	}
}

// Run waits a minute, runs once, then runs every 24 hours until ctx ends.
func (r *Retention) Run(ctx context.Context) {
	timer := time.NewTimer(r.firstDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			r.RunOnce(ctx)
			timer.Reset(r.interval)
		}
	}
}

// cutoffs returns the age limits for a run at now; days is kept within the
// allowed range, so a cutoff is always in the past. The audit limit is never
// closer to now than the scan limit.
func cutoffs(now time.Time, days int) (scans, audit time.Time) {
	days = min(max(days, settings.MinRetentionDays), settings.MaxRetentionDays)
	period := time.Duration(days) * 24 * time.Hour
	scans = now.Add(-period)
	audit = now.Add(-max(AuditRetention, period))
	return scans, audit
}

// RunOnce performs one run and logs its outcome. It does nothing when
// another instance holds the lock.
func (r *Retention) RunOnce(ctx context.Context) Result {
	start := r.now()
	days := r.cfg.Current().RetentionDays
	res := Result{RetentionDays: min(max(days, settings.MinRetentionDays), settings.MaxRetentionDays)}
	res.ScanCutoff, res.AuditCutoff = cutoffs(start, days)
	res.RawReportCutoff = start.Add(-RawReportRetention)

	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		r.log.WarnContext(ctx, "data retention could not get a database connection", "error", err)
		res.FailedSteps++
		return res
	}
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&locked); err != nil {
		r.log.WarnContext(ctx, "data retention could not take its lock", "error", err)
		conn.Release()
		res.FailedSteps++
		return res
	}
	if !locked {
		conn.Release()
		res.Skipped = true
		r.log.DebugContext(ctx, "data retention skipped: another instance is running it")
		return res
	}
	defer func() {
		if _, err := conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", advisoryLockKey); err != nil {
			// Closing the connection drops the lock.
			_ = conn.Conn().Close(context.WithoutCancel(ctx))
		}
		conn.Release()
	}()

	q := store.New(conn)
	steps := []struct {
		name string
		into *int64
		run  func() (int64, error)
	}{
		{"scans", &res.Scans, func() (int64, error) { return r.deleteScans(ctx, q, res.ScanCutoff) }},
		{"raw reports", &res.RawReports, func() (int64, error) { return r.clearRawReports(ctx, q, res.RawReportCutoff) }},
		{"sonar analyses", &res.SonarAnalyses, func() (int64, error) { return r.deleteAnalyses(ctx, q, res.ScanCutoff) }},
		{"sonar deliveries", &res.SonarDeliveries, func() (int64, error) {
			return r.deleteOldest(ctx, func(n int32) (int64, error) {
				return q.DeleteExpiredSonarDeliveries(ctx, store.DeleteExpiredSonarDeliveriesParams{Cutoff: ts(res.ScanCutoff), BatchSize: n})
			})
		}},
		{"audit events", &res.AuditEvents, func() (int64, error) {
			return r.deleteOldest(ctx, func(n int32) (int64, error) {
				return q.DeleteExpiredAuditEvents(ctx, store.DeleteExpiredAuditEventsParams{Cutoff: ts(res.AuditCutoff), BatchSize: n})
			})
		}},
	}
	for _, s := range steps {
		n, err := s.run()
		*s.into = n
		if err != nil {
			res.FailedSteps++
			r.log.WarnContext(ctx, "data retention step failed", "step", s.name, "deleted_before_error", n, "error", err)
		}
	}

	// A large clean-up leaves the planner with statistics of the old data.
	if res.Scans+res.RawReports >= r.analyzeAt && ctx.Err() == nil {
		if err := analyze(ctx, conn, res.Scans > 0); err != nil {
			res.FailedSteps++
			r.log.WarnContext(ctx, "data retention step failed", "step", "analyze", "error", err)
		} else {
			res.Analyzed = true
		}
	}

	// A run that deleted anything is itself audited, so the deletion of old
	// audit events stays visible in the audit log.
	if res.Scans+res.RawReports+res.SonarAnalyses+res.SonarDeliveries+res.AuditEvents > 0 {
		if err := recordRun(ctx, q, res); err != nil {
			res.FailedSteps++
			r.log.WarnContext(ctx, "data retention could not write its audit event", "error", err)
		}
	}

	res.Duration = r.now().Sub(start)
	r.log.InfoContext(ctx, "data retention run finished",
		"retention_days", res.RetentionDays,
		"scans_deleted", res.Scans,
		"raw_reports_cleared", res.RawReports,
		"sonar_analyses_deleted", res.SonarAnalyses,
		"sonar_deliveries_deleted", res.SonarDeliveries,
		"audit_events_deleted", res.AuditEvents,
		"analyzed", res.Analyzed,
		"failed_steps", res.FailedSteps,
		"duration", res.Duration.Round(time.Millisecond).String())
	return res
}

// analyze updates the planner statistics of scans, and of vulnerabilities
// when scans were deleted, because their findings went with them. ANALYZE
// reads a fixed-size sample, so it takes about as long on a large table as on
// a small one and fits in the statement timeout.
func analyze(ctx context.Context, conn *pgxpool.Conn, findings bool) error {
	if _, err := conn.Exec(ctx, "ANALYZE scans"); err != nil {
		return fmt.Errorf("analyze scans: %w", err)
	}
	if findings {
		if _, err := conn.Exec(ctx, "ANALYZE vulnerabilities"); err != nil {
			return fmt.Errorf("analyze vulnerabilities: %w", err)
		}
	}
	return nil
}

// AuditActor is the actor of the audit event a run writes.
const AuditActor = "retention"

func recordRun(ctx context.Context, q *store.Queries, res Result) error {
	detail, err := json.Marshal(map[string]any{
		"retention_days":    res.RetentionDays,
		"scan_cutoff":       res.ScanCutoff.UTC().Format(time.RFC3339),
		"audit_cutoff":      res.AuditCutoff.UTC().Format(time.RFC3339),
		"scans":             res.Scans,
		"raw_reports":       res.RawReports,
		"raw_report_cutoff": res.RawReportCutoff.UTC().Format(time.RFC3339),
		"sonar_analyses":    res.SonarAnalyses,
		"sonar_deliveries":  res.SonarDeliveries,
		"audit_events":      res.AuditEvents,
	})
	if err != nil {
		return err
	}
	return q.InsertAudit(ctx, store.InsertAuditParams{Actor: AuditActor, Action: audit.ActionRetentionDeleted, Detail: detail})
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// beginning is the cursor position before every row.
func beginning() pgtype.Timestamptz {
	return pgtype.Timestamptz{InfinityModifier: pgtype.NegativeInfinity, Valid: true}
}

// cursor tracks the newest (time, id) position deleted so far.
type cursor struct {
	at pgtype.Timestamptz
	id int64
}

func (c *cursor) advance(at pgtype.Timestamptz, id int64) {
	if c.at.InfinityModifier != pgtype.Finite || at.Time.After(c.at.Time) || (at.Time.Equal(c.at.Time) && id > c.id) {
		c.at, c.id = at, id
	}
}

// deleteScans deletes expired scans that are not current in batches. Each
// batch resumes after the newest scan the previous one deleted, so the
// current scans that are skipped are not examined again.
func (r *Retention) deleteScans(ctx context.Context, q *store.Queries, cutoff time.Time) (int64, error) {
	cur := cursor{at: beginning()}
	var total int64
	for ctx.Err() == nil {
		rows, err := q.DeleteExpiredScans(ctx, store.DeleteExpiredScansParams{
			AfterAt: cur.at, AfterID: cur.id, Cutoff: ts(cutoff), BatchSize: r.batch,
		})
		if err != nil {
			return total, fmt.Errorf("delete scans: %w", err)
		}
		for _, row := range rows {
			cur.advance(row.ScannedAt, row.ID)
		}
		total += int64(len(rows))
		if len(rows) < int(r.batch) {
			return total, nil
		}
	}
	return total, ctx.Err()
}

// clearRawReports empties the stored original report of scans older than
// cutoff that are not current, in batches paged like deleteScans.
func (r *Retention) clearRawReports(ctx context.Context, q *store.Queries, cutoff time.Time) (int64, error) {
	cur := cursor{at: beginning()}
	var total int64
	for ctx.Err() == nil {
		rows, err := q.ClearExpiredRawReports(ctx, store.ClearExpiredRawReportsParams{
			AfterAt: cur.at, AfterID: cur.id, Cutoff: ts(cutoff), BatchSize: r.batch,
		})
		if err != nil {
			return total, fmt.Errorf("clear raw reports: %w", err)
		}
		for _, row := range rows {
			cur.advance(row.ScannedAt, row.ID)
		}
		total += int64(len(rows))
		if len(rows) < int(r.batch) {
			return total, nil
		}
	}
	return total, ctx.Err()
}

// deleteAnalyses is deleteScans for SonarQube analyses.
func (r *Retention) deleteAnalyses(ctx context.Context, q *store.Queries, cutoff time.Time) (int64, error) {
	cur := cursor{at: beginning()}
	var total int64
	for ctx.Err() == nil {
		rows, err := q.DeleteExpiredSonarAnalyses(ctx, store.DeleteExpiredSonarAnalysesParams{
			AfterAt: cur.at, AfterID: cur.id, Cutoff: ts(cutoff), BatchSize: r.batch,
		})
		if err != nil {
			return total, fmt.Errorf("delete sonar analyses: %w", err)
		}
		for _, row := range rows {
			cur.advance(row.AnalysedAt, row.ID)
		}
		total += int64(len(rows))
		if len(rows) < int(r.batch) {
			return total, nil
		}
	}
	return total, ctx.Err()
}

// deleteOldest repeats del until a batch comes back short.
func (r *Retention) deleteOldest(ctx context.Context, del func(batch int32) (int64, error)) (int64, error) {
	var total int64
	for ctx.Err() == nil {
		n, err := del(r.batch)
		total += n
		if err != nil {
			return total, err
		}
		if n < int64(r.batch) {
			return total, nil
		}
	}
	return total, ctx.Err()
}
