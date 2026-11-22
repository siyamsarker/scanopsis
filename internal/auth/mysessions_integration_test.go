// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package auth

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/clientip"
	"github.com/siyamsarker/scanopsis/internal/store"
)

// TestIntegrationMySessions signs one user in three times and another once,
// then lists and ends sessions through the handlers, against PostgreSQL.
func TestIntegrationMySessions(t *testing.T) {
	ctx := context.Background()
	e := newIntegrationEnv(t)
	e.addAdmin(t, "root", "initial-pass")
	e.addAdmin(t, "other", "other-pass")
	resolver := clientip.New(nil)
	sessions := NewSessions(e.q, SessionOptions{
		Idle: testIdle, Absolute: testAbsolute, Secure: true, Now: e.clock.Now, ClientIP: resolver.IP,
	})
	rec := audit.New(e.q, slog.New(slog.NewTextHandler(io.Discard, nil)))
	login := NewLocalLogin(e.q, sessions, rec, resolver.IP, NewRateLimiter(time.Millisecond, 100, nil), NewRateLimiter(time.Millisecond, 100, nil), nil)
	my := NewMySessions(e.q, sessions, rec, resolver.IP, nil)

	signIn := func(user, pass, addr, agent string) *http.Cookie {
		t.Helper()
		r := newRequest(http.MethodPost, "/api/v1/auth/local/login", strings.NewReader(creds(user, pass)))
		r.RemoteAddr = addr
		r.Header.Set("User-Agent", agent)
		w := httptest.NewRecorder()
		login.ServeHTTP(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("sign-in %s: %d %s", user, w.Code, w.Body.String())
		}
		return w.Result().Cookies()[0]
	}
	call := func(h http.Handler, method, target string, c *http.Cookie, id string) *httptest.ResponseRecorder {
		t.Helper()
		r := newRequest(method, target, nil)
		r.RemoteAddr = "198.51.100.1:1"
		r.AddCookie(c)
		r.SetPathValue("id", id)
		p, err := sessions.Authenticate(r)
		if err != nil {
			t.Fatalf("authenticate: %v", err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
		return w
	}
	list := func(c *http.Cookie) []sessionJSON {
		t.Helper()
		w := call(my.ListHandler(), http.MethodGet, "/api/v1/me/sessions", c, "")
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("list: %d %v", w.Code, w.Header())
		}
		var body struct{ Items []sessionJSON }
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Items
	}

	first := signIn("root", "initial-pass", "192.0.2.1:1000", "Browser One")
	second := signIn("root", "initial-pass", "[2001:db8::5]:1000", "Browser Two")
	third := signIn("root", "initial-pass", "192.0.2.3:1000", strings.Repeat("x", 300))
	otherCookie := signIn("other", "other-pass", "192.0.2.4:1000", "Other")

	items := list(second)
	if len(items) != 3 {
		t.Fatalf("listed %d sessions; want 3: %+v", len(items), items)
	}
	byIP := map[string]sessionJSON{}
	for _, it := range items {
		byIP[it.ClientIP] = it
	}
	if !byIP["2001:db8::5"].Current || byIP["192.0.2.1"].Current || byIP["192.0.2.3"].Current {
		t.Errorf("current flags wrong: %+v", items)
	}
	if byIP["192.0.2.1"].UserAgent != "Browser One" || len(byIP["192.0.2.3"].UserAgent) != maxUserAgent {
		t.Errorf("user agents wrong: %+v", items)
	}
	if items[0].ClientIP != "192.0.2.3" {
		t.Errorf("not most recent first: %+v", items)
	}
	for _, it := range items {
		if it.ID < 1 || it.CreatedAt.IsZero() || !it.ExpiresAt.After(it.CreatedAt) {
			t.Errorf("incomplete item %+v", it)
		}
	}

	// The current session cannot be ended here; another user's cannot at all.
	cur := strconv.FormatInt(byIP["2001:db8::5"].ID, 10)
	if w := call(my.EndHandler(), http.MethodDelete, "/", second, cur); w.Code != http.StatusConflict {
		t.Errorf("ending the current session: %d", w.Code)
	}
	otherID := list(otherCookie)[0].ID
	if w := call(my.EndHandler(), http.MethodDelete, "/", second, strconv.FormatInt(otherID, 10)); w.Code != http.StatusNotFound {
		t.Errorf("ending another user's session: %d", w.Code)
	}
	if w := call(my.EndHandler(), http.MethodDelete, "/", second, "abc"); w.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d", w.Code)
	}

	// End one, then all others.
	firstID := strconv.FormatInt(byIP["192.0.2.1"].ID, 10)
	if w := call(my.EndHandler(), http.MethodDelete, "/", second, firstID); w.Code != http.StatusNoContent {
		t.Fatalf("end one: %d %s", w.Code, w.Body.String())
	}
	if _, err := sessions.Authenticate(requestWith(first)); err == nil {
		t.Error("the ended session still works")
	}
	if w := call(my.EndHandler(), http.MethodDelete, "/", second, firstID); w.Code != http.StatusNotFound {
		t.Errorf("ending it twice: %d", w.Code)
	}
	w := call(my.EndOthersHandler(), http.MethodDelete, "/", second, "")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"ended":1}` {
		t.Fatalf("end others: %d %s", w.Code, w.Body.String())
	}
	if _, err := sessions.Authenticate(requestWith(third)); err == nil {
		t.Error("another session survived ending all others")
	}
	if got := list(second); len(got) != 1 || !got[0].Current {
		t.Errorf("after ending others: %+v", got)
	}
	if got := list(otherCookie); len(got) != 1 {
		t.Errorf("the other user lost sessions: %+v", got)
	}
	// Nothing left to end is not an event.
	if w := call(my.EndOthersHandler(), http.MethodDelete, "/", second, ""); strings.TrimSpace(w.Body.String()) != `{"ended":0}` {
		t.Errorf("end others again: %s", w.Body.String())
	}

	rows, err := e.pool.Query(ctx, "SELECT actor, detail FROM audit_log WHERE action = $1 ORDER BY id", audit.ActionSessionRevoked)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var actor string
		var detail map[string]any
		if err := rows.Scan(&actor, &detail); err != nil {
			t.Fatal(err)
		}
		got = append(got, actor+" "+detail["scope"].(string)+" "+detail["ip"].(string))
	}
	want := []string{"local:root one 198.51.100.1", "local:root others 198.51.100.1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("audit = %v; want %v", got, want)
	}

	// Idle sessions are not listed.
	e.clock.Advance(testIdle + time.Minute)
	if n, err := e.q.ListUserSessions(ctx, store.ListUserSessionsParams{
		UserID: 1, Now: ts(e.clock.Now()), IdleCutoff: ts(e.clock.Now().Add(-testIdle)), MaxRows: 10,
	}); err != nil || len(n) != 0 {
		t.Errorf("idle sessions listed: %v %v", n, err)
	}
}
