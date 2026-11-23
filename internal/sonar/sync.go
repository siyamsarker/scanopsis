// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package sonar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/store"
)

// advisoryLockKey is the PostgreSQL advisory lock that lets one instance sync
// at a time.
const advisoryLockKey int64 = 0x53434e5359 // "SCNSY"

// maxTransportErrors is how many calls in a row may fail without an answer
// from SonarQube (a timeout, a refused or reset connection) before a sync
// gives up until the next interval.
const maxTransportErrors = 3

// Run processes webhook deliveries with a small pool of workers and, every
// sync interval, reads the newest analysis of every linked project in case a
// webhook was missed. The sync runs in this goroutine, apart from the
// workers, so a slow sync never delays deliveries and a burst of deliveries
// never delays the sync. It returns when ctx ends. The settings are read
// again on every check, so changes apply without a restart.
func (s *Sonar) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for range max(s.workers, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case d := <-s.queue:
					s.process(ctx, d)
				}
			}
		}()
	}
	defer wg.Wait()

	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()
	var last time.Time // zero: a sync is due at the first tick
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap := s.cfg.Current()
			if !snap.SonarEnabled() {
				continue
			}
			interval := snap.SonarSyncInterval
			if interval < s.minInterval {
				interval = s.minInterval
			}
			if !last.IsZero() && time.Since(last) < interval {
				continue
			}
			last = time.Now()
			s.sync(ctx, snap)
		}
	}
}

// lockSync takes the advisory lock on a connection of its own, outside the
// pool, so a sync that waits for SonarQube never holds a pooled connection.
// The returned function releases the lock by closing that connection. ok is
// false when another instance holds the lock or the connection failed.
func (s *Sonar) lockSync(ctx context.Context) (unlock func(), ok bool) {
	conn, err := pgx.ConnectConfig(ctx, s.pool.Config().ConnConfig.Copy())
	if err != nil {
		s.log.WarnContext(ctx, "SonarQube sync could not connect to the database", "error", err)
		return nil, false
	}
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&locked); err != nil || !locked {
		_ = conn.Close(context.WithoutCancel(ctx))
		return nil, false
	}
	return func() { _ = conn.Close(context.WithoutCancel(ctx)) }, true
}

// sync runs one reconciliation if no other instance is running one. It holds
// no pooled database connection while it waits for SonarQube: every read or
// write of the database is its own short statement.
func (s *Sonar) sync(ctx context.Context, snap settings.Snapshot) {
	unlock, ok := s.lockSync(ctx)
	if !ok {
		return
	}
	defer unlock()

	// Under the lock, so only one instance changes delivery rows.
	s.interruptPending(ctx, snap.SonarSyncInterval)

	cl := s.client(snap)
	v, err := cl.serverVersion(ctx)
	if err != nil {
		// Without a version answer every project call would fail the same
		// way, possibly after its own timeout: stop and retry next interval.
		if ctx.Err() != nil {
			return
		}
		s.warn(ctx, "SonarQube sync stopped: reading the SonarQube version failed", "", err)
		s.finishSync(briefError(err))
		return
	}
	s.mu.Lock()
	s.version = v
	s.mu.Unlock()

	var lastErr string
	services, err := s.q.SonarLinkedServices(ctx)
	if err != nil {
		s.log.WarnContext(ctx, "SonarQube sync could not list services", "error", err)
		lastErr = "Listing the services failed."
	}
	var (
		synced      []string // keys of the projects read successfully
		failed      int
		firstFailed string
		firstErr    error
		transport   int // consecutive calls without an answer from SonarQube
		stoppedAt   string
	)
	for i, svc := range services {
		if ctx.Err() != nil {
			return
		}
		err := s.syncProject(ctx, cl, svc)
		if err == nil {
			synced = append(synced, svc.SonarProjectKey)
			transport = 0
			continue
		}
		if ctx.Err() != nil {
			return
		}
		failed++
		if firstErr == nil {
			firstFailed, firstErr = svc.SonarProjectKey, err
		}
		s.log.DebugContext(ctx, "SonarQube sync failed for a project", "project", svc.SonarProjectKey, "error", err)
		lastErr = svc.SonarProjectKey + ": " + briefError(err)
		var ae *apiError
		if errors.As(err, &ae) {
			transport = 0
			if ae.Status == http.StatusUnauthorized {
				stoppedAt = "SonarQube rejected the token"
				break // the token is wrong for every project
			}
			continue
		}
		if transport++; transport >= maxTransportErrors {
			stoppedAt = fmt.Sprintf("%d calls in a row got no answer from SonarQube", transport)
			lastErr = stoppedAt + "; the next sync will try again"
			failed += len(services) - i - 1 // not tried
			break
		}
	}
	if failed > 0 {
		// One line per sync, however many projects failed.
		args := []any{"failed", failed, "projects", len(services), "first_project", firstFailed}
		if stoppedAt != "" {
			args = append(args, "stopped_early", stoppedAt)
		}
		s.warn(ctx, "SonarQube sync finished with failures", "", firstErr, args...)
	}
	if len(synced) > 0 {
		if _, err := s.q.ResolveQueuedSonarDeliveries(ctx, store.ResolveQueuedSonarDeliveriesParams{
			Detail: syncedDetail, QueuedDetail: queuedDetail, ProjectKeys: synced,
		}); err != nil && ctx.Err() == nil {
			s.log.WarnContext(ctx, "could not resolve queued SonarQube deliveries", "error", err)
		}
	}
	if ctx.Err() != nil {
		return
	}
	s.finishSync(lastErr)
}

// finishSync records the end of a sync.
func (s *Sonar) finishSync(lastErr string) {
	s.mu.Lock()
	s.lastSyncAt = s.now()
	s.lastErr = lastErr
	s.mu.Unlock()
}

// syncProject stores the newest analysis of a project unless it is known.
func (s *Sonar) syncProject(ctx context.Context, cl *client, svc store.SonarLinkedServicesRow) error {
	latest, err := cl.latestAnalysis(ctx, svc.SonarProjectKey)
	if err != nil {
		return err
	}
	if latest == nil {
		return nil
	}
	known, err := s.q.SonarAnalysisKnown(ctx, store.SonarAnalysisKnownParams{ServiceID: svc.ID, AnalysisKey: latest.Key})
	if err != nil || known {
		return err
	}
	measures, err := cl.measures(ctx, svc.SonarProjectKey)
	if err != nil {
		return err
	}
	gate, conds, err := cl.projectStatus(ctx, svc.SonarProjectKey)
	if err != nil {
		return err
	}
	return s.save(ctx, snapshot{
		ServiceID: svc.ID, TaskID: "sync:" + latest.Key, AnalysisKey: latest.Key,
		AnalysedAt: latest.Date, Revision: latest.Revision, Gate: gate,
		Conditions: conds, Measures: measures,
	}, "")
}

// warn logs a failure with the SonarQube status code when there is one.
func (s *Sonar) warn(ctx context.Context, msg, project string, err error, extra ...any) {
	args := []any{"project", project, "error", err}
	args = append(args, extra...)
	var ae *apiError
	if errors.As(err, &ae) {
		args = append(args, "status", ae.Status)
	}
	s.log.WarnContext(ctx, msg, args...)
}

// pendingTimeout is how long a delivery may stay pending before it counts as
// interrupted.
const pendingTimeout = 10 * time.Minute

// interruptedDetail must not promise more than the sync can do: the sync
// only reads projects that are already linked by key.
const interruptedDetail = "Processing was interrupted. Redeliver the webhook from SonarQube to process it again; analyses of projects linked by key are also picked up by the next sync."

// queuedDetail marks a delivery that stays pending because the processing
// queue was full. The next sync reads the analysis of a project linked by key
// and then marks the delivery linked (syncedDetail).
const queuedDetail = "The processing queue was full; the next sync will read the analysis."

const syncedDetail = "The processing queue was full; the sync read the analysis."

// interruptPending fails deliveries left pending for longer than
// pendingTimeout, for example because the server stopped while processing
// them. A delivery that waits for the sync because the queue was full is
// given two more sync intervals. It runs on every sync, under the sync's
// advisory lock.
func (s *Sonar) interruptPending(ctx context.Context, interval time.Duration) {
	interval = max(interval, s.minInterval)
	now := s.now()
	_, err := s.q.InterruptSonarDeliveries(ctx, store.InterruptSonarDeliveriesParams{
		Cutoff:       pgtype.Timestamptz{Time: now.Add(-pendingTimeout), Valid: true},
		QueuedCutoff: pgtype.Timestamptz{Time: now.Add(-pendingTimeout - 2*interval), Valid: true},
		QueuedDetail: queuedDetail,
		Detail:       interruptedDetail,
	})
	if err != nil && ctx.Err() == nil {
		s.log.WarnContext(ctx, "could not clean up interrupted SonarQube deliveries", "error", err)
	}
}
