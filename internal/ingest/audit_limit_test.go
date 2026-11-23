// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/auth"
)

func TestIngestRateLimitedRequestsAreNotAudited(t *testing.T) {
	e := newHEnv(t, func(o *Options) { o.Limiter = auth.NewRateLimiter(time.Hour, 1, nil) })
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	if w := e.do(req{body: body}); w.Code != 201 {
		t.Fatalf("%d", w.Code)
	}
	for range 50 {
		if w := e.do(req{body: body}); w.Code != 429 || errCode(t, w) != "too_many_requests" || w.Header().Get("Retry-After") == "" {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	if ev := e.audit.list(); len(ev) != 0 {
		t.Errorf("%d audit rows for rate-limited requests", len(ev))
	}
	if strings.Contains(e.logs.String(), "level=WARN") {
		t.Errorf("rate limit logged as a warning: %s", e.logs)
	}
}

func TestIngestAuditRowsPerAddressAreBounded(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	e := newHEnv(t, nil)
	e.in.auditLimit = auth.NewRateLimiter(time.Minute, DefaultAuditBurst, func() time.Time { return now })
	bad := map[string]string{"Authorization": "Bearer abc"}
	for i := range 300 {
		w := e.do(req{noAuth: true, header: bad})
		if w.Code != 401 || errCode(t, w) != "invalid_token" {
			t.Fatalf("request %d: %d %s", i, w.Code, w.Body)
		}
	}
	if n := len(e.audit.list()); n != DefaultAuditBurst {
		t.Fatalf("%d audit rows for 300 rejected requests, want %d", n, DefaultAuditBurst)
	}
	logs := e.logs.String()
	if n := strings.Count(logs, "level=WARN"); n != DefaultAuditBurst {
		t.Errorf("%d warnings, want %d", n, DefaultAuditBurst)
	}
	if !strings.Contains(logs, "level=DEBUG") || !strings.Contains(logs, "not recorded") {
		t.Errorf("suppressed rejections are not logged at debug level: %s", logs)
	}
	// Another address is still audited.
	e.in.opts.ClientIP = func(*http.Request) netip.Addr { return netip.MustParseAddr("192.0.2.99") }
	e.do(req{noAuth: true, header: bad})
	ev := e.audit.list()
	if len(ev) != DefaultAuditBurst+1 || ev[len(ev)-1].detail["ip"] != "192.0.2.99" {
		t.Fatalf("other address: %d rows, last %+v", len(ev), ev[len(ev)-1])
	}
	// The first address is audited again as time passes.
	e.in.opts.ClientIP = func(*http.Request) netip.Addr { return netip.MustParseAddr("192.0.2.10") }
	now = now.Add(time.Minute)
	e.do(req{noAuth: true, header: bad})
	if n := len(e.audit.list()); n != DefaultAuditBurst+2 {
		t.Errorf("after a minute: %d rows", n)
	}
}

func TestIngestDefaultAuditLimiter(t *testing.T) {
	e := newHEnv(t, nil)
	for range 30 {
		e.do(req{noAuth: true})
	}
	if n := len(e.audit.list()); n != DefaultAuditBurst {
		t.Errorf("%d rows, want %d", n, DefaultAuditBurst)
	}
}

func TestIngestAuditLimiterOption(t *testing.T) {
	e := newHEnv(t, func(o *Options) { o.AuditLimiter = auth.NewRateLimiter(time.Hour, 2, nil) })
	for range 5 {
		e.do(req{noAuth: true})
	}
	if n := len(e.audit.list()); n != 2 {
		t.Errorf("%d rows, want 2", n)
	}
}
