// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package retention

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/store"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestCutoffs(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	for _, tc := range []struct {
		name           string
		days           int
		scans, auditAt time.Time
	}{
		{"default", 90, now.Add(-90 * day), now.Add(-365 * day)},
		{"minimum", 7, now.Add(-7 * day), now.Add(-365 * day)},
		{"exactly a year", 365, now.Add(-365 * day), now.Add(-365 * day)},
		{"audit is never shorter", 1000, now.Add(-1000 * day), now.Add(-1000 * day)},
		{"zero falls back to the minimum", 0, now.Add(-7 * day), now.Add(-365 * day)},
		{"below the minimum", 1, now.Add(-7 * day), now.Add(-365 * day)},
		{"above the maximum", 100000, now.Add(-3650 * day), now.Add(-3650 * day)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scans, audit := cutoffs(now, tc.days)
			if !scans.Equal(tc.scans) || !audit.Equal(tc.auditAt) {
				t.Errorf("cutoffs(%d) = %v, %v; want %v, %v", tc.days, scans, audit, tc.scans, tc.auditAt)
			}
		})
	}
}

func TestCursorAdvance(t *testing.T) {
	at := func(sec int64) pgtype.Timestamptz {
		return pgtype.Timestamptz{Time: time.Unix(sec, 0), Valid: true}
	}
	c := cursor{at: beginning()}
	c.advance(at(10), 5)
	if c.id != 5 || !c.at.Time.Equal(time.Unix(10, 0)) {
		t.Fatalf("first advance: %+v", c)
	}
	c.advance(at(10), 3) // same time, lower id: no move
	c.advance(at(9), 99) // earlier: no move
	if c.id != 5 || !c.at.Time.Equal(time.Unix(10, 0)) {
		t.Fatalf("cursor moved backwards: %+v", c)
	}
	c.advance(at(10), 8) // same time, higher id
	if c.id != 8 {
		t.Fatalf("id = %d, want 8", c.id)
	}
	c.advance(at(11), 1) // later
	if c.id != 1 || !c.at.Time.Equal(time.Unix(11, 0)) {
		t.Fatalf("later position ignored: %+v", c)
	}
}

type fixedSettings struct{ days int }

func (f fixedSettings) Current() settings.Snapshot { return settings.Snapshot{RetentionDays: f.days} }

func TestNewDefaults(t *testing.T) {
	r := New(nil, fixedSettings{90}, discard)
	if r.batch != BatchSize || r.firstDelay != time.Minute || r.interval != 24*time.Hour {
		t.Errorf("defaults: batch %d, first %v, interval %v", r.batch, r.firstDelay, r.interval)
	}
	if advisoryLockKey == 0x53434e5359 {
		t.Error("the lock key must differ from the SonarQube sync's")
	}
}

func TestRunStopsWhenContextEnds(t *testing.T) {
	r := New(nil, fixedSettings{90}, discard)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context ended")
	}
}

func TestBatchLoopsStopWhenContextEnds(t *testing.T) {
	r := New(nil, fixedSettings{90}, discard)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	q := store.New(nil) // never used: the context has ended before the first batch
	if n, err := r.deleteScans(ctx, q, time.Now()); n != 0 || err == nil {
		t.Errorf("deleteScans = %d, %v", n, err)
	}
	if n, err := r.deleteAnalyses(ctx, q, time.Now()); n != 0 || err == nil {
		t.Errorf("deleteAnalyses = %d, %v", n, err)
	}
	calls := 0
	n, err := r.deleteOldest(ctx, func(int32) (int64, error) { calls++; return 0, nil })
	if n != 0 || err == nil || calls != 0 {
		t.Errorf("deleteOldest = %d, %v, calls %d", n, err, calls)
	}
}
