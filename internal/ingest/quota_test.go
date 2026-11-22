// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/auth"
)

func (e *hEnv) doAs(projectID int, body []byte) (code int, retryAfter, errorCode string) {
	e.t.Helper()
	w := e.doToken(e.g.token(e.t, func(c map[string]any) {
		c["project_id"] = strconv.Itoa(projectID)
		c["project_path"] = "platform/p" + strconv.Itoa(projectID)
	}), body)
	if w.Code >= 400 {
		errorCode = errCode(e.t, w)
	}
	return w.Code, w.Header().Get("Retry-After"), errorCode
}

// A project may upload its burst and then is answered 429 with Retry-After,
// before its body is read; another project is not affected; the rejection is
// audited, bounded per client address.
func TestIngestProjectRateLimit(t *testing.T) {
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	now := time.Now()
	clock := func() time.Time { return now }
	e := newHEnv(t, func(o *Options) { o.ProjectLimiter = auth.NewRateLimiter(10*time.Second, 3, clock) })

	for i := range 3 {
		if code, _, _ := e.doAs(42, body); code != http.StatusCreated {
			t.Fatalf("upload %d: %d", i, code)
		}
	}
	code, retry, ec := e.doAs(42, body)
	if code != http.StatusTooManyRequests || ec != "project_rate_limited" || retry != "10" {
		t.Fatalf("over the burst: %d %q %q", code, retry, ec)
	}
	if len(e.save.calls) != 3 {
		t.Errorf("%d uploads stored", len(e.save.calls))
	}
	if n := e.in.budget.inUse(); n != 0 {
		t.Errorf("%d bytes held by a rejected upload", n)
	}
	// Another project has its own budget.
	if code, _, _ := e.doAs(43, body); code != http.StatusCreated {
		t.Errorf("another project: %d", code)
	}
	// The rejection is recorded with the project.
	ev := e.audit.list()
	if len(ev) != 1 || ev[0].action != "ingest.rejected" || ev[0].detail["reason"] != "project_rate_limited" || ev[0].detail["project_path"] != "platform/p42" || ev[0].actor != "ci:platform/p42" {
		t.Fatalf("audit = %+v", ev)
	}
	// A token comes back with the time.
	now = now.Add(10 * time.Second)
	if code, _, _ := e.doAs(42, body); code != http.StatusCreated {
		t.Errorf("after the wait: %d", code)
	}
	// A flood does not fill the audit log: at most the audit burst per address.
	for range 40 {
		e.doAs(42, body)
	}
	if n := len(e.audit.list()); n > DefaultAuditBurst {
		t.Errorf("%d audit records for a flood", n)
	}
}

// The default limit is on without configuration: DefaultProjectBurst uploads,
// then 429.
func TestIngestProjectRateLimitDefault(t *testing.T) {
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	e := newHEnv(t, nil)
	for i := range DefaultProjectBurst {
		if code, _, _ := e.doAs(7, body); code != http.StatusCreated {
			t.Fatalf("upload %d: %d", i, code)
		}
	}
	if code, retry, ec := e.doAs(7, body); code != http.StatusTooManyRequests || ec != "project_rate_limited" || retry == "" {
		t.Fatalf("%d %q %q", code, retry, ec)
	}
}

// The project limit applies after the allow-list checks, so only projects
// this instance accepts use their budget, and it is separate from the limit
// per client address.
func TestIngestProjectLimitIsSeparateFromTheAddressLimit(t *testing.T) {
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	now := time.Now()
	clock := func() time.Time { return now }
	e := newHEnv(t, func(o *Options) {
		o.ProjectLimiter = auth.NewRateLimiter(time.Hour, 1, clock)
		o.Limiter = auth.NewRateLimiter(time.Hour, 5, clock)
	})
	if code, _, _ := e.doAs(1, body); code != 201 {
		t.Fatalf("%d", code)
	}
	if code, _, ec := e.doAs(1, body); code != 429 || ec != "project_rate_limited" {
		t.Fatalf("%d %s", code, ec)
	}
	// The address still has tokens, and a different project passes.
	if code, _, _ := e.doAs(2, body); code != 201 {
		t.Fatalf("%d", code)
	}
	// The address limit answers with its own code.
	for range 3 {
		e.doAs(3, body)
	}
	if code, _, ec := e.doAs(4, body); code != 429 || ec != "too_many_requests" {
		t.Fatalf("%d %s", code, ec)
	}
}

func TestRetrySeconds(t *testing.T) {
	for in, want := range map[time.Duration]int{0: 1, time.Millisecond: 1, time.Second: 1, 1500 * time.Millisecond: 2, 10 * time.Second: 10, 59*time.Second + 1: 60} {
		if got := retrySeconds(in); got != want {
			t.Errorf("%v: %d, want %d", in, got, want)
		}
	}
}
