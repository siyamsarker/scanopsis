// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	want := map[string]string{
		"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
			"font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'",
		"X-Content-Type-Options":     "nosniff",
		"X-Frame-Options":            "DENY",
		"Referrer-Policy":            "no-referrer",
		"Cross-Origin-Opener-Policy": "same-origin",
	}
	for _, secure := range []bool{false, true} {
		e := newEnv(t, func(d *Deps) { d.Secure = secure })
		for _, target := range []string{"/healthz", "/api/v1/nope", "/", "/missing.js"} {
			h := e.do("GET", target, "", "").Header()
			for k, v := range want {
				if h.Get(k) != v {
					t.Errorf("secure=%v %s: %s = %q, want %q", secure, target, k, h.Get(k), v)
				}
			}
			hsts := h.Get("Strict-Transport-Security")
			if secure && hsts != "max-age=63072000; includeSubDomains" {
				t.Errorf("%s: HSTS = %q", target, hsts)
			}
			if !secure && hsts != "" {
				t.Errorf("%s: HSTS must be absent over http, got %q", target, hsts)
			}
		}
	}
}

func TestCrossOriginProtection(t *testing.T) {
	e := newEnv(t, nil)
	cross := []string{"Sec-Fetch-Site", "cross-site"}
	same := []string{"Sec-Fetch-Site", "same-origin"}

	rec := e.do("POST", "/api/v1/auth/logout", "viewer", "", cross...)
	assertError(t, rec, 403, "cross_origin")
	if e.sessions.ended != 0 || len(e.audit.events) != 0 {
		t.Fatal("rejected request must not reach the handler")
	}
	if rec := e.do("POST", "/api/v1/auth/logout", "viewer", "", same...); rec.Code != 204 {
		t.Fatalf("same-origin logout = %d", rec.Code)
	}

	assertError(t, e.do("PATCH", "/api/v1/users/3", "admin", `{"role":"admin"}`, cross...), 403, "cross_origin")
	if e.users.users[2].Role != "viewer" {
		t.Fatal("cross-origin PATCH changed the role")
	}
	if rec := e.do("PATCH", "/api/v1/users/3", "admin", `{"role":"admin"}`, same...); rec.Code != 200 {
		t.Fatalf("same-origin PATCH = %d", rec.Code)
	}

	// Origin header from another host is rejected too.
	assertError(t, e.do("POST", "/api/v1/auth/logout", "", "", "Origin", "https://evil.example"), 403, "cross_origin")

	// Safe methods are allowed cross-site.
	if rec := e.do("GET", "/api/v1/version", "", "", cross...); rec.Code != 200 {
		t.Fatalf("cross-site GET = %d", rec.Code)
	}
}

func TestCrossSiteLocalLoginNeverReachesHandler(t *testing.T) {
	reached := false
	login := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusNoContent)
	})
	e := newEnv(t, func(d *Deps) { d.LocalLogin = login })
	body := `{"username":"root","password":"x"}`
	assertError(t, e.do("POST", "/api/v1/auth/local/login", "", body, "Sec-Fetch-Site", "cross-site"), 403, "cross_origin")
	if reached {
		t.Fatal("cross-site login reached the handler")
	}
	if rec := e.do("POST", "/api/v1/auth/local/login", "", body, "Sec-Fetch-Site", "same-origin"); rec.Code != 204 || !reached {
		t.Fatalf("same-origin login = %d, reached = %v", rec.Code, reached)
	}
}

func TestRequestLogOmitsQueryAndCookies(t *testing.T) {
	e := newEnv(t, nil)
	e.do("GET", "/api/v1/version?code=SECRET", "", "", "Cookie", "scanopsis_session=COOKIEVALUE", "Authorization", "Bearer TOKENVALUE")
	out := e.logs.String()
	for _, leak := range []string{"SECRET", "code=", "COOKIEVALUE", "TOKENVALUE", "scanopsis_session"} {
		if strings.Contains(out, leak) {
			t.Errorf("log contains %q: %s", leak, out)
		}
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(strings.SplitN(out, "\n", 2)[0]), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, out)
	}
	if rec["msg"] != "request" || rec["method"] != "GET" || rec["path"] != "/api/v1/version" ||
		rec["status"] != float64(200) || rec["client_ip"] != "192.0.2.7" {
		t.Fatalf("log record = %v", rec)
	}
	if _, ok := rec["duration_ms"]; !ok {
		t.Fatal("duration_ms missing")
	}
}

func TestRequestLogWithoutClientIPAndDefaultLogger(t *testing.T) {
	e := newEnv(t, func(d *Deps) { d.ClientIP = nil })
	e.do("GET", "/healthz", "", "")
	if !strings.Contains(e.logs.String(), `"client_ip":""`) {
		t.Fatalf("log = %s", e.logs.String())
	}
	// A nil logger must not panic.
	h := New(Deps{Sessions: &fakeSessions{}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestPanicRecovery(t *testing.T) {
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("kaboom") })
	e := newEnv(t, func(d *Deps) { d.GitLabLogin = boom })
	rec := e.do("GET", "/api/v1/auth/gitlab/login", "", "")
	assertError(t, rec, 500, "internal_error")
	out := e.logs.String()
	if !strings.Contains(out, "kaboom") || !strings.Contains(out, "stack") || !strings.Contains(out, "panic while handling request") {
		t.Fatalf("panic not logged: %s", out)
	}
	if strings.Contains(rec.Body.String(), "kaboom") {
		t.Fatal("panic value leaked to the client")
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("security headers missing on the 500 response")
	}
}

func TestAbortHandlerPanicIsRethrown(t *testing.T) {
	abort := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	e := newEnv(t, func(d *Deps) { d.GitLabLogin = abort })
	defer func() {
		if v, _ := recover().(error); !errors.Is(v, http.ErrAbortHandler) {
			t.Fatal("http.ErrAbortHandler must propagate")
		}
	}()
	e.do("GET", "/api/v1/auth/gitlab/login", "", "")
}

func TestStatusRecorderUnwrap(t *testing.T) {
	inner := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: inner}
	if rec.Unwrap() != http.ResponseWriter(inner) {
		t.Fatal("Unwrap must return the wrapped writer")
	}
}
