// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// countingSaver records how many saves run at once.
type countingSaver struct {
	fakeSaver
	cur, peak atomic.Int32
}

func (c *countingSaver) save(ctx context.Context, in *saveInput) (Result, error) {
	n := c.cur.Add(1)
	defer c.cur.Add(-1)
	for {
		p := c.peak.Load()
		if n <= p || c.peak.CompareAndSwap(p, n) {
			break
		}
	}
	time.Sleep(20 * time.Millisecond)
	return c.fakeSaver.save(ctx, in)
}

// More concurrent small uploads than there are database slots never store
// more than that many at once, and each succeeds or is answered busy.
func TestIngestLimitsUploadsInTheDatabasePhase(t *testing.T) {
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	const slots, uploads = 3, 24
	e := newHEnv(t, func(o *Options) {
		o.DBSlots = slots
		o.MemoryBudget = budgetFor(body, uploads)
		o.AdmitWait = 2 * time.Second
	})
	cs := &countingSaver{}
	e.in.store = cs
	var results []<-chan *httptest.ResponseRecorder
	for range uploads {
		results = append(results, uploadAsync(context.Background(), e, body))
	}
	for _, r := range results {
		if w := <-r; w.Code != http.StatusCreated && (w.Code != http.StatusServiceUnavailable || errCode(t, w) != "busy") {
			t.Errorf("%d %s", w.Code, w.Body)
		}
	}
	if got := cs.peak.Load(); got > slots || got < 2 {
		t.Errorf("peak of %d concurrent saves, want 2 to %d", got, slots)
	}
	if len(e.in.dbSlots) != 0 {
		t.Errorf("%d slots still held", len(e.in.dbSlots))
	}
}

// An upload that cannot get a database slot within the wait is answered 503
// busy with Retry-After, and gives its memory back.
func TestIngestBusyWhenNoDatabaseSlot(t *testing.T) {
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	e := newHEnv(t, func(o *Options) {
		o.DBSlots = 1
		o.MemoryBudget = budgetFor(body, 4)
		o.AdmitWait = 100 * time.Millisecond
	})
	bs := &blockingSaver{entered: make(chan struct{}, 4), release: make(chan struct{})}
	e.in.store = bs
	first := uploadAsync(context.Background(), e, body)
	<-bs.entered
	w := <-uploadAsync(context.Background(), e, body)
	if w.Code != http.StatusServiceUnavailable || errCode(t, w) != "busy" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("%d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
	close(bs.release)
	if w := <-first; w.Code != http.StatusCreated {
		t.Errorf("%d %s", w.Code, w.Body)
	}
	if n := e.in.budget.inUse(); n != 0 {
		t.Errorf("%d bytes still held", n)
	}
}

// The default number of slots is a quarter of the pool, and at least 2.
func TestIngestDBSlotsFollowThePool(t *testing.T) {
	for _, c := range []struct{ conns, want int }{{16, 4}, {32, 8}, {4, 2}, {1, 2}} {
		cfg, err := pgxpool.ParseConfig("postgres://u@127.0.0.1:1/db?pool_max_conns=" + strconv.Itoa(c.conns))
		if err != nil {
			t.Fatal(err)
		}
		pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		in := New(pool, &fakeSettings{}, &recorder{}, Options{})
		pool.Close()
		if got := cap(in.dbSlots); got != c.want {
			t.Errorf("MaxConns %d: %d slots, want %d", c.conns, got, c.want)
		}
	}
}
