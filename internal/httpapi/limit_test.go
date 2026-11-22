// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// gate is a handler that holds every request until released and counts how
// many are inside at once.
type gate struct {
	release chan struct{}
	entered chan struct{}
	mu      sync.Mutex
	active  int
	peak    int
}

func newGate() *gate {
	return &gate{release: make(chan struct{}), entered: make(chan struct{}, 1000)}
}

func (g *gate) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	g.mu.Lock()
	g.active++
	g.peak = max(g.peak, g.active)
	g.mu.Unlock()
	g.entered <- struct{}{}
	<-g.release
	g.mu.Lock()
	g.active--
	g.mu.Unlock()
	WriteJSON(w, http.StatusOK, statusBody{Status: "ok"})
}

// With 100 concurrent readers and room for 4, no more than 4 run at once, the
// rest are answered 503 busy with Retry-After after the short wait instead of
// queueing, and a slot is free again once its request is done.
func TestConcurrencyLimitAnswersBusy(t *testing.T) {
	const limit, readers = 4, 100
	g := newGate()
	e := newEnv(t, func(d *Deps) {
		d.MaxConcurrent = limit
		d.BusyWait = 50 * time.Millisecond
		d.Features = map[string]http.Handler{"getPortfolio": g}
	})

	recs := make([]*httptest.ResponseRecorder, readers)
	var wg sync.WaitGroup
	for i := range readers {
		wg.Go(func() { recs[i] = e.do("GET", "/api/v1/portfolio", "viewer", "") })
	}
	for range limit {
		<-g.entered // the admitted requests are inside
	}

	// The probes, the web interface and the Trivy upload are not counted:
	// they are answered while every slot is taken.
	for _, target := range []string{"/healthz", "/readyz", "/", "/assets/app.js"} {
		if rec := e.do("GET", target, "", ""); rec.Code != 200 {
			t.Errorf("GET %s with every slot taken = %d, want 200", target, rec.Code)
		}
	}
	time.Sleep(200 * time.Millisecond) // longer than BusyWait: the waiting requests give up
	close(g.release)
	wg.Wait()

	ok, busy := 0, 0
	for _, rec := range recs {
		switch rec.Code {
		case 200:
			ok++
		case 503:
			busy++
			assertError(t, rec, 503, "busy")
			if got := rec.Header().Get("Retry-After"); got != "2" {
				t.Errorf("Retry-After = %q, want 2", got)
			}
		default:
			t.Errorf("unexpected status %d", rec.Code)
		}
	}
	if ok != limit || busy != readers-limit {
		t.Errorf("%d answered 200 and %d answered 503, want %d and %d", ok, busy, limit, readers-limit)
	}
	if g.peak != limit {
		t.Errorf("peak concurrency = %d, want %d", g.peak, limit)
	}

	// Slots are returned: new requests are served again.
	g2 := newGate()
	close(g2.release)
	e2 := newEnv(t, func(d *Deps) {
		d.MaxConcurrent = 1
		d.BusyWait = 50 * time.Millisecond
		d.Features = map[string]http.Handler{"getPortfolio": g2}
	})
	for range 3 {
		if rec := e2.do("GET", "/api/v1/portfolio", "viewer", ""); rec.Code != 200 {
			t.Fatalf("sequential request = %d, want 200: the slot was not released", rec.Code)
		}
	}
}

// A request that waits for a slot is served as soon as one frees up, without
// waiting for the full BusyWait.
func TestConcurrencyLimitWaitsForASlot(t *testing.T) {
	g := newGate()
	e := newEnv(t, func(d *Deps) {
		d.MaxConcurrent = 1
		d.BusyWait = 5 * time.Second
		d.Features = map[string]http.Handler{"getPortfolio": g}
	})
	var first, second *httptest.ResponseRecorder
	var wg sync.WaitGroup
	wg.Go(func() { first = e.do("GET", "/api/v1/portfolio", "viewer", "") })
	<-g.entered
	wg.Go(func() { second = e.do("GET", "/api/v1/portfolio", "viewer", "") })
	time.Sleep(50 * time.Millisecond)
	close(g.release)
	wg.Wait()
	if first.Code != 200 || second.Code != 200 {
		t.Errorf("statuses %d and %d, want both 200", first.Code, second.Code)
	}
}

// The upload is admitted by the ingest package, not counted here: it is
// served while every slot is taken, and it does not take one.
func TestConcurrencyLimitSkipsUpload(t *testing.T) {
	hold := newGate()
	up := newGate()
	close(up.release)
	e := newEnv(t, func(d *Deps) {
		d.MaxConcurrent = 1
		d.BusyWait = 20 * time.Millisecond
		d.Features = map[string]http.Handler{"getPortfolio": hold, "ingestTrivy": up}
	})
	var wg sync.WaitGroup
	wg.Go(func() { e.do("GET", "/api/v1/portfolio", "viewer", "") })
	<-hold.entered
	for range 3 {
		if rec := e.do("POST", uploadPath, "", "{}"); rec.Code != 200 {
			t.Errorf("upload while every slot is taken = %d, want 200", rec.Code)
		}
	}
	if rec := e.do("GET", "/api/v1/services", "viewer", ""); rec.Code != 503 {
		t.Errorf("another API request = %d, want 503", rec.Code)
	}
	close(hold.release)
	wg.Wait()
}

func TestConcurrencyLimitDisabledByDefault(t *testing.T) {
	g := newGate()
	e := newEnv(t, func(d *Deps) { d.Features = map[string]http.Handler{"getPortfolio": g} })
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { e.do("GET", "/api/v1/portfolio", "viewer", "") })
	}
	for range 20 {
		<-g.entered
	}
	close(g.release)
	wg.Wait()
}
