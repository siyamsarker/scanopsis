// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeHistory drives a historyCache with a settable fingerprint, a clock
// and a computation that counts its runs and can be held until released.
type fakeHistory struct {
	mu      sync.Mutex
	print   string
	clock   time.Time
	calls   atomic.Int32
	hold    chan struct{} // when not nil, compute waits for it to close
	fail    error
	started chan struct{} // receives once per computation start
	sawCtx  chan context.Context
}

func newFake(t *testing.T) (*fakeHistory, *historyCache) {
	t.Helper()
	f := &fakeHistory{print: "a", clock: time.Unix(1_000_000, 0), started: make(chan struct{}, 100),
		sawCtx: make(chan context.Context, 100)}
	h := &historyCache{
		fingerprint: func(context.Context) (string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.print, nil
		},
		compute: func(ctx context.Context) ([]byte, error) {
			n := f.calls.Add(1)
			f.started <- struct{}{}
			f.sawCtx <- ctx
			f.mu.Lock()
			hold, fail, print := f.hold, f.fail, f.print
			f.mu.Unlock()
			if hold != nil {
				select {
				case <-hold:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			if fail != nil {
				return nil, fail
			}
			return []byte(fmt.Sprintf("%s#%d", print, n)), nil
		},
		now: func() time.Time {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.clock
		},
		grace:   30 * time.Second,
		maxAge:  2 * time.Minute,
		timeout: 5 * time.Second,
	}
	return f, h
}

func (f *fakeHistory) set(print string, advance time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.print = print
	f.clock = f.clock.Add(advance)
}

func getBody(t *testing.T, h *historyCache) string {
	t.Helper()
	b, err := h.get(context.Background())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return string(b)
}

// Concurrent requests share one computation, and while a computation runs
// nobody starts another: with a history cached they are served it at once.
func TestHistorySingleFlight(t *testing.T) {
	f, h := newFake(t)
	f.hold = make(chan struct{})
	bodies := make([]string, 20)
	var wg sync.WaitGroup
	for i := range bodies {
		wg.Go(func() { bodies[i] = getBody(t, h) })
	}
	<-f.started
	time.Sleep(20 * time.Millisecond) // let the others reach the wait
	close(f.hold)
	wg.Wait()
	for i, b := range bodies {
		if b != "a#1" {
			t.Fatalf("reader %d got %q, want a#1", i, b)
		}
	}
	if n := f.calls.Load(); n != 1 {
		t.Fatalf("%d computations, want 1", n)
	}

	// The data changes and the grace period is over: one request starts a
	// computation; the others are served the last history meanwhile.
	f.mu.Lock()
	f.hold = make(chan struct{})
	f.mu.Unlock()
	f.set("b", time.Minute)
	first := make(chan string)
	go func() { first <- getBody(t, h) }()
	<-f.started
	for range 10 {
		if b := getBody(t, h); b != "a#1" {
			t.Fatalf("during the computation got %q, want the last history a#1", b)
		}
	}
	close(f.hold)
	if b := <-first; b != "b#2" {
		t.Fatalf("the request that started it got %q, want b#2", b)
	}
	if n := f.calls.Load(); n != 2 {
		t.Fatalf("%d computations, want 2", n)
	}
}

// A request that waits gives up with its own context; the computation goes
// on, bounded by its own timeout, and its result serves the next request.
func TestHistoryTimeouts(t *testing.T) {
	f, h := newFake(t)
	f.hold = make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := h.get(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting request: %v, want its deadline", err)
	}
	computeCtx := <-f.sawCtx
	if computeCtx.Err() != nil {
		t.Fatal("the computation was cancelled with the request")
	}
	if deadline, ok := computeCtx.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
		t.Fatalf("computation deadline %v (%v), want within the 5 s timeout", deadline, ok)
	}
	close(f.hold)
	if b := getBody(t, h); b != "a#1" || f.calls.Load() != 1 {
		t.Fatalf("after the computation: %q with %d computations, want a#1 with 1", b, f.calls.Load())
	}

	// A computation that runs past its timeout fails, holds nothing, and the
	// next request tries again.
	_, h = newFake(t)
	h.timeout = 20 * time.Millisecond
	h.compute = func(ctx context.Context) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	start := time.Now()
	if _, err := h.get(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow computation: %v, want its deadline", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("slow computation ended after %v", took)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running != nil || h.body != nil {
		t.Fatal("a failed computation left state behind")
	}
}

// After the data changes, the last history is served for the grace period;
// a burst of changes costs one computation per period. Unchanged data is
// served until maxAge.
func TestHistoryGrace(t *testing.T) {
	f, h := newFake(t)
	if b := getBody(t, h); b != "a#1" {
		t.Fatalf("first = %q", b)
	}
	for i, step := range []struct {
		print   string
		advance time.Duration
		want    string
	}{
		{"b", 10 * time.Second, "a#1"},  // changed, within the grace period
		{"c", 19 * time.Second, "a#1"},  // 29 s after the computation
		{"d", 2 * time.Second, "d#2"},   // 31 s: computed again
		{"e", 5 * time.Second, "d#2"},   // a new grace period
		{"f", 24 * time.Second, "d#2"},  // 29 s after: still the last
		{"f", 1 * time.Second, "f#3"},   // 30 s after: computed again
		{"f", 110 * time.Second, "f#3"}, // unchanged, younger than maxAge
		{"f", 10 * time.Second, "f#4"},  // unchanged but too old
	} {
		f.set(step.print, step.advance)
		if b := getBody(t, h); b != step.want {
			t.Fatalf("step %d: %q, want %q", i, b, step.want)
		}
	}

	// A failed computation keeps the last history and is not retried
	// before the grace period ends.
	f.mu.Lock()
	f.fail = errors.New("database gone")
	f.mu.Unlock()
	f.set("g", 3*time.Minute)
	if _, err := h.get(context.Background()); err == nil {
		t.Fatal("failed computation: no error")
	}
	calls := f.calls.Load()
	f.set("g", 10*time.Second)
	if b := getBody(t, h); b != "f#4" || f.calls.Load() != calls {
		t.Fatalf("after a failure: %q with %d computations, want f#4 with %d", b, f.calls.Load(), calls)
	}
}

// Reset drops the cached history at once, whatever the fingerprint and the
// grace period say, and a computation that was running when the data was
// replaced does not put its old result back.
func TestHistoryCacheReset(t *testing.T) {
	f, h := newFake(t)
	if b := getBody(t, h); b != "a#1" {
		t.Fatalf("first = %q", b)
	}
	// Unchanged fingerprint, young history: served from the cache.
	if b := getBody(t, h); b != "a#1" || f.calls.Load() != 1 {
		t.Fatalf("second = %q after %d computations", b, f.calls.Load())
	}
	h.Reset()
	if b := getBody(t, h); b != "a#2" {
		t.Fatalf("after Reset = %q, want a fresh a#2", b)
	}

	// A computation in flight during Reset is not stored.
	f.mu.Lock()
	f.hold = make(chan struct{})
	hold := f.hold
	f.mu.Unlock()
	f.set("b", time.Minute)
	stale := make(chan string)
	go func() { stale <- getBody(t, h) }()
	for f.calls.Load() < 3 {
		time.Sleep(time.Millisecond)
	}
	// Drain the start signals of the three computations so far.
	<-f.started
	<-f.started
	h.Reset()
	f.mu.Lock()
	f.hold = nil
	f.print = "c"
	f.mu.Unlock()
	if b := getBody(t, h); b != "c#4" {
		t.Fatalf("after Reset during a computation = %q, want c#4", b)
	}
	close(hold)
	<-stale
	if b := getBody(t, h); b != "c#4" {
		t.Fatalf("a reset computation replaced the history: %q", b)
	}
}
