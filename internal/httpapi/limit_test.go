// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const ipHeader = "X-Test-IP"

// ipFromHeader gives each test client its own address.
func ipFromHeader(r *http.Request) netip.Addr {
	if v := r.Header.Get(ipHeader); v != "" {
		return netip.MustParseAddr(v)
	}
	return netip.MustParseAddr("192.0.2.7")
}

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

func limited(g http.Handler, limit int, wait time.Duration) func(*Deps) {
	return func(d *Deps) {
		d.MaxConcurrent = limit
		d.BusyWait = wait
		d.ClientIP = ipFromHeader
		d.Features = map[string]http.Handler{"getPortfolio": g, "ingestTrivy": g}
	}
}

func ip(i int) string { return fmt.Sprintf("198.51.100.%d", i) }

// With 100 readers, each from its own address, and room for 4, no more than 4
// run at once, the rest are answered 503 busy with Retry-After after the short
// wait instead of queueing, and the slots are returned afterwards.
func TestConcurrencyLimitAnswersBusy(t *testing.T) {
	const limit, readers = 4, 100
	g := newGate()
	e := newEnv(t, limited(g, limit, 50*time.Millisecond))

	recs := make([]*httptest.ResponseRecorder, readers)
	var wg sync.WaitGroup
	for i := range readers {
		wg.Go(func() { recs[i] = e.do("GET", "/api/v1/portfolio", "viewer", "", ipHeader, ip(i+1)) })
	}
	for range limit {
		<-g.entered
	}
	// The probes and the web interface are not counted.
	for _, target := range []string{"/healthz", "/readyz", "/", "/assets/app.js"} {
		if rec := e.do("GET", target, "", ""); rec.Code != 200 {
			t.Errorf("GET %s with every slot taken = %d, want 200", target, rec.Code)
		}
	}
	time.Sleep(200 * time.Millisecond) // longer than the wait: the others give up
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
	if ok != limit || busy != readers-limit || g.peak != limit {
		t.Errorf("%d served, %d busy, peak %d; want %d, %d, %d", ok, busy, g.peak, limit, readers-limit, limit)
	}

	g2 := newGate()
	close(g2.release)
	e2 := newEnv(t, limited(g2, 1, 50*time.Millisecond))
	for range 3 {
		if rec := e2.do("GET", "/api/v1/portfolio", "viewer", ""); rec.Code != 200 {
			t.Fatalf("sequential request = %d: the slot was not released", rec.Code)
		}
	}
}

// A request that waits for a slot is served as soon as one frees up.
func TestConcurrencyLimitWaitsForASlot(t *testing.T) {
	g := newGate()
	e := newEnv(t, limited(g, 1, 5*time.Second))
	var first, second *httptest.ResponseRecorder
	var wg sync.WaitGroup
	wg.Go(func() { first = e.do("GET", "/api/v1/portfolio", "viewer", "", ipHeader, ip(1)) })
	<-g.entered
	wg.Go(func() { second = e.do("GET", "/api/v1/portfolio", "viewer", "", ipHeader, ip(2)) })
	time.Sleep(50 * time.Millisecond)
	close(g.release)
	wg.Wait()
	if first.Code != 200 || second.Code != 200 {
		t.Errorf("statuses %d and %d, want both 200", first.Code, second.Code)
	}
}

// One address holds at most a quarter of the slots: with 8 slots, 2. Its other
// requests are answered 503 busy, while another address is still served.
func TestConcurrencyLimitCapsOneClient(t *testing.T) {
	const limit = 8
	g := newGate()
	e := newEnv(t, limited(g, limit, 100*time.Millisecond))

	const greedy = 20
	recs := make([]*httptest.ResponseRecorder, greedy)
	var wg sync.WaitGroup
	for i := range greedy {
		wg.Go(func() { recs[i] = e.do("GET", "/api/v1/portfolio", "viewer", "", ipHeader, ip(1)) })
	}
	for range limit / 4 {
		<-g.entered
	}
	time.Sleep(300 * time.Millisecond) // the rest of the greedy client gives up
	// Another address is served while the first still holds its share.
	var other *httptest.ResponseRecorder
	wg.Go(func() { other = e.do("GET", "/api/v1/portfolio", "viewer", "", ipHeader, ip(2)) })
	<-g.entered
	close(g.release)
	wg.Wait()

	ok, busy := 0, 0
	for _, rec := range recs {
		switch rec.Code {
		case 200:
			ok++
		case 503:
			busy++
		}
	}
	if ok != limit/4 || busy != greedy-limit/4 {
		t.Errorf("greedy client: %d served, %d busy, want %d and %d", ok, busy, limit/4, greedy-limit/4)
	}
	if other.Code != 200 {
		t.Errorf("another address = %d, want 200", other.Code)
	}
	if g.peak > limit/4+1 {
		t.Errorf("peak %d: one client held more than its share", g.peak)
	}
}

func TestClientKey(t *testing.T) {
	k := func(s string) string {
		p, ok := clientKey(netip.MustParseAddr(s))
		if !ok {
			return "-"
		}
		return p.String()
	}
	for in, want := range map[string]string{
		"192.0.2.7":                  "192.0.2.7/32",
		"::ffff:192.0.2.7":           "192.0.2.7/32",
		"2001:db8:1:2:aaaa:bbbb:1:2": "2001:db8:1:2::/64",
		"2001:db8:1:2:cccc:dddd:3:4": "2001:db8:1:2::/64",
	} {
		if got := k(in); got != want {
			t.Errorf("clientKey(%s) = %s, want %s", in, got, want)
		}
	}
	if _, ok := clientKey(netip.Addr{}); ok {
		t.Error("an invalid address has a key")
	}
}

// A client that sends its body slowly holds no slot while it does, not even
// of its own address: a request of the same address, which many users may
// share, is served.
func TestConcurrencyLimitSlowBodyHoldsNoSlot(t *testing.T) {
	var inHandler atomic.Int32
	read := make(chan string, 1)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inHandler.Add(1)
		b, _ := io.ReadAll(r.Body)
		read <- string(b)
		inHandler.Add(-1)
		WriteJSON(w, http.StatusOK, statusBody{Status: "ok"})
	})
	e := newEnv(t, func(d *Deps) {
		d.MaxConcurrent = 1
		d.BusyWait = 50 * time.Millisecond
		d.ClientIP = ipFromHeader
		d.Features = map[string]http.Handler{"createService": h, "getPortfolio": h}
	})
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	// A request whose body arrives slowly: the header is sent, the body is not.
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = fmt.Fprintf(conn, "POST /api/v1/services HTTP/1.1\r\nHost: x\r\nContent-Length: 10\r\n"+
		"Content-Type: application/json\r\n%s: admin\r\n%s: %s\r\n\r\n12345", roleHeader, ipHeader, ip(1))
	time.Sleep(200 * time.Millisecond)
	if inHandler.Load() != 0 {
		t.Fatal("the handler ran before the body was complete")
	}

	// With the only slot free, another request is served at once.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/api/v1/portfolio", nil)
	req.Header.Set(roleHeader, "viewer")
	req.Header.Set(ipHeader, ip(1))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("request while another sends its body slowly = %d, want 200", resp.StatusCode)
	}
	<-read // drain the GET's (empty) body

	// The rest of the body arrives: the buffered request runs with all of it.
	_, _ = io.WriteString(conn, "67890")
	select {
	case got := <-read:
		if got != "1234567890" {
			t.Errorf("handler read %q, want the whole body", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the slow request did not complete")
	}
}

// liveServer serves e over HTTP. The limiter reads a body under a read
// deadline of its own, which a ResponseRecorder cannot set.
func liveServer(t *testing.T, e *env) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(e.h)
	t.Cleanup(srv.Close)
	return srv
}

// do sends a request to srv; a nil body has no body, otherwise rd is sent and
// length is its Content-Length (-1 for chunked).
func liveDo(t *testing.T, srv *httptest.Server, method, path, role, clientIP string, rd io.Reader, length int64) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = length
	if role != "" {
		req.Header.Set(roleHeader, role)
	}
	if clientIP != "" {
		req.Header.Set(ipHeader, clientIP)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	rec := httptest.NewRecorder()
	rec.Code = resp.StatusCode
	rec.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	rec.Header().Set("Retry-After", resp.Header.Get("Retry-After"))
	_, _ = io.Copy(rec.Body, resp.Body)
	return rec
}

// The limiter enforces the body limit itself: a body over it, announced by
// its Content-Length or not, is answered 413 and never reaches the handler.
func TestConcurrencyLimitRejectsLargeBodies(t *testing.T) {
	var called atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called.Add(1)
		WriteJSON(w, http.StatusOK, statusBody{Status: "ok"})
	})
	e := newEnv(t, func(d *Deps) {
		d.MaxConcurrent = 4
		d.Features = map[string]http.Handler{"createService": h}
	})
	srv := liveServer(t, e)
	big := strings.Repeat("x", maxAPIBody+10)
	assertError(t, liveDo(t, srv, "POST", "/api/v1/services", "admin", "", strings.NewReader(big), int64(len(big))), 413, "payload_too_large")
	// No Content-Length (chunked): the limit applies while reading.
	assertError(t, liveDo(t, srv, "POST", "/api/v1/services", "admin", "", io.NopCloser(strings.NewReader(big)), -1), 413, "payload_too_large")
	if called.Load() != 0 {
		t.Error("an oversized body reached the handler")
	}
	exact := strings.Repeat("x", maxAPIBody)
	if resp := liveDo(t, srv, "POST", "/api/v1/services", "admin", "", strings.NewReader(exact), int64(len(exact))); resp.Code != 200 {
		t.Errorf("a body of exactly the limit = %d, want 200", resp.Code)
	}
}

// A writer that cannot set a read deadline would leave the server's long
// read timeout for the body, so the request is refused rather than read.
func TestConcurrencyLimitRefusesWithoutAReadDeadline(t *testing.T) {
	var called atomic.Int32
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Add(1) })
	e := newEnv(t, func(d *Deps) {
		d.MaxConcurrent = 4
		d.Features = map[string]http.Handler{"createService": h}
	})
	assertError(t, e.do("POST", "/api/v1/services", "admin", `{"a":1}`), 500, "internal_error") // a recorder has no deadlines
	if called.Load() != 0 {
		t.Error("the handler ran for a body that could not be read under a deadline")
	}
	if bodyReadTimeout >= DefaultBusyWait {
		t.Errorf("the body read deadline %v is not shorter than the busy wait %v", bodyReadTimeout, DefaultBusyWait)
	}
}

// countingBody counts the bytes read from it.
type countingBody struct {
	left int64
	read atomic.Int64
}

func (c *countingBody) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, io.EOF
	}
	n := min(int64(len(p)), c.left)
	c.left -= n
	c.read.Add(n)
	return int(n), nil
}

// Paths under the upload prefix are outside both generic limits, the same
// test in both places: a huge body sent to one that is no route is not read
// into memory by the limiter.
func TestConcurrencyLimitDoesNotBufferIngestPaths(t *testing.T) {
	e := newEnv(t, func(d *Deps) { d.MaxConcurrent = 4 })
	for _, path := range []string{"/api/v1/ingest/anything", "/api/v1/ingest/trivy", "/api/v1/ingest/"} {
		body := &countingBody{left: 64 << 20}
		req := httptest.NewRequestWithContext(context.Background(), "POST", path, io.NopCloser(body))
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if n := body.read.Load(); n != 0 {
			t.Errorf("POST %s: %d bytes of the body were read by the server stack, want 0 (rejected without reading: %d)", path, n, rec.Code)
		}
	}
	if limitedAPIPath("/api/v1/ingest/anything") || limitedAPIPath(uploadPath) || !limitedAPIPath("/api/v1/services") || limitedAPIPath("/healthz") {
		t.Error("limitedAPIPath disagrees with the documented exemptions")
	}
}

// The bodies that wait for or hold a slot may take 32 MiB together: of many
// concurrent bodies of 1 MiB, 32 are admitted and the others are answered 503
// busy, although there are slots for all of them.
func TestConcurrencyLimitBoundsBodyMemory(t *testing.T) {
	const bodies = 64
	g := newGate()
	e := newEnv(t, func(d *Deps) {
		limited(g, 100, 100*time.Millisecond)(d)
		d.Features = map[string]http.Handler{"createService": g}
	})
	srv := liveServer(t, e)
	codes := make([]int, bodies)
	var wg sync.WaitGroup
	for i := range bodies {
		wg.Go(func() {
			body := strings.Repeat("x", maxAPIBody)
			req, _ := http.NewRequestWithContext(context.Background(), "POST", srv.URL+"/api/v1/services", strings.NewReader(body))
			req.Header.Set(roleHeader, "admin")
			req.Header.Set(ipHeader, ip(i+1))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			_ = resp.Body.Close()
			codes[i] = resp.StatusCode
		})
	}
	for range bodyBudget / maxAPIBody {
		<-g.entered
	}
	time.Sleep(400 * time.Millisecond)
	close(g.release)
	wg.Wait()
	ok, busy := 0, 0
	for _, c := range codes {
		switch c {
		case 200:
			ok++
		case 503:
			busy++
		}
	}
	if want := bodyBudget / maxAPIBody; ok != want || busy != bodies-want || g.peak != want {
		t.Errorf("%d served, %d busy, peak %d; want %d, %d, %d", ok, busy, g.peak, want, bodies-want, want)
	}
}

// The upload is admitted by the ingest package, not counted here.
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

type hangingPinger struct{ calls atomic.Int32 }

func (h *hangingPinger) Ping(ctx context.Context) error {
	h.calls.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

// Many concurrent /readyz calls against a database check that hangs share one
// check: it runs once, nobody queues behind it, every caller is answered 503
// within the readiness timeout, and the result is then reused for a second.
func TestReadyzSharesOneHangingCheck(t *testing.T) {
	p := &hangingPinger{}
	e := newEnv(t, func(d *Deps) { d.DB = p })
	const callers = 200
	codes := make([]int, callers)
	var wg sync.WaitGroup
	start := time.Now()
	for i := range callers {
		wg.Go(func() { codes[i] = e.do("GET", "/readyz", "", "").Code })
	}
	wg.Wait()
	if d := time.Since(start); d > readyTimeout+time.Second {
		t.Errorf("callers took %v, want about the readiness timeout of %v", d, readyTimeout)
	}
	for i, c := range codes {
		if c != 503 {
			t.Fatalf("caller %d got %d, want 503", i, c)
		}
	}
	if n := p.calls.Load(); n != 1 {
		t.Errorf("the database check ran %d times, want once", n)
	}
	// Within a second the answer comes from the cache, instantly.
	start = time.Now()
	if rec := e.do("GET", "/readyz", "", ""); rec.Code != 503 || time.Since(start) > 100*time.Millisecond || p.calls.Load() != 1 {
		t.Errorf("cached answer: %d in %v after %d checks", rec.Code, time.Since(start), p.calls.Load())
	}
}

type countingPinger struct{ calls atomic.Int32 }

func (c *countingPinger) Ping(context.Context) error { c.calls.Add(1); return nil }

// A healthy database is checked at most once per second, however often
// /readyz is called.
func TestReadyzChecksOncePerSecond(t *testing.T) {
	p := &countingPinger{}
	e := newEnv(t, func(d *Deps) { d.DB = p })
	for range 50 {
		if rec := e.do("GET", "/readyz", "", ""); rec.Code != 200 {
			t.Fatalf("readyz = %d", rec.Code)
		}
	}
	if n := p.calls.Load(); n != 1 {
		t.Errorf("%d checks for 50 calls within a second, want 1", n)
	}
	time.Sleep(readyCacheTTL + 100*time.Millisecond)
	e.do("GET", "/readyz", "", "")
	if n := p.calls.Load(); n != 2 {
		t.Errorf("%d checks after the cache expired, want 2", n)
	}
}

// A check that panics is answered as a failure, publishes its result and
// releases its waiters; the next check runs normally after the cache expires.
func TestReadyzSurvivesAPanickingCheck(t *testing.T) {
	var calls atomic.Int32
	e := newEnv(t, func(d *Deps) {
		d.DB = pingFunc(func(context.Context) error {
			if calls.Add(1) == 1 {
				panic("boom")
			}
			return nil
		})
	})
	const callers = 20
	codes := make([]int, callers)
	var wg sync.WaitGroup
	start := time.Now()
	for i := range callers {
		wg.Go(func() { codes[i] = e.do("GET", "/readyz", "", "").Code })
	}
	wg.Wait()
	if d := time.Since(start); d > time.Second {
		t.Errorf("callers waited %v for a check that panicked", d)
	}
	for i, c := range codes {
		if c != 503 {
			t.Errorf("caller %d got %d, want 503", i, c)
		}
	}
	time.Sleep(readyCacheTTL + 100*time.Millisecond)
	if rec := e.do("GET", "/readyz", "", ""); rec.Code != 200 {
		t.Errorf("readyz after the panic = %d, want 200", rec.Code)
	}
}

type pingFunc func(context.Context) error

func (f pingFunc) Ping(ctx context.Context) error { return f(ctx) }
