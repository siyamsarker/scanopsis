// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sendHeaders opens a connection and sends the head of a POST that announces
// a body of n bytes from the given client address, without the body.
func sendHeaders(t *testing.T, addr, clientIP string, n int) net.Conn {
	t.Helper()
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_, _ = fmt.Fprintf(conn, "POST /api/v1/services HTTP/1.1\r\nHost: x\r\nContent-Length: %d\r\n"+
		"Content-Type: application/json\r\n%s: admin\r\n%s: %s\r\n\r\n", n, roleHeader, ipHeader, clientIP)
	return conn
}

// statusLine reads the status line of the answer on conn, or "" when none
// comes in time.
func statusLine(conn net.Conn, within time.Duration) string {
	_ = conn.SetReadDeadline(time.Now().Add(within))
	buf := make([]byte, 64)
	n, _ := conn.Read(buf)
	line, _, _ := strings.Cut(string(buf[:n]), "\r\n")
	return line
}

// shortBodyTimeout lets unsent bodies expire soon, so that closing the test
// server does not wait for the real read deadline.
func shortBodyTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := bodyReadTimeout
	bodyReadTimeout = d
	t.Cleanup(func() { bodyReadTimeout = old })
}

func postBody(t *testing.T, url, clientIP string) (int, time.Duration) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url+"/api/v1/services",
		strings.NewReader(strings.Repeat("x", maxAPIBody)))
	req.Header.Set(roleHeader, "admin")
	req.Header.Set(ipHeader, clientIP)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode, time.Since(start)
}

// Many requests from one address that announce a body and do not send it
// hold bytes, not slots: they are held to the address's part of the body
// memory, so another address's request is not made busy, and a request of the
// same address without a body, as another user behind the same proxy would
// send, is served at once.
func TestConcurrencyLimitSlowBodiesOfOneClientHoldNoSlots(t *testing.T) {
	shortBodyTimeout(t, 1500*time.Millisecond)
	g := newGate()
	close(g.release)
	e := newEnv(t, func(d *Deps) {
		limited(g, 16, 100*time.Millisecond)(d)
		d.Features = map[string]http.Handler{"createService": g, "getPortfolio": g}
	})
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	perClient := bodyBudget / clientBodyShare / maxAPIBody
	var conns []net.Conn
	for range perClient + 4 {
		conns = append(conns, sendHeaders(t, srv.Listener.Addr().String(), ip(1), maxAPIBody))
	}
	time.Sleep(400 * time.Millisecond) // the ones over the byte share have given up

	busy := 0
	for _, c := range conns {
		if strings.Contains(statusLine(c, 50*time.Millisecond), "503") {
			busy++
		}
	}
	if busy != 4 {
		t.Errorf("%d of the slow requests were answered 503, want the 4 over the byte share of %d bodies", busy, perClient)
	}

	// The same address without a body, and another address with one, are served at once.
	for _, c := range []struct {
		name string
		do   func() (int, time.Duration)
	}{
		{"same address, no body", func() (int, time.Duration) {
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/api/v1/portfolio", nil)
			req.Header.Set(roleHeader, "viewer")
			req.Header.Set(ipHeader, ip(1))
			start := time.Now()
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			return resp.StatusCode, time.Since(start)
		}},
		{"another address, with a body", func() (int, time.Duration) { return postBody(t, srv.URL, ip(2)) }},
	} {
		if code, took := c.do(); code != 200 || took > time.Second {
			t.Errorf("%s: %d after %v, want 200 at once", c.name, code, took)
		}
	}
}

// One address cannot reserve more than its part of the body budget, even with
// many slots: 8 MiB of the 32 MiB, so further bodies of 1 MiB are answered
// busy while another address still gets room.
func TestConcurrencyLimitCapsBodyBytesOfOneClient(t *testing.T) {
	shortBodyTimeout(t, 1500*time.Millisecond)
	g := newGate()
	close(g.release)
	e := newEnv(t, func(d *Deps) {
		limited(g, 64, 100*time.Millisecond)(d)
		d.Features = map[string]http.Handler{"createService": g}
	})
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	var conns []net.Conn
	for range 10 {
		conns = append(conns, sendHeaders(t, srv.Listener.Addr().String(), ip(1), maxAPIBody))
	}
	time.Sleep(400 * time.Millisecond)
	busy := 0
	for _, c := range conns {
		if strings.Contains(statusLine(c, 50*time.Millisecond), "503") {
			busy++
		}
	}
	if want := 10 - bodyBudget/clientBodyShare/maxAPIBody; busy != want {
		t.Errorf("%d answered 503, want %d: one address may reserve %d MiB", busy, want, bodyBudget/clientBodyShare>>20)
	}
	if code, took := postBody(t, srv.URL, ip(2)); code != 200 || took > time.Second {
		t.Errorf("another address: %d after %v, want 200 at once", code, took)
	}
}

// Unsent bodies are answered 408 after the short read deadline and their
// bytes are free again: with the address's byte share full of them, a further
// body is busy, and once they have expired a complete request is served.
func TestConcurrencyLimitReleasesUnsentBodies(t *testing.T) {
	shortBodyTimeout(t, 400*time.Millisecond)
	g := newGate()
	close(g.release)
	e := newEnv(t, func(d *Deps) {
		limited(g, 4, 100*time.Millisecond)(d)
		d.Features = map[string]http.Handler{"createService": g}
	})
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	perClient := bodyBudget / clientBodyShare / maxAPIBody
	var unsent []net.Conn
	for range perClient {
		unsent = append(unsent, sendHeaders(t, srv.Listener.Addr().String(), ip(1), maxAPIBody))
	}
	time.Sleep(50 * time.Millisecond)
	extra := sendHeaders(t, srv.Listener.Addr().String(), ip(1), maxAPIBody)
	if got := statusLine(extra, 2*time.Second); !strings.Contains(got, "503") {
		t.Errorf("a body over the address's byte share = %q, want 503", got)
	}
	for i, c := range unsent {
		if got := statusLine(c, 3*time.Second); !strings.Contains(got, "408") {
			t.Errorf("unsent body %d: %q, want 408 after the read deadline", i, got)
		}
	}
	if code, _ := postBody(t, srv.URL, ip(1)); code != 200 {
		t.Errorf("request after the release = %d, want 200", code)
	}
}

// Every body counts at least 64 KiB, so a burst of slow bodies that announce a
// single byte is limited by the address's byte share like larger ones: 8 MiB
// is 128 of them, not thousands.
func TestConcurrencyLimitChargesAtLeastTheMinimumWeight(t *testing.T) {
	shortBodyTimeout(t, 1500*time.Millisecond)
	g := newGate()
	close(g.release)
	e := newEnv(t, func(d *Deps) {
		limited(g, 64, 100*time.Millisecond)(d)
		d.Features = map[string]http.Handler{"createService": g}
	})
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	if got := bodyWeight(&http.Request{Body: io.NopCloser(strings.NewReader("x")), ContentLength: 1}); got != minBodyWeight {
		t.Fatalf("weight of a 1-byte body = %d, want %d", got, minBodyWeight)
	}
	perClient := int(bodyBudget / clientBodyShare / minBodyWeight)
	var conns []net.Conn
	for range perClient + 20 {
		conns = append(conns, sendHeaders(t, srv.Listener.Addr().String(), ip(1), 1))
	}
	time.Sleep(400 * time.Millisecond)
	busy := 0
	for _, c := range conns {
		if strings.Contains(statusLine(c, 20*time.Millisecond), "503") {
			busy++
		}
	}
	for _, c := range conns {
		_ = c.Close() // a server drains a tiny unread body until the client closes
	}
	if busy != 20 {
		t.Errorf("%d answered 503, want the 20 beyond the %d bodies of the address's share", busy, perClient)
	}
}
