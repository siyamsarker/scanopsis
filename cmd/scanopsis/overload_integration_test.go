// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/config"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

// With every pooled connection stuck in the database, the server must answer
// the readers it cannot serve with 503 busy after a short wait instead of
// queueing them, and the readiness probe must keep answering 200 quickly.
//
// The database is made slow with a lock: GET /api/v1/setup reads the users
// table, and a transaction that holds ACCESS EXCLUSIVE on it stops every
// such read inside the database, each holding a pooled connection.
func TestIntegrationOverloadAnswersBusyAndStaysReady(t *testing.T) {
	const (
		poolSize = 16
		share    = poolSize / 4 // one client address may hold a quarter of the slots
		readers  = 100
	)
	pool := storetest.New(t)
	addr := freeAddr(t)
	cfg, err := config.Load(env(map[string]string{
		"SCANOPSIS_DATABASE_URL": dbURL(t, pool) + "&pool_max_conns=16",
		"SCANOPSIS_PUBLIC_URL":   "http://" + addr,
		"SCANOPSIS_LISTEN_ADDR":  addr,
		"SCANOPSIS_SECRET_KEY":   base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveApp(ctx, cfg, slog.New(slog.NewJSONHandler(logs, nil))) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serveApp returned %v", err)
			}
		case <-time.After(20 * time.Second):
			t.Error("server did not stop")
		}
	})

	base := "http://" + addr
	status := func(path string) (int, string, time.Duration) {
		start := time.Now()
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, "", time.Since(start)
		}
		_ = resp.Body.Close()
		return resp.StatusCode, resp.Header.Get("Retry-After"), time.Since(start)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if code, _, _ := status("/readyz"); code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server not ready; log:\n%s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if code, _, _ := status("/api/v1/setup"); code != 200 {
		t.Fatalf("setup status before the overload = %d", code)
	}

	// Hold the lock on a connection that is not the server's.
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_ = tx.Rollback(context.Background())
		}
	}
	defer release()
	if _, err := tx.Exec(context.Background(), "LOCK TABLE users IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}

	type result struct {
		code       int
		retryAfter string
		took       time.Duration
	}
	results := make([]result, readers)
	var wg sync.WaitGroup
	for i := range readers {
		wg.Go(func() {
			code, ra, d := status("/api/v1/setup")
			results[i] = result{code, ra, d}
		})
	}

	// While the readers are stuck or waiting, the probes answer at once.
	time.Sleep(500 * time.Millisecond)
	var ready []time.Duration
	for range 20 {
		code, _, d := status("/readyz")
		if code != http.StatusOK {
			t.Errorf("/readyz under overload = %d, want 200", code)
		}
		ready = append(ready, d)
		if code, _, _ := status("/healthz"); code != http.StatusOK {
			t.Errorf("/healthz under overload = %d, want 200", code)
		}
		time.Sleep(50 * time.Millisecond)
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i] < ready[j] })
	t.Logf("/readyz under overload with %d readers: p50 %v, p95 %v, max %v", readers, ready[10], ready[18], ready[19])
	if ready[18] > 250*time.Millisecond {
		t.Errorf("/readyz p95 = %v under overload, want well under the 2 s readiness timeout", ready[18])
	}

	// The readers that found no free slot give up after about 3 s with 503.
	time.Sleep(3500 * time.Millisecond)
	release()
	wg.Wait()

	ok, busy := 0, 0
	var slowest time.Duration
	for _, r := range results {
		switch r.code {
		case http.StatusOK:
			ok++
		case http.StatusServiceUnavailable:
			busy++
			if r.retryAfter == "" {
				t.Error("a 503 busy answer has no Retry-After")
			}
			slowest = max(slowest, r.took)
		default:
			t.Errorf("unexpected status %d", r.code)
		}
	}
	t.Logf("%d readers, pool of %d, %d per client: %d served, %d answered 503 busy (slowest %v)", readers, poolSize, share, ok, busy, slowest.Round(time.Millisecond))
	if ok != share || busy != readers-share {
		t.Errorf("%d served and %d answered 503, want %d and %d", ok, busy, share, readers-share)
	}
	if slowest > 5*time.Second {
		t.Errorf("a busy answer took %v, want about 3 s", slowest)
	}

	// The server recovers once the database is free again.
	if code, _, _ := status("/api/v1/setup"); code != http.StatusOK {
		t.Errorf("setup status after the overload = %d, want 200", code)
	}
}
