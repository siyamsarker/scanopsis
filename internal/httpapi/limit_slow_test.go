// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"fmt"
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
// cannot make another address's request busy: they are held to their
// address's share of the slots and of the body memory.
func TestConcurrencyLimitSlowBodiesOfOneClientDoNotBlockAnother(t *testing.T) {
	shortBodyTimeout(t, 1500*time.Millisecond)
	g := newGate()
	close(g.release)
	e := newEnv(t, func(d *Deps) {
		limited(g, 16, 100*time.Millisecond)(d)
		d.Features = map[string]http.Handler{"createService": g, "getPortfolio": g}
	})
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	var conns []net.Conn
	for range 12 {
		conns = append(conns, sendHeaders(t, srv.Listener.Addr().String(), ip(1), maxAPIBody))
	}
	time.Sleep(400 * time.Millisecond) // the ones over the share have given up

	busy := 0
	for _, c := range conns {
		if strings.Contains(statusLine(c, 50*time.Millisecond), "503") {
			busy++
		}
	}
	if want := 12 - 16/clientShareDivisor; busy != want {
		t.Errorf("%d of the slow requests were answered 503, want %d (all over the share of %d)", busy, want, 16/clientShareDivisor)
	}

	// The other address is served at once, although 4 bodies are reserved.
	if code, took := postBody(t, srv.URL, ip(2)); code != 200 || took > time.Second {
		t.Errorf("another address: %d after %v, want 200 at once", code, took)
	}
}

// One address cannot reserve more than its part of the body budget, even with
// many slots: 8 MiB of the 32 MiB, so a ninth body of 1 MiB waits and is
// answered busy while another address still gets room.
func TestConcurrencyLimitCapsBodyBytesOfOneClient(t *testing.T) {
	shortBodyTimeout(t, 1500*time.Millisecond)
	g := newGate()
	close(g.release)
	e := newEnv(t, func(d *Deps) {
		limited(g, 64, 100*time.Millisecond)(d) // a share of 16 slots
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

// A client that reserves bytes with a Content-Length and does not send the
// body is answered 408 after the short read deadline, and the reservation and
// the share are free again.
func TestConcurrencyLimitReleasesUnsentBodies(t *testing.T) {
	shortBodyTimeout(t, 300*time.Millisecond)

	g := newGate()
	close(g.release)
	e := newEnv(t, func(d *Deps) {
		limited(g, 4, 100*time.Millisecond)(d)
		d.Features = map[string]http.Handler{"createService": g}
	})
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	// With 4 slots one address holds 1: its second request is busy while the
	// first waits for its body.
	first := sendHeaders(t, srv.Listener.Addr().String(), ip(1), maxAPIBody)
	time.Sleep(50 * time.Millisecond)
	second := sendHeaders(t, srv.Listener.Addr().String(), ip(1), maxAPIBody)
	if got := statusLine(second, 2*time.Second); !strings.Contains(got, "503") {
		t.Errorf("second request of the address = %q, want 503 while the first holds the share", got)
	}
	if got := statusLine(first, 3*time.Second); !strings.Contains(got, "408") {
		t.Errorf("unsent body: %q, want 408 after the read deadline", got)
	}
	if code, _ := postBody(t, srv.URL, ip(1)); code != 200 {
		t.Errorf("request after the release = %d, want 200", code)
	}
}
