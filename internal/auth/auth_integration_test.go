// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/clientip"
	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

// movingClock is the wall clock plus an adjustable offset, so that tests can
// age sessions whose created and last-seen times PostgreSQL sets itself.
type movingClock struct {
	mu     sync.Mutex
	offset time.Duration
}

func (c *movingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.offset)
}

func (c *movingClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offset += d
}

type integrationEnv struct {
	pool     *pgxpool.Pool
	q        *store.Queries
	sessions *Sessions
	clock    *movingClock
	login    http.Handler
}

func newIntegrationEnv(t *testing.T) *integrationEnv {
	t.Helper()
	pool := storetest.New(t)
	q := store.New(pool)
	clk := &movingClock{}
	sessions := NewSessions(q, SessionOptions{Idle: testIdle, Absolute: testAbsolute, Secure: true, Now: clk.Now})
	rec := audit.New(q, slog.New(slog.NewTextHandler(io.Discard, nil)))
	resolver := clientip.New(nil)
	login := NewLocalLogin(q, sessions, rec, resolver.IP, NewRateLimiter(12*time.Second, 10, nil))
	return &integrationEnv{pool: pool, q: q, sessions: sessions, clock: clk, login: login}
}

func (e *integrationEnv) bootstrap(t *testing.T, username, password string) bool {
	t.Helper()
	rec := audit.New(e.q, slog.New(slog.NewTextHandler(io.Discard, nil)))
	created, err := BootstrapAdmin(context.Background(), e.q, rec, username, password)
	if err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	return created
}

func (e *integrationEnv) signIn(t *testing.T, user, pass string, existing *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	r := newRequest(http.MethodPost, "/api/v1/auth/local/login", strings.NewReader(creds(user, pass)))
	r.RemoteAddr = "203.0.113.9:4444"
	if existing != nil {
		r.AddCookie(existing)
	}
	w := httptest.NewRecorder()
	e.login.ServeHTTP(w, r)
	return w
}

func (e *integrationEnv) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIntegrationLoginCookieAuthenticateEndCycle(t *testing.T) {
	e := newIntegrationEnv(t)
	if !e.bootstrap(t, "root", "initial-pass") {
		t.Fatal("bootstrap did not create the administrator")
	}

	// Wrong password and unknown user: 401, no session.
	for _, u := range []string{"root", "ghost"} {
		if w := e.signIn(t, u, "wrong", nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status %d; want 401", u, w.Code)
		}
	}
	if n := e.count(t, "SELECT count(*) FROM sessions"); n != 0 {
		t.Fatalf("%d sessions after failed logins", n)
	}

	w := e.signIn(t, "root", "initial-pass", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("login status %d: %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-scanopsis_session" || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatalf("cookies %+v", cookies)
	}
	cookie := cookies[0]

	// Only the hash of the token is stored.
	tok, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(tok) != 32 {
		t.Fatalf("token %q", cookie.Value)
	}
	sum := sha256.Sum256(tok)
	if n := e.count(t, "SELECT count(*) FROM sessions WHERE id_hash = $1", sum[:]); n != 1 {
		t.Fatalf("%d sessions keyed by sha256(token); want 1", n)
	}
	if n := e.count(t, "SELECT count(*) FROM sessions WHERE id_hash = $1", tok); n != 0 {
		// A 32-byte raw token would also satisfy the length check.
		t.Fatal("the raw token is stored")
	}

	r := newRequest(http.MethodGet, "/api/v1/me", nil)
	r.AddCookie(cookie)
	p, err := e.sessions.Authenticate(r)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if p.Username != "root" || p.Role != RoleAdmin || p.Source != SourceLocal || p.UserID == 0 || !p.IsAdmin() {
		t.Fatalf("principal %+v", p)
	}
	if n := e.count(t, "SELECT count(*) FROM users WHERE username = 'root' AND last_login_at IS NOT NULL"); n != 1 {
		t.Fatal("last_login_at was not set")
	}

	// Sign out.
	out := httptest.NewRecorder()
	r = newRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	r.AddCookie(cookie)
	if err := e.sessions.End(out, r); err != nil {
		t.Fatal(err)
	}
	if c := out.Result().Cookies(); len(c) != 1 || c[0].MaxAge != -1 {
		t.Fatalf("clearing cookie %+v", c)
	}
	r = newRequest(http.MethodGet, "/api/v1/me", nil)
	r.AddCookie(cookie)
	if _, err := e.sessions.Authenticate(r); !errors.Is(err, ErrNoSession) {
		t.Fatalf("session usable after End: %v", err)
	}
	if n := e.count(t, "SELECT count(*) FROM sessions"); n != 0 {
		t.Fatalf("%d sessions left after End", n)
	}

	// Audit trail: bootstrap, two failures, one success; never the password.
	rows, err := e.pool.Query(context.Background(), "SELECT actor, action, detail FROM audit_log ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	var lastDetail map[string]any
	for rows.Next() {
		var actor, action string
		var detail []byte
		if err := rows.Scan(&actor, &action, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(detail), "initial-pass") {
			t.Fatalf("password in audit detail %s", detail)
		}
		actions = append(actions, action)
		if action == "login.succeeded" {
			if err := json.Unmarshal(detail, &lastDetail); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"user.created", "login.failed", "login.failed", "login.succeeded"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("audit actions %v; want %v", actions, want)
	}
	if lastDetail["method"] != "local" || lastDetail["username"] != "root" || lastDetail["ip"] != "203.0.113.9" {
		t.Fatalf("login.succeeded detail %v", lastDetail)
	}
}

func TestIntegrationLoginReplacesExistingSession(t *testing.T) {
	e := newIntegrationEnv(t)
	e.bootstrap(t, "root", "initial-pass")
	first := e.signIn(t, "root", "initial-pass", nil).Result().Cookies()[0]
	second := e.signIn(t, "root", "initial-pass", first).Result().Cookies()[0]
	if first.Value == second.Value {
		t.Fatal("session token reused across logins")
	}
	if n := e.count(t, "SELECT count(*) FROM sessions"); n != 1 {
		t.Fatalf("%d sessions; want 1 (the old one replaced)", n)
	}
	r := newRequest(http.MethodGet, "/", nil)
	r.AddCookie(first)
	if _, err := e.sessions.Authenticate(r); !errors.Is(err, ErrNoSession) {
		t.Fatalf("pre-login session still valid: %v", err)
	}
}

func TestIntegrationSessionLifetimes(t *testing.T) {
	e := newIntegrationEnv(t)
	e.bootstrap(t, "root", "initial-pass")
	auth := func(c *http.Cookie) error {
		r := newRequest(http.MethodGet, "/", nil)
		r.AddCookie(c)
		_, err := e.sessions.Authenticate(r)
		return err
	}

	// Idle timeout, and activity extending the session.
	c := e.signIn(t, "root", "initial-pass", nil).Result().Cookies()[0]
	e.clock.Advance(testIdle - time.Minute)
	if err := auth(c); err != nil {
		t.Fatalf("just before the idle limit: %v", err)
	}
	var lastSeen, created time.Time
	if err := e.pool.QueryRow(context.Background(), "SELECT last_seen_at, created_at FROM sessions").Scan(&lastSeen, &created); err != nil {
		t.Fatal(err)
	}
	if lastSeen.Sub(created) < testIdle-2*time.Minute {
		t.Fatalf("last_seen_at was not advanced by the request: %v after creation", lastSeen.Sub(created))
	}
	e.clock.Advance(testIdle - time.Minute)
	if err := auth(c); err != nil {
		t.Fatalf("active session was cut off: %v", err)
	}
	e.clock.Advance(testIdle + time.Minute)
	if err := auth(c); !errors.Is(err, ErrNoSession) {
		t.Fatalf("idle session accepted: %v", err)
	}

	// Housekeeping removes what is no longer usable.
	n, err := e.sessions.RemoveExpired(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("RemoveExpired = %d, %v; want 1", n, err)
	}
	if left := e.count(t, "SELECT count(*) FROM sessions"); left != 0 {
		t.Fatalf("%d sessions left", left)
	}
}

// PostgreSQL sets created_at and last_seen_at itself, so each scenario that
// moves the clock needs its own environment.
func TestIntegrationAbsoluteExpiry(t *testing.T) {
	e := newIntegrationEnv(t)
	e.bootstrap(t, "root", "initial-pass")
	auth := func(c *http.Cookie) error {
		r := newRequest(http.MethodGet, "/", nil)
		r.AddCookie(c)
		_, err := e.sessions.Authenticate(r)
		return err
	}
	// Absolute timeout, even for an active session: it must survive until
	// close to the limit and be gone just after it.
	c := e.signIn(t, "root", "initial-pass", nil).Result().Cookies()[0]
	loggedIn := e.clock.Now()
	for e.clock.Now().Sub(loggedIn) < testAbsolute-30*time.Minute {
		e.clock.Advance(20 * time.Minute)
		if err := auth(c); err != nil {
			t.Fatalf("active session rejected after %v: %v", e.clock.Now().Sub(loggedIn), err)
		}
	}
	e.clock.Advance(31 * time.Minute)
	if err := auth(c); !errors.Is(err, ErrNoSession) {
		t.Fatalf("session outlived the absolute limit: %v", err)
	}
}

func TestIntegrationBootstrapCreatesOneAdminAndIsIdempotent(t *testing.T) {
	e := newIntegrationEnv(t)
	if e.bootstrap(t, "root", "") {
		t.Fatal("created an admin without a password")
	}
	if n := e.count(t, "SELECT count(*) FROM users"); n != 0 {
		t.Fatalf("%d users without a password", n)
	}
	if !e.bootstrap(t, "root", "initial-pass") {
		t.Fatal("first call did not create the administrator")
	}
	if e.bootstrap(t, "root", "initial-pass") || e.bootstrap(t, "other", "another-pass") {
		t.Fatal("a later call created another administrator")
	}
	if n := e.count(t, "SELECT count(*) FROM users"); n != 1 {
		t.Fatalf("%d users; want 1", n)
	}
	if n := e.count(t, "SELECT count(*) FROM users WHERE role = 'admin' AND source = 'local' AND username = 'root'"); n != 1 {
		t.Fatalf("admin row missing")
	}
	if n := e.count(t, "SELECT count(*) FROM audit_log WHERE action = 'user.created' AND detail->>'reason' = 'bootstrap'"); n != 1 {
		t.Fatalf("%d bootstrap audit events; want 1", n)
	}
	u, err := e.q.GetLocalUserByUsername(context.Background(), "root")
	if err != nil || !u.PasswordHash.Valid {
		t.Fatalf("GetLocalUserByUsername: %+v, %v", u, err)
	}
	if ok, err := VerifyPassword(u.PasswordHash.String, "initial-pass"); err != nil || !ok {
		t.Fatalf("stored hash does not verify: %v, %v", ok, err)
	}
}

func TestIntegrationLoginRateLimitUsesClientIP(t *testing.T) {
	e := newIntegrationEnv(t)
	ip := func(*http.Request) netip.Addr { return netip.MustParseAddr("198.51.100.1") }
	h := NewLocalLogin(e.q, e.sessions, audit.New(e.q, slog.New(slog.NewTextHandler(io.Discard, nil))), ip, NewRateLimiter(time.Hour, 1, nil))
	for i, want := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, newRequest(http.MethodPost, "/", strings.NewReader(creds("ghost", "x"))))
		if w.Code != want {
			t.Fatalf("attempt %d: status %d; want %d", i+1, w.Code, want)
		}
	}
}
