// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package auth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/clientip"
)

func (e *integrationEnv) newSetup() *Setup {
	rec := audit.New(e.q, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return NewSetup(e.q, e.sessions, rec, clientip.New(nil).IP, NewRateLimiter(12*time.Second, 50, nil), nil)
}

func postSetup(h http.Handler, body string) *httptest.ResponseRecorder {
	r := newRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(body))
	r.RemoteAddr = "203.0.113.9:4444"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestIntegrationSetupCreatesOneAdminAndSession(t *testing.T) {
	e := newIntegrationEnv(t)
	s := e.newSetup()
	ctx := context.Background()
	token, err := s.PrepareToken(ctx)
	if err != nil || token == "" {
		t.Fatalf("PrepareToken: %q, %v", token, err)
	}
	h := s.Handler()

	if w := postSetup(h, setupBody("wrong", "root", goodSetupPass)); w.Code != http.StatusForbidden {
		t.Fatalf("wrong token: status %d", w.Code)
	}
	if n := e.count(t, "SELECT count(*) FROM users"); n != 0 {
		t.Fatalf("%d users after a wrong token", n)
	}

	w := postSetup(h, setupBody(token, "root", goodSetupPass))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies %+v", cookies)
	}
	if n := e.count(t, "SELECT count(*) FROM users WHERE role = 'admin' AND source = 'local' AND username = 'root' AND NOT must_change_password"); n != 1 {
		t.Fatal("administrator row missing")
	}
	u, err := e.q.GetLocalUserByUsername(ctx, "root")
	if err != nil || !u.PasswordHash.Valid {
		t.Fatalf("GetLocalUserByUsername: %+v, %v", u, err)
	}
	if ok, err := VerifyPassword(ctx, u.PasswordHash.String, goodSetupPass); err != nil || !ok {
		t.Fatalf("stored hash does not verify: %v, %v", ok, err)
	}

	// The session is signed in but the second factor is not verified.
	r := newRequest(http.MethodGet, "/api/v1/me", nil)
	r.AddCookie(cookies[0])
	p, err := e.sessions.Authenticate(r)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if p.Username != "root" || p.Role != RoleAdmin || p.MFAVerified || p.Complete() {
		t.Fatalf("principal %+v", p)
	}

	if n := e.count(t, "SELECT count(*) FROM audit_log WHERE action = 'setup.completed' AND actor = 'local:root' AND detail->>'ip' = '203.0.113.9'"); n != 1 {
		t.Fatalf("%d setup.completed events; want 1", n)
	}
	if n := e.count(t, "SELECT count(*) FROM audit_log WHERE detail::text LIKE '%' || $1 || '%'", goodSetupPass); n != 0 {
		t.Fatal("the password is in the audit log")
	}

	// Setup is over: the token is dead and a second POST is a conflict.
	if w := postSetup(h, setupBody(token, "second", goodSetupPass)); w.Code != http.StatusConflict {
		t.Fatalf("second setup: status %d", w.Code)
	}
	if tok, err := e.newSetup().PrepareToken(ctx); tok != "" || err != nil {
		t.Fatalf("PrepareToken after setup: %q, %v", tok, err)
	}
	get := httptest.NewRecorder()
	h.ServeHTTP(get, newRequest(http.MethodGet, "/api/v1/setup", nil))
	if strings.TrimSpace(get.Body.String()) != `{"required":false}` {
		t.Fatalf("status body %q", get.Body.String())
	}
	if n := e.count(t, "SELECT count(*) FROM users"); n != 1 {
		t.Fatalf("%d users; want 1", n)
	}
}

func TestIntegrationSetupConcurrentRequestsCreateOneAdmin(t *testing.T) {
	e := newIntegrationEnv(t)
	s := e.newSetup()
	token, err := s.PrepareToken(context.Background())
	if err != nil || token == "" {
		t.Fatalf("PrepareToken: %q, %v", token, err)
	}
	h := s.Handler()
	const n = 5
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := "root" + string(rune('a'+i))
			codes <- postSetup(h, setupBody(token, name, goodSetupPass)).Code
		}()
	}
	wg.Wait()
	close(codes)
	got := map[int]int{}
	for c := range codes {
		got[c]++
	}
	if got[http.StatusNoContent] != 1 || got[http.StatusConflict] != n-1 {
		t.Fatalf("status counts %v", got)
	}
	if c := e.count(t, "SELECT count(*) FROM users WHERE role = 'admin'"); c != 1 {
		t.Fatalf("%d administrators", c)
	}
}

func TestIntegrationSetupThenSignIn(t *testing.T) {
	e := newIntegrationEnv(t)
	s := e.newSetup()
	token, _ := s.PrepareToken(context.Background())
	if w := postSetup(s.Handler(), setupBody(token, "root", goodSetupPass)); w.Code != http.StatusNoContent {
		t.Fatalf("status %d", w.Code)
	}
	if w := e.signIn(t, "root", goodSetupPass, nil); w.Code != http.StatusNoContent {
		t.Fatalf("sign-in with the setup password: status %d", w.Code)
	}
}
