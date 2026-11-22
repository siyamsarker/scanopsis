// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package sonar

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// roundTrip is an http.RoundTripper made of a function.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// intercept makes the SonarQube client of e send its requests through fn,
// which may answer them itself or pass them on with next.
func (e *env) intercept(fn func(r *http.Request, next http.RoundTripper) (*http.Response, error)) {
	next := e.fake.Client().Transport
	e.s.hc = sameHostClient(&http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return fn(r, next)
	})})
}

// blackHole never answers until the request is cancelled.
func blackHole(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

// lockedBuffer is a log destination safe for concurrent use.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func (e *env) captureLog() *lockedBuffer {
	buf := &lockedBuffer{}
	e.s.log = slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return buf
}

func shortTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := requestTimeout
	requestTimeout = d
	t.Cleanup(func() { requestTimeout = old })
}

func (e *env) outcomes(t *testing.T) map[string]int {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT outcome, count(*) FROM sonar_deliveries GROUP BY outcome`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var o string
		var n int
		if err := rows.Scan(&o, &n); err != nil {
			t.Fatal(err)
		}
		out[o] = n
	}
	return out
}

// runUntil runs the loops until cond holds.
func (e *env) runUntil(t *testing.T, cond func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.s.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	waitFor(t, cond)
}

// A burst of 300 webhooks, more than the queue holds: none is lost. Each ends
// linked by a worker or pending for the sync, and the sync then links the
// pending ones.
func TestBurstOf300WebhooksLosesNothing(t *testing.T) {
	e := newEnv(t)
	e.s.queue = make(chan delivery, 50)
	const projects, perProject = 30, 10
	for p := range projects {
		key := fmt.Sprintf("proj:%d", p)
		e.service(t, fmt.Sprintf("platform/p%d", p), 0, key)
		e.project(key)
	}
	for i := range projects * perProject {
		wantStatus(t, e, hook(fmt.Sprintf("t%d", i), fmt.Sprintf("proj:%d", i%projects), nil), 202)
	}
	// Nothing is processed yet: the queue holds 50, the other 250 wait.
	got := e.outcomes(t)
	if got["pending"] != 300 || got["failed"] != 0 {
		t.Fatalf("after the burst: %v", got)
	}
	var queued int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM sonar_deliveries WHERE detail = $1`, queuedDetail).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 250 {
		t.Fatalf("%d deliveries wait for the sync, want 250", queued)
	}

	start := time.Now()
	e.runUntil(t, func() bool { return e.outcomes(t)["pending"] == 0 })
	t.Logf("300 webhooks resolved in %v", time.Since(start))
	if got := e.outcomes(t); got["linked"] != 300 || len(got) != 1 {
		t.Errorf("outcomes: %v", got)
	}
}

// A sync that waits for SonarQube does not hold up the deliveries.
func TestDeliveriesAreProcessedWhileTheSyncWaits(t *testing.T) {
	e := newEnv(t)
	e.service(t, "platform/slow", 0, "slow")
	e.project("slow")
	e.project("proj")
	e.service(t, "platform/proj", 0, "proj")
	release := make(chan struct{})
	var versionCalls atomic.Int32
	e.intercept(func(r *http.Request, next http.RoundTripper) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/api/server/version") {
			versionCalls.Add(1)
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		return next.RoundTrip(r)
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.s.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	waitFor(t, func() bool { return versionCalls.Load() == 1 }) // the sync is waiting

	wantStatus(t, e, hook("while-sync", "proj", nil), 202)
	if d := e.waitDelivery(t, "while-sync"); d.Outcome != "linked" {
		t.Errorf("delivery: %+v", d)
	}
	close(release)
}

// Several deliveries are processed at once: with the fake answering slowly,
// 8 deliveries finish far sooner than one after the other.
func TestWorkersProcessConcurrently(t *testing.T) {
	e := newEnv(t)
	const n = 8
	for i := range n {
		key := fmt.Sprintf("c:%d", i)
		e.service(t, fmt.Sprintf("platform/c%d", i), 0, key)
		e.project(key)
		wantStatus(t, e, hook(fmt.Sprintf("c%d", i), key, nil), 202)
	}
	e.fake.SetDelay(100 * time.Millisecond)
	start := time.Now()
	e.runUntil(t, func() bool { return e.outcomes(t)["linked"] == n })
	// One after the other: 8 deliveries x 2 calls x 100 ms = 1.6 s.
	if d := time.Since(start); d > 1200*time.Millisecond {
		t.Errorf("took %v; the deliveries were not processed concurrently", d)
	}
}

// No pooled database connection is held while SonarQube is called, by the
// sync or by a worker.
func TestNoConnectionHeldDuringHTTP(t *testing.T) {
	e := newEnv(t)
	for i := range 3 {
		key := fmt.Sprintf("h:%d", i)
		e.service(t, fmt.Sprintf("platform/h%d", i), 0, key)
		e.project(key)
	}
	wantStatus(t, e, hook("held", "h:0", nil), 202)
	var calls, held atomic.Int32
	e.intercept(func(r *http.Request, next http.RoundTripper) (*http.Response, error) {
		calls.Add(1)
		if n := e.pool.Stat().AcquiredConns(); n != 0 {
			held.Add(1)
		}
		time.Sleep(20 * time.Millisecond) // let a leaked connection show
		return next.RoundTrip(r)
	})
	e.runUntil(t, func() bool {
		_, at, _ := e.state()
		return !at.IsZero() && e.outcomes(t)["pending"] == 0
	})
	if calls.Load() < 8 {
		t.Fatalf("only %d calls were observed", calls.Load())
	}
	if held.Load() != 0 {
		t.Errorf("%d of %d calls were made while a pooled connection was held", held.Load(), calls.Load())
	}
}

// With SonarQube not answering, a sync ends after the first call and logs one
// line; it does not try every project.
func TestSyncStopsWhenTheVersionCallFails(t *testing.T) {
	e := newEnv(t)
	shortTimeout(t, 150*time.Millisecond)
	logs := e.captureLog()
	for i := range 20 {
		e.service(t, fmt.Sprintf("platform/o%d", i), 0, fmt.Sprintf("o:%d", i))
	}
	var calls atomic.Int32
	e.intercept(func(r *http.Request, _ http.RoundTripper) (*http.Response, error) {
		calls.Add(1)
		return blackHole(r)
	})
	start := time.Now()
	e.runSync(t)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("sync took %v", d)
	}
	if calls.Load() != 1 {
		t.Errorf("%d calls, want 1", calls.Load())
	}
	if n := strings.Count(logs.String(), `msg="SonarQube sync`); n != 1 {
		t.Errorf("%d warnings, want 1:\n%s", n, logs)
	}
	if _, at, lastErr := e.state(); at.IsZero() || lastErr == "" {
		t.Errorf("the failed sync must be recorded: %v %q", at, lastErr)
	}
}

// When only the project calls get no answer, the sync gives up after three in
// a row and reports once.
func TestSyncStopsAfterThreeTransportErrors(t *testing.T) {
	e := newEnv(t)
	shortTimeout(t, 100*time.Millisecond)
	logs := e.captureLog()
	for i := range 20 {
		key := fmt.Sprintf("o:%d", i)
		e.service(t, fmt.Sprintf("platform/o%d", i), 0, key)
		e.project(key)
	}
	var projectCalls atomic.Int32
	e.intercept(func(r *http.Request, next http.RoundTripper) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/api/server/version") {
			return next.RoundTrip(r)
		}
		projectCalls.Add(1)
		return blackHole(r)
	})
	e.runSync(t)
	if projectCalls.Load() != maxTransportErrors {
		t.Errorf("%d project calls, want %d", projectCalls.Load(), maxTransportErrors)
	}
	if n := strings.Count(logs.String(), `msg="SonarQube sync`); n != 1 || !strings.Contains(logs.String(), "SonarQube sync finished with failures") {
		t.Errorf("want one summary warning:\n%s", logs)
	}
	if !strings.Contains(logs.String(), "failed=20") {
		t.Errorf("the summary must count the projects not tried:\n%s", logs)
	}
	if _, _, lastErr := e.state(); !strings.Contains(lastErr, "no answer") {
		t.Errorf("lastErr %q", lastErr)
	}
}

// Many failing projects produce one summary line, not one per project.
func TestSyncAggregatesProjectWarnings(t *testing.T) {
	e := newEnv(t)
	logs := e.captureLog()
	for i := range 12 {
		key := fmt.Sprintf("a:%d", i)
		e.service(t, fmt.Sprintf("platform/a%d", i), 0, key)
		e.project(key)
	}
	e.fake.Fail("api/project_analyses/search", 500)
	e.runSync(t)
	if n := strings.Count(logs.String(), `msg="SonarQube sync`); n != 1 {
		t.Errorf("%d warnings, want 1:\n%s", n, logs)
	}
	if !strings.Contains(logs.String(), "failed=12") {
		t.Errorf("summary: %s", logs)
	}
}

// A rejected webhook is audited with the reason and the address, at most a
// burst per address.
func TestRejectedWebhooksAreAuditedWithABound(t *testing.T) {
	e := newEnv(t)
	send := func(ip string, secret string, body []byte) int {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/hooks/sonarqube", bytes.NewReader(body))
		req.Header.Set("X-Test-IP", ip)
		req.Header.Set(signatureHeader, sign(secret, body))
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec.Code
	}
	body := hook("x", "k", nil)
	for range DefaultAuditBurst + 15 {
		if code := send("203.0.113.9", "wrong", body); code != http.StatusUnauthorized {
			t.Fatalf("status %d", code)
		}
	}
	if code := send("198.51.100.4", "wrong", body); code != http.StatusUnauthorized {
		t.Fatalf("status %d", code)
	}
	// A signed body that is not a webhook is a rejection too.
	if code := send("192.0.2.1", testSecret, []byte(`{"nope":true}`)); code != http.StatusBadRequest {
		t.Fatalf("status %d", code)
	}

	perIP := map[string]int{}
	reasons := map[string]int{}
	for _, a := range e.audits(t) {
		if a["action"] != "webhook.rejected" {
			continue
		}
		if a["actor"] != "sonarqube:unknown" {
			t.Errorf("actor %v", a["actor"])
		}
		d := a["detail"].(map[string]any)
		perIP[d["ip"].(string)]++
		reasons[d["reason"].(string)]++
	}
	want := map[string]int{"203.0.113.9": DefaultAuditBurst, "198.51.100.4": 1, "192.0.2.1": 1}
	if fmt.Sprint(perIP) != fmt.Sprint(want) {
		t.Errorf("records per address: %v, want %v", perIP, want)
	}
	if reasons["invalid_signature"] != DefaultAuditBurst+1 || reasons["invalid_payload"] != 1 {
		t.Errorf("reasons: %v", reasons)
	}
	// Nothing was stored for a rejected delivery.
	if n := len(e.outcomes(t)); n != 0 {
		t.Errorf("deliveries recorded: %v", e.outcomes(t))
	}
}
