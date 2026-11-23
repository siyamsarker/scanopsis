// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

// hangingGitLab serves the signing keys, or holds every request until the
// test releases it.
type hangingGitLab struct {
	ts      *httptest.Server
	hits    atomic.Int32
	hang    atomic.Bool
	release chan struct{}
	mu      sync.Mutex
	keys    []jose.JSONWebKey
}

func newHangingGitLab(t *testing.T) *hangingGitLab {
	t.Helper()
	h := &hangingGitLab{release: make(chan struct{})}
	h.hang.Store(true)
	h.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		h.hits.Add(1)
		if h.hang.Load() {
			select {
			case <-h.release:
			case <-req.Context().Done():
				return
			}
		}
		h.mu.Lock()
		b, _ := json.Marshal(jose.JSONWebKeySet{Keys: h.keys})
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}))
	t.Cleanup(h.ts.Close)
	t.Cleanup(func() { close(h.release) }) // runs before the server is closed
	return h
}

// While GitLab hangs and the cache is cold, concurrent uploads share one
// fetch: all are answered "unavailable" when it times out, the uploads after
// that inside the interval are answered at once without a request, and a
// GitLab that came back is used after the interval.
func TestVerifyHangingGitLabSharesOneFetch(t *testing.T) {
	h := newHangingGitLab(t)
	r := newRotating(t) // for its signing helpers only
	s1 := r.newKey(t, "key-1")
	h.mu.Lock()
	h.keys = r.keys
	h.mu.Unlock()
	g := &gitlab{url: h.ts.URL}
	tok := r.sign(t, s1, g.claims())

	clock := newTestClock()
	v := clock.verifier()
	const fetchTimeout = 400 * time.Millisecond
	v.timeout = fetchTimeout
	ctx := context.Background()

	const n = 40
	start := time.Now()
	errs := make(chan *tokenError, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, te := v.verify(ctx, g.url, "Bearer "+tok)
			errs <- te
		}()
	}
	wg.Wait()
	close(errs)
	took := time.Since(start)
	for te := range errs {
		if te == nil || !te.unavailable {
			t.Fatalf("a concurrent upload got %v, want gitlab_unavailable", te)
		}
	}
	if took > 3*fetchTimeout {
		t.Errorf("%d uploads took %v with a fetch timeout of %v: they did not share the fetch", n, took, fetchTimeout)
	}
	if got := h.hits.Load(); got != 1 {
		t.Fatalf("%d fetches for %d concurrent uploads, want 1", got, n)
	}

	// Inside the interval: answered at once, no request.
	start = time.Now()
	for range 20 {
		if _, te := v.verify(ctx, g.url, "Bearer "+tok); te == nil || !te.unavailable {
			t.Fatalf("inside the interval: %v", te)
		}
	}
	if d := time.Since(start); d > fetchTimeout/2 {
		t.Errorf("uploads inside the interval took %v", d)
	}
	if got := h.hits.Load(); got != 1 {
		t.Errorf("%d fetches inside the interval, want 1", got)
	}

	// GitLab recovers; after the interval the keys are fetched and used.
	h.hang.Store(false)
	clock.advance(keyRefetchInterval)
	if _, te := v.verify(ctx, g.url, "Bearer "+tok); te != nil {
		t.Fatalf("after recovery: %v", te)
	}
	if got := h.hits.Load(); got != 2 {
		t.Errorf("%d fetches in all, want 2", got)
	}
}

// A request waits for the shared fetch only as long as its own context
// allows, and giving up does not cancel the fetch for the others.
func TestVerifyWaiterRespectsItsContext(t *testing.T) {
	h := newHangingGitLab(t)
	r := newRotating(t)
	s1 := r.newKey(t, "key-1")
	h.mu.Lock()
	h.keys = r.keys
	h.mu.Unlock()
	g := &gitlab{url: h.ts.URL}
	tok := r.sign(t, s1, g.claims())
	clock := newTestClock()
	v := clock.verifier()

	patient := make(chan *tokenError, 1)
	go func() {
		_, te := v.verify(context.Background(), g.url, "Bearer "+tok)
		patient <- te
	}()
	for h.hits.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, te := v.verify(ctx, g.url, "Bearer "+tok); te == nil || !te.unavailable {
		t.Fatalf("impatient waiter: %v", te)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("the waiter took %v to give up", d)
	}
	// The fetch goes on, and the patient one gets the keys.
	h.hang.Store(false)
	h.release <- struct{}{}
	select {
	case te := <-patient:
		if te != nil {
			t.Fatalf("patient waiter: %v", te)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the patient waiter was not answered")
	}
	if got := h.hits.Load(); got != 1 {
		t.Errorf("%d fetches, want 1", got)
	}
}
