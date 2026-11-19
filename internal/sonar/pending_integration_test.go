// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package sonar

import (
	"context"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/settings"
)

func TestWebhookRecordedPendingThenInterrupted(t *testing.T) {
	e := newEnv(t)
	base := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	now := base
	e.s.now = func() time.Time { return now }
	wantStatus(t, e, hook("old", "k", nil), 202)
	now = base.Add(9 * time.Minute)
	wantStatus(t, e, hook("recent", "k", nil), 202)
	for _, task := range []string{"old", "recent"} {
		if d := e.delivery(t, task); d.Outcome != "pending" || d.Detail != "" {
			t.Errorf("%s on receipt: %+v", task, d)
		}
	}
	// As after a restart: the queue is empty, the rows remain.
	e.s.queue = make(chan delivery, 100)

	// The sync fails what has been pending for more than 10 minutes.
	now = base.Add(11 * time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.s.Run(ctx); close(done) }()
	waitFor(t, func() bool { return e.delivery(t, "old").Outcome != "pending" })
	cancel()
	<-done
	d := e.delivery(t, "old")
	if d.Outcome != "failed" || d.Detail != interruptedDetail {
		t.Errorf("old: %+v", d)
	}
	if d := e.delivery(t, "recent"); d.Outcome != "pending" {
		t.Errorf("a delivery pending for 2 minutes must stay pending: %+v", d)
	}

	// A redelivery of the failed task is accepted again and queued; a
	// redelivery of a task that is not failed changes nothing.
	wantStatus(t, e, hook("old", "other-project", nil), 202)
	if d := e.delivery(t, "old"); d.Outcome != "failed" {
		t.Errorf("redelivery for another project was accepted: %+v", d)
	}
	wantStatus(t, e, hook("old", "k", nil), 202)
	if d := e.delivery(t, "old"); d.Outcome != "pending" || d.Detail != "" {
		t.Errorf("redelivered failed task: %+v", d)
	}
	if len(e.s.queue) != 1 {
		t.Errorf("redelivered failed task queued %d times, want 1", len(e.s.queue))
	}
	// Bounded: at most 5 attempts, and not more often than once a minute.
	for i := 0; i < 6; i++ {
		_, _ = e.pool.Exec(context.Background(), "UPDATE sonar_deliveries SET outcome = 'failed' WHERE task_id = 'old'")
		wantStatus(t, e, hook("old", "k", nil), 202)
		now = now.Add(2 * time.Minute)
	}
	if d := e.delivery(t, "old"); d.Outcome != "failed" {
		t.Errorf("a sixth attempt was accepted: %+v", d)
	}
	var attempts int
	if err := e.pool.QueryRow(context.Background(), "SELECT attempts FROM sonar_deliveries WHERE task_id = 'old'").Scan(&attempts); err != nil || attempts != 5 {
		t.Errorf("attempts = %d, %v; want 5", attempts, err)
	}

	before, queued := e.delivery(t, "recent"), len(e.s.queue)
	wantStatus(t, e, hook("recent", "k", nil), 202)
	if after := e.delivery(t, "recent"); after != before || len(e.s.queue) != queued {
		t.Errorf("redelivery of a pending task changed it: %+v -> %+v (queue %d -> %d)", before, after, queued, len(e.s.queue))
	}
}

func (f *fixedSettings) set(fn func(*settings.Snapshot)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(&f.snap)
}
