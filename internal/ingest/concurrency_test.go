// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"bytes"
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/settings"
)

// blockingSaver holds every save until it is released.
type blockingSaver struct {
	fakeSaver
	entered chan struct{}
	release chan struct{}
}

func (b *blockingSaver) save(ctx context.Context, in *saveInput) (Result, error) {
	b.entered <- struct{}{}
	<-b.release
	return b.fakeSaver.save(ctx, in)
}

// uploadAsync sends a valid upload in the background.
func uploadAsync(ctx context.Context, e *hEnv, body []byte) <-chan *httptest.ResponseRecorder {
	tok := e.g.token(e.t, nil)
	out := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		hr := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/ingest/trivy", bytes.NewReader(body))
		hr.Header.Set("Authorization", "Bearer "+tok)
		hr.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.in.Handlers()["ingestTrivy"].ServeHTTP(w, hr)
		out <- w
	}()
	return out
}

// budgetFor returns a memory budget that admits n uploads of body.
func budgetFor(body []byte, n int) int64 {
	return int64(n)*uploadWeight(int64(len(body)), DefaultMaxReportBytes) + 1
}

func TestIngestAdmitsUploadsByMemory(t *testing.T) {
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	e := newHEnv(t, func(o *Options) { o.MemoryBudget = budgetFor(body, 2) })
	bs := &blockingSaver{entered: make(chan struct{}, 16), release: make(chan struct{})}
	e.in.store = bs
	var results []<-chan *httptest.ResponseRecorder
	for range 2 {
		results = append(results, uploadAsync(context.Background(), e, body))
		<-bs.entered
	}
	if got, want := e.in.budget.inUse(), 2*uploadWeight(int64(len(body)), DefaultMaxReportBytes); got != want {
		t.Errorf("in use %d, want %d", got, want)
	}
	// The next upload waits for memory.
	third := uploadAsync(context.Background(), e, body)
	select {
	case <-bs.entered:
		t.Fatal("a third upload was processed concurrently")
	case <-third:
		t.Fatal("a third upload finished while the others hold the memory")
	case <-time.After(100 * time.Millisecond):
	}
	// A waiter whose client goes away is still answered 503 busy and
	// recorded, marked as gone in the server log.
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := uploadAsync(ctx, e, body)
	time.Sleep(50 * time.Millisecond)
	cancel()
	w := <-cancelled
	if w.Code != http.StatusServiceUnavailable || errCode(t, w) != "busy" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("%d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
	if ev := e.audit.list(); len(ev) != 1 || ev[0].detail["reason"] != "busy" || ev[0].detail["project_path"] != "platform/payments-api" {
		t.Errorf("audit = %+v", ev)
	}
	if !strings.Contains(e.logs.String(), "client_gone=true") {
		t.Errorf("the log does not mark the client as gone:\n%s", e.logs.String())
	}
	// Freed memory lets the waiter proceed.
	bs.release <- struct{}{}
	<-bs.entered
	close(bs.release)
	for _, r := range append(results, third) {
		if w := <-r; w.Code != http.StatusCreated {
			t.Errorf("%d %s", w.Code, w.Body)
		}
	}
	if n := e.in.budget.inUse(); n != 0 {
		t.Errorf("%d bytes still held", n)
	}
}

// Small uploads are not held back by a large one that fits beside them, and
// a large one waits until enough is free: the admission follows the size.
func TestIngestAdmissionFollowsSize(t *testing.T) {
	small := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	big := []byte(`{"SchemaVersion":2,"ArtifactName":"` + strings.Repeat("b", 400) + `"}`)
	// Room for the weight of one big upload and a little more.
	budget := uploadWeight(int64(len(big)), DefaultMaxReportBytes) + 4
	e := newHEnv(t, func(o *Options) { o.MemoryBudget = budget })
	bs := &blockingSaver{entered: make(chan struct{}, 16), release: make(chan struct{})}
	e.in.store = bs
	first := uploadAsync(context.Background(), e, big)
	<-bs.entered
	// The first holds almost everything: even a small upload must wait.
	second := uploadAsync(context.Background(), e, small)
	select {
	case <-bs.entered:
		t.Fatal("an upload was admitted beyond the budget")
	case <-time.After(100 * time.Millisecond):
	}
	close(bs.release)
	for _, r := range []<-chan *httptest.ResponseRecorder{first, second} {
		if w := <-r; w.Code != http.StatusCreated {
			t.Errorf("%d %s", w.Code, w.Body)
		}
	}
}

// An upload that needs more than the whole budget runs alone.
func TestIngestOversizedUploadRunsAlone(t *testing.T) {
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	e := newHEnv(t, func(o *Options) { o.MemoryBudget = 1024 })
	if w := e.do(req{body: body}); w.Code != http.StatusCreated {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if n := e.in.budget.inUse(); n != 0 {
		t.Errorf("%d bytes still held", n)
	}
}

// A waiter is answered 503 busy when the wait time passes, without its
// request ending.
func TestIngestAdmitWaitEnds(t *testing.T) {
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	e := newHEnv(t, func(o *Options) { o.MemoryBudget = budgetFor(body, 1); o.AdmitWait = 80 * time.Millisecond })
	bs := &blockingSaver{entered: make(chan struct{}, 4), release: make(chan struct{})}
	e.in.store = bs
	first := uploadAsync(context.Background(), e, body)
	<-bs.entered
	started := time.Now()
	w := <-uploadAsync(context.Background(), e, body)
	if w.Code != http.StatusServiceUnavailable || errCode(t, w) != "busy" || w.Header().Get("Retry-After") != "5" {
		t.Fatalf("%d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
	if d := time.Since(started); d < 70*time.Millisecond || d > 5*time.Second {
		t.Errorf("answered after %v", d)
	}
	close(bs.release)
	if w := <-first; w.Code != http.StatusCreated {
		t.Errorf("%d", w.Code)
	}
}

func TestUploadWeight(t *testing.T) {
	const limit = 64 << 20
	for _, tc := range []struct {
		name string
		cl   int64
		want int64
	}{
		{"known", 10 << 20, 20<<20 + uploadOverhead},
		{"unknown", -1, 2*limit + uploadOverhead},
		{"zero", 0, 2*limit + uploadOverhead},
		{"at the limit", limit, 2*limit + uploadOverhead},
	} {
		if got := uploadWeight(tc.cl, limit); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestBudgetFromLimit(t *testing.T) {
	old := debug.SetMemoryLimit(-1)
	defer debug.SetMemoryLimit(old)
	debug.SetMemoryLimit(math.MaxInt64)
	if got := budgetFromLimit(); got != defaultMemoryBudget {
		t.Errorf("no limit: %d", got)
	}
	debug.SetMemoryLimit(400 << 20)
	if got := budgetFromLimit(); got != 160<<20 {
		t.Errorf("400 MiB: %d", got)
	}
	debug.SetMemoryLimit(32 << 20)
	if got := budgetFromLimit(); got != minMemoryBudget {
		t.Errorf("32 MiB: %d", got)
	}
}

// Sizes are in units of u, so that every request here is above smallUpload.
func TestMemBudget(t *testing.T) {
	b, clock := clockedBudget(100 * u)
	ctx := context.Background()
	r1, err := b.acquire(ctx, 60*u)
	if err != nil {
		t.Fatal(err)
	}
	// 50 does not fit beside 60; once it has waited the fairness bound, a
	// later, smaller request does not jump the queue.
	got := make(chan string, 4)
	go func() {
		rel, _ := b.acquire(ctx, 50*u)
		got <- "50"
		rel()
	}()
	waitQueued(t, b, 1)
	clock.advance(defaultFairWait)
	go func() {
		rel, _ := b.acquire(ctx, 10*u)
		got <- "10"
		rel()
	}()
	select {
	case v := <-got:
		t.Fatalf("%s was admitted out of turn", v)
	case <-time.After(60 * time.Millisecond):
	}
	r1()
	// Both fit together once the first is released.
	if a, c := <-got, <-got; a+c != "5010" && a+c != "1050" {
		t.Errorf("admitted %s %s", a, c)
	}
	// A cancelled waiter leaves the queue and wakes the one behind it.
	r2, _ := b.acquire(ctx, 90*u)
	cctx, cancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() { _, err := b.acquire(cctx, 80*u); errc <- err }()
	waitQueued(t, b, 1)
	clock.advance(defaultFairWait)
	behind := make(chan struct{})
	go func() {
		rel, _ := b.acquire(ctx, 10*u) // fits beside 90 but waits behind the 80
		// Released before the signal, so the check of the memory in use
		// below cannot see it still held.
		rel()
		close(behind)
	}()
	select {
	case <-behind:
		t.Fatal("jumped the queue")
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	if err := <-errc; err == nil {
		t.Error("cancelled acquire succeeded")
	}
	<-behind
	r2()
	r2() // a second release changes nothing
	if n := b.inUse(); n != 0 {
		t.Errorf("%d in use", n)
	}
}

func TestIngestSlotIsReleasedAfterFailures(t *testing.T) {
	e := newHEnv(t, nil)
	for range 3 {
		if w := e.do(req{body: []byte(`{"SchemaVersion":`)}); w.Code != 400 {
			t.Fatalf("%d", w.Code)
		}
	}
	if w := e.do(req{body: []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)}); w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if n := e.in.budget.inUse(); n != 0 {
		t.Errorf("%d bytes still held", n)
	}
}

// Uploads that fail the token, namespace or ref check do not wait for memory.
func TestIngestRejectedUploadsDoNotWaitForMemory(t *testing.T) {
	e := newHEnv(t, func(o *Options) { o.MemoryBudget = 1024 })
	if _, err := e.in.budget.acquire(context.Background(), 1024); err != nil { // everything is taken
		t.Fatal(err)
	}
	if w := e.do(req{noAuth: true}); w.Code != 401 {
		t.Fatalf("bad token: %d", w.Code)
	}
	e.cfg.set(func(s *settings.Snapshot) { s.IngestAllowedNamespaces = []string{"other"} })
	if w := e.do(req{body: []byte(`{}`)}); w.Code != 403 {
		t.Fatalf("namespace: %d", w.Code)
	}
}

// The heap is returned to the operating system after a large upload when the
// server is idle, and not after small ones or while another upload runs.
func TestIngestTrimsMemoryAfterLargeUploads(t *testing.T) {
	e := newHEnv(t, nil)
	freed := make(chan struct{}, 4)
	e.in.freeMemory = func() { freed <- struct{}{} }

	e.in.trim(trimThreshold - 1)
	e.in.trim(trimThreshold)
	select {
	case <-freed:
	case <-time.After(2 * time.Second):
		t.Fatal("no trim after a large upload")
	}
	select {
	case <-freed:
		t.Fatal("trimmed after a small upload, or twice")
	case <-time.After(50 * time.Millisecond):
	}

	rel, err := e.in.budget.acquire(context.Background(), 1) // another upload runs
	if err != nil {
		t.Fatal(err)
	}
	e.in.trim(trimThreshold)
	select {
	case <-freed:
		t.Fatal("trimmed while an upload runs")
	case <-time.After(50 * time.Millisecond):
	}
	rel()

	// A real upload of a large size trims once it is stored.
	big := []byte(`{"SchemaVersion":2,"ArtifactName":"` + strings.Repeat("x", 11<<20) + `"}`)
	if w := e.do(req{body: big}); w.Code != http.StatusCreated {
		t.Fatalf("%d %.200s", w.Code, w.Body)
	}
	select {
	case <-freed:
	case <-time.After(2 * time.Second):
		t.Fatal("no trim after a large upload through the handler")
	}
}

// Busy answers happen under load, so they are audited only within the
// per-address bound, even though the token was valid.
func TestIngestBusyIsAuditedWithinTheBound(t *testing.T) {
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	e := newHEnv(t, func(o *Options) { o.MemoryBudget = budgetFor(body, 1); o.AdmitWait = 20 * time.Millisecond })
	bs := &blockingSaver{entered: make(chan struct{}, 16), release: make(chan struct{})}
	e.in.store = bs
	held := uploadAsync(context.Background(), e, body)
	<-bs.entered
	const n = 3 * DefaultAuditBurst
	for range n {
		w := <-uploadAsync(context.Background(), e, body)
		if w.Code != http.StatusServiceUnavailable || errCode(t, w) != "busy" || w.Header().Get("Retry-After") == "" {
			t.Fatalf("%d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body)
		}
	}
	close(bs.release)
	if w := <-held; w.Code != http.StatusCreated {
		t.Errorf("held upload: %d %s", w.Code, w.Body)
	}
	busy := 0
	for _, ev := range e.audit.list() {
		if ev.detail["reason"] == "busy" {
			busy++
		}
	}
	if busy != DefaultAuditBurst {
		t.Errorf("%d busy rejections recorded for %d answers, want %d", busy, n, DefaultAuditBurst)
	}
	// Busy answers have their own bound: a rejected token from the same
	// address is still recorded.
	before := len(e.audit.list())
	if w := e.do(req{noAuth: true}); w.Code != http.StatusUnauthorized {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := len(e.audit.list()); got != before+1 {
		t.Errorf("a rejected token after the busy answers was not recorded (%d → %d)", before, got)
	}
}
