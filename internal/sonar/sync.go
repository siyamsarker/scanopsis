// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package sonar

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/store"
)

// advisoryLockKey is the PostgreSQL advisory lock that lets one instance sync
// at a time.
const advisoryLockKey int64 = 0x53434e5359 // "SCNSY"

// Run processes webhook deliveries and, every sync interval, reads the newest
// analysis of every linked project in case a webhook was missed. It returns
// when ctx ends. The settings are read again on every check, so changes apply
// without a restart.
func (s *Sonar) Run(ctx context.Context) {
	s.interruptPending(ctx)
	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()
	var last time.Time // zero: a sync is due at the first tick
	for {
		select {
		case <-ctx.Done():
			return
		case d := <-s.queue:
			s.process(ctx, d)
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

// sync runs one reconciliation if no other instance is running one.
func (s *Sonar) sync(ctx context.Context, snap settings.Snapshot) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		s.log.WarnContext(ctx, "SonarQube sync could not get a database connection", "error", err)
		return
	}
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&locked); err != nil || !locked {
		conn.Release()
		return
	}
	defer func() {
		if _, err := conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", advisoryLockKey); err != nil {
			// Closing the connection drops the lock.
			_ = conn.Conn().Close(context.WithoutCancel(ctx))
		}
		conn.Release()
	}()

	cl := s.client(snap)
	var lastErr string
	if v, err := cl.serverVersion(ctx); err != nil {
		s.warn(ctx, "reading the SonarQube version failed", "", err)
		lastErr = briefError(err)
	} else {
		s.mu.Lock()
		s.version = v
		s.mu.Unlock()
	}

	services, err := s.q.SonarLinkedServices(ctx)
	if err != nil {
		s.log.WarnContext(ctx, "SonarQube sync could not list services", "error", err)
		lastErr = "Listing the services failed."
	}
	for _, svc := range services {
		if ctx.Err() != nil {
			return
		}
		err := s.syncProject(ctx, cl, svc)
		if err == nil {
			continue
		}
		s.warn(ctx, "SonarQube sync failed for a project", svc.SonarProjectKey, err)
		lastErr = svc.SonarProjectKey + ": " + briefError(err)
		var ae *apiError
		if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
			break // the token is wrong for every project
		}
	}
	if ctx.Err() != nil {
		return
	}
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
func (s *Sonar) warn(ctx context.Context, msg, project string, err error) {
	args := []any{"project", project, "error", err}
	var ae *apiError
	if errors.As(err, &ae) {
		args = append(args, "status", ae.Status)
	}
	s.log.WarnContext(ctx, msg, args...)
}

// pendingTimeout is how long a delivery may stay pending before it counts as
// interrupted.
const pendingTimeout = 10 * time.Minute

// interruptPending fails deliveries left pending by a server that stopped
// while processing them.
func (s *Sonar) interruptPending(ctx context.Context) {
	_, err := s.q.InterruptSonarDeliveries(ctx, store.InterruptSonarDeliveriesParams{
		ReceivedAt: pgtype.Timestamptz{Time: s.now().Add(-pendingTimeout), Valid: true},
		Detail:     "Processing was interrupted; the next sync picks the analysis up.",
	})
	if err != nil && ctx.Err() == nil {
		s.log.WarnContext(ctx, "could not clean up interrupted SonarQube deliveries", "error", err)
	}
}
