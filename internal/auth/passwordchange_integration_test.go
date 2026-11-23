// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/store"
)

func TestIntegrationPasswordChange(t *testing.T) {
	e := newIntegrationEnv(t)
	ctx := context.Background()
	e.addAdmin(t, "root", pwCurrent)
	u, err := e.q.GetLocalUserByUsername(ctx, "root")
	if err != nil {
		t.Fatal(err)
	}
	// The user must change the password, as after a reset from the command line.
	if err := e.q.SetPassword(ctx, store.SetPasswordParams{ID: u.ID, PasswordHash: u.PasswordHash, MustChange: true}); err != nil {
		t.Fatal(err)
	}

	first := e.signIn(t, "root", pwCurrent, nil).Result().Cookies()[0]
	// A second session of the same user, started without replacing the first.
	w := httptest.NewRecorder()
	if err := e.sessions.Start(w, newRequest(http.MethodPost, "/", nil), u.ID); err != nil {
		t.Fatal(err)
	}
	other := w.Result().Cookies()[0]
	r := newRequest(http.MethodGet, "/", nil)
	r.AddCookie(first)
	p, err := e.sessions.Authenticate(r)
	if err != nil {
		t.Fatal(err)
	}
	// The router fills in the session and the second factor; do the same here.
	tok, err := base64.RawURLEncoding.DecodeString(first.Value)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(tok)
	p.SessionID = sum[:]
	p.MFAVerified, p.MustChangePassword = true, true

	h := NewPasswordChange(e.q, audit.New(e.q, slog.New(slog.NewTextHandler(io.Discard, nil))), NewRateLimiter(12*time.Second, 10, nil), nil)
	do := func(body string) *httptest.ResponseRecorder {
		r := newRequest(http.MethodPost, "/api/v1/me/password", strings.NewReader(body))
		r = r.WithContext(WithPrincipal(r.Context(), p))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	if w := do(pwBody("wrong", pwNew)); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current: status %d", w.Code)
	}
	if w := do(pwBody(pwCurrent, "short")); w.Code != http.StatusBadRequest {
		t.Fatalf("weak: status %d", w.Code)
	}
	if n := e.count(t, "SELECT count(*) FROM sessions"); n != 2 {
		t.Fatalf("%d sessions after rejected changes; want 2", n)
	}
	if w := do(pwBody(pwCurrent, pwNew)); w.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	u, err = e.q.GetUser(ctx, u.ID)
	if err != nil || u.MustChangePassword || !u.PasswordHash.Valid {
		t.Fatalf("user %+v, %v", u, err)
	}
	if ok, err := VerifyPassword(ctx, u.PasswordHash.String, pwNew); err != nil || !ok {
		t.Fatalf("new password does not verify: %v, %v", ok, err)
	}
	if ok, _ := VerifyPassword(ctx, u.PasswordHash.String, pwCurrent); ok {
		t.Fatal("old password still verifies")
	}

	// The current session survives, the other one is gone.
	for _, c := range []struct {
		cookie *http.Cookie
		alive  bool
	}{{first, true}, {other, false}} {
		r := newRequest(http.MethodGet, "/", nil)
		r.AddCookie(c.cookie)
		_, err := e.sessions.Authenticate(r)
		if c.alive && err != nil {
			t.Fatalf("current session ended: %v", err)
		}
		if !c.alive && !errors.Is(err, ErrNoSession) {
			t.Fatalf("other session still valid: %v", err)
		}
	}
	if n := e.count(t, "SELECT count(*) FROM audit_log WHERE action = 'user.password_changed' AND actor = 'local:root'"); n != 1 {
		t.Fatalf("%d password_changed events; want 1", n)
	}
	if n := e.count(t, "SELECT count(*) FROM audit_log WHERE detail::text LIKE '%' || $1 || '%' OR detail::text LIKE '%' || $2 || '%'", pwCurrent, pwNew); n != 0 {
		t.Fatal("a password is in the audit log")
	}

	// The new password signs in; the old one does not.
	if w := e.signIn(t, "root", pwNew, nil); w.Code != http.StatusNoContent {
		t.Fatalf("sign-in with the new password: status %d", w.Code)
	}
	if w := e.signIn(t, "root", pwCurrent, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("sign-in with the old password: status %d", w.Code)
	}
}

func TestIntegrationPasswordChangeRejectsNonLocalUser(t *testing.T) {
	e := newIntegrationEnv(t)
	ctx := context.Background()
	gl, err := e.q.UpsertGitLabUser(ctx, store.UpsertGitLabUserParams{
		GitlabIssuer: pgtype.Text{String: "https://gitlab.example.com", Valid: true},
		GitlabUserID: pgtype.Int8{Int64: 5, Valid: true}, Username: "ada", Name: "Ada", Email: "ada@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	h := NewPasswordChange(e.q, audit.New(e.q, slog.New(slog.NewTextHandler(io.Discard, nil))), NewRateLimiter(time.Second, 10, nil), nil)
	r := newRequest(http.MethodPost, "/", strings.NewReader(pwBody(pwCurrent, pwNew)))
	r = r.WithContext(WithPrincipal(r.Context(), &Principal{UserID: gl.ID, Source: SourceGitLab, Username: "ada", MFAVerified: true}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}
