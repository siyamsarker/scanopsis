// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Package retention deletes old data on a schedule: scan history, SonarQube
// analyses and deliveries after the configured number of days, and audit
// events after a fixed longer period. The latest scan of every service, ref,
// artifact and kind, and the latest analysis of every service, are always
// kept, however old.
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
	SonarAnalyses   int64
	SonarDeliveries int64
	AuditEvents     int64
	FailedSteps     int
	ScanCutoff      time.Time
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
	firstDelay time.Duration
	interval   time.Duration
}

// New returns the job. Start it with go r.Run(ctx).
func New(pool *pgxpool.Pool, cfg SettingsSource, log *slog.Logger) *Retention {
	return &Retention{
		pool: pool, cfg: cfg, log: log,
		now: time.Now, batch: BatchSize, firstDelay: firstRunDelay, interval: runInterval,
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

	// A run that deleted anything is itself audited, so the deletion of old
	// audit events stays visible in the audit log.
	if res.Scans+res.SonarAnalyses+res.SonarDeliveries+res.AuditEvents > 0 {
		if err := recordRun(ctx, q, res); err != nil {
			res.FailedSteps++
			r.log.WarnContext(ctx, "data retention could not write its audit event", "error", err)
		}
	}

	res.Duration = r.now().Sub(start)
	r.log.InfoContext(ctx, "data retention run finished",
		"retention_days", res.RetentionDays,
		"scans_deleted", res.Scans,
		"sonar_analyses_deleted", res.SonarAnalyses,
		"sonar_deliveries_deleted", res.SonarDeliveries,
		"audit_events_deleted", res.AuditEvents,
		"failed_steps", res.FailedSteps,
		"duration", res.Duration.Round(time.Millisecond).String())
	return res
}

// AuditActor is the actor of the audit event a run writes.
const AuditActor = "retention"

func recordRun(ctx context.Context, q *store.Queries, res Result) error {
	detail, err := json.Marshal(map[string]any{
		"retention_days":   res.RetentionDays,
		"scan_cutoff":      res.ScanCutoff.UTC().Format(time.RFC3339),
		"audit_cutoff":     res.AuditCutoff.UTC().Format(time.RFC3339),
		"scans":            res.Scans,
		"sonar_analyses":   res.SonarAnalyses,
		"sonar_deliveries": res.SonarDeliveries,
		"audit_events":     res.AuditEvents,
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

// deleteScans deletes expired, non-latest scans in batches. Each batch
// resumes after the newest scan the previous one deleted, so the latest
// scans that are skipped are not examined again.
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
