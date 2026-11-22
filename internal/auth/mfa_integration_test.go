// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pquerna/otp/totp"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/clientip"
	"github.com/siyamsarker/scanopsis/internal/secretbox"
	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

func TestIntegrationMFAEnrolVerifyReplayRecovery(t *testing.T) {
	ctx := context.Background()
	pool := storetest.New(t)
	q := store.New(pool)
	clk := &movingClock{}
	sessions := NewSessions(q, SessionOptions{Idle: testIdle, Absolute: testAbsolute, Secure: true, Now: clk.Now})
	box, err := secretbox.New(bytes.Repeat([]byte{3}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	rec := audit.New(q, slog.New(slog.NewTextHandler(io.Discard, nil)))
	recoveryKey, err := RecoveryKey(bytes.Repeat([]byte{3}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	m := NewMFA(q, sessions, rec, clientip.New(nil).IP, MFAOptions{
		Issuer: "Scanopsis", Box: box, Limiter: NewRateLimiter(30*time.Second, 50, nil), Now: clk.Now,
		RecoveryKey: recoveryKey,
	})

	user, err := q.CreateLocalUser(ctx, store.CreateLocalUserParams{Username: "root", Role: store.RoleAdmin, PasswordHash: pgtype.Text{String: "x", Valid: true}})
	if err != nil {
		t.Fatal(err)
	}

	// signIn starts a fresh session (first factor only) and returns its cookie.
	signIn := func() *http.Cookie {
		w := httptest.NewRecorder()
		if err := sessions.Start(w, newRequest(http.MethodPost, "/", nil), user.ID); err != nil {
			t.Fatal(err)
		}
		return w.Result().Cookies()[0]
	}
	call := func(h http.Handler, c *http.Cookie, body string) *httptest.ResponseRecorder {
		r := newRequest(http.MethodPost, "/", strings.NewReader(body))
		r.RemoteAddr = "203.0.113.9:1234"
		r.AddCookie(c)
		p, err := sessions.Authenticate(r)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
		// A promoted session comes with a new cookie: follow it, as a browser does.
		for _, nc := range w.Result().Cookies() {
			if nc.Name == c.Name {
				*c = *nc
			}
		}
		return w
	}
	principal := func(c *http.Cookie) *Principal {
		p, err := sessions.Authenticate(requestWith(c))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	code := func(secret string, at time.Time) string {
		c, err := totp.GenerateCode(secret, at)
		if err != nil {
			t.Fatal(err)
		}
		return codeBody(c)
	}

	// Enrol.
	c1 := signIn()
	if p := principal(c1); p.MFAEnrolled || p.MFAVerified || p.Complete() {
		t.Fatalf("new session %+v", *p)
	}
	w := call(m.VerifyHandler(), c1, codeBody("123456"))
	if w.Code != http.StatusConflict || errCode(t, w) != "mfa_enrollment_required" {
		t.Fatalf("verify before enrolment: %d %s", w.Code, w.Body)
	}
	w = call(m.StartEnrollmentHandler(), c1, "")
	var enr struct{ Secret string }
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &enr) != nil {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	stored, err := q.GetUser(ctx, user.ID)
	if err != nil || bytes.Contains(stored.TotpSecretEnc, []byte(enr.Secret)) || stored.TotpEnabledAt.Valid {
		t.Fatalf("stored user %+v, %v", stored, err)
	}
	w = call(m.ConfirmEnrollmentHandler(), c1, code(enr.Secret, clk.Now()))
	var rc struct {
		Codes []string `json:"recovery_codes"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &rc) != nil || len(rc.Codes) != 10 {
		t.Fatalf("confirm: %d %s", w.Code, w.Body)
	}
	if p := principal(c1); !p.MFAEnrolled || !p.MFAVerified || !p.Complete() {
		t.Fatalf("after confirm %+v", *p)
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM recovery_codes WHERE user_id = $1 AND used_at IS NULL", user.ID).Scan(&n); err != nil || n != 10 {
		t.Fatalf("%d recovery codes stored, %v", n, err)
	}
	plain := strings.ReplaceAll(rc.Codes[0], "-", "")
	unsalted := sha256.Sum256([]byte(plain))
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM recovery_codes WHERE code_hash = $1 OR code_hash = $2", []byte(plain), unsalted[:]).Scan(&n); err != nil || n != 0 {
		t.Fatalf("recovery code stored in the clear or as a plain SHA-256 (%d), %v", n, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM recovery_codes WHERE user_id = $1 AND version = 2 AND code_hash = $2", user.ID, recoveryMAC(recoveryKey, user.ID, plain)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("recovery code not stored as a version 2 HMAC (%d), %v", n, err)
	}
	w = call(m.StartEnrollmentHandler(), c1, "")
	if w.Code != http.StatusConflict || errCode(t, w) != "mfa_already_enrolled" {
		t.Fatalf("start again: %d %s", w.Code, w.Body)
	}

	// A new sign-in: the enrolment code is a replay, the next step's code works once.
	c2 := signIn()
	if p := principal(c2); !p.MFAEnrolled || p.MFAVerified || p.Complete() {
		t.Fatalf("second session %+v", *p)
	}
	if w := call(m.VerifyHandler(), c2, code(enr.Secret, clk.Now())); w.Code != http.StatusUnauthorized || errCode(t, w) != "invalid_code" {
		t.Fatalf("enrolment code replayed: %d %s", w.Code, w.Body)
	}
	clk.Advance(30 * time.Second)
	next := code(enr.Secret, clk.Now())
	beforeVerify := *c2
	if w := call(m.VerifyHandler(), c2, next); w.Code != http.StatusNoContent {
		t.Fatalf("verify: %d %s", w.Code, w.Body)
	}
	// The token the session had before the second factor no longer works.
	if _, err := sessions.Authenticate(requestWith(&beforeVerify)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("pre-verification cookie after verify: %v; want ErrNoSession", err)
	}
	if c2.Value == beforeVerify.Value {
		t.Fatal("verify kept the session token")
	}
	if p := principal(c2); !p.MFAVerified || !p.Complete() {
		t.Fatalf("after verify %+v", *p)
	}
	c3 := signIn()
	if w := call(m.VerifyHandler(), c3, next); w.Code != http.StatusUnauthorized {
		t.Fatalf("replay: %d %s", w.Code, w.Body)
	}
	if principal(c3).MFAVerified {
		t.Fatal("replayed code verified the session")
	}

	// Recovery code: once, case and hyphen insensitive.
	typed := codeBody(strings.ToUpper(strings.ReplaceAll(rc.Codes[3], "-", "")))
	if w := call(m.VerifyHandler(), c3, typed); w.Code != http.StatusNoContent || !principal(c3).MFAVerified {
		t.Fatalf("recovery: %d %s", w.Code, w.Body)
	}
	c4 := signIn()
	if w := call(m.VerifyHandler(), c4, codeBody(rc.Codes[3])); w.Code != http.StatusUnauthorized {
		t.Fatalf("recovery code reused: %d", w.Code)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM recovery_codes WHERE user_id = $1 AND used_at IS NULL", user.ID).Scan(&n); err != nil || n != 9 {
		t.Fatalf("%d unused recovery codes; want 9, %v", n, err)
	}

	// A code issued before version 2 (ten symbols, plain SHA-256, version 1
	// after the migration) still works, once.
	legacy := sha256.Sum256([]byte("abcdefghjk"))
	if _, err := pool.Exec(ctx, "INSERT INTO recovery_codes (user_id, code_hash, version) VALUES ($1, $2, 1)", user.ID, legacy[:]); err != nil {
		t.Fatal(err)
	}
	if w := call(m.VerifyHandler(), c4, codeBody("ABCDE-FGHJK")); w.Code != http.StatusNoContent {
		t.Fatalf("legacy recovery code: %d %s", w.Code, w.Body)
	}
	cReuse := signIn()
	if w := call(m.VerifyHandler(), cReuse, codeBody("abcde-fghjk")); w.Code != http.StatusUnauthorized {
		t.Fatalf("legacy recovery code reused: %d", w.Code)
	}
	// The version is part of the check: a legacy hash is never read as version 2.
	if _, err := pool.Exec(ctx, "INSERT INTO recovery_codes (user_id, code_hash, version) VALUES ($1, $2, 3)", user.ID, legacy[:]); err == nil {
		t.Fatal("version 3 accepted by the database")
	}

	// Audit trail: no code or secret in it.
	rows, err := pool.Query(ctx, "SELECT action, detail::text FROM audit_log ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var a, d string
		if err := rows.Scan(&a, &d); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, a)
		for _, s := range append([]string{enr.Secret, next}, rc.Codes...) {
			if strings.Contains(d, s) {
				t.Fatalf("audit detail %s contains a secret", d)
			}
		}
	}
	want := "mfa.enrolled mfa.failed mfa.verified mfa.failed mfa.verified mfa.failed mfa.verified mfa.failed"
	if got := strings.Join(actions, " "); got != want {
		t.Fatalf("audit actions %q; want %q", got, want)
	}

	// After an administrator's reset the user can enrol again.
	if err := store.ResetMFA(ctx, pool, user.ID); err != nil {
		t.Fatal(err)
	}
	c5 := signIn()
	if w := call(m.StartEnrollmentHandler(), c5, ""); w.Code != http.StatusOK {
		t.Fatalf("enrol after reset: %d %s", w.Code, w.Body)
	}
}

func TestIntegrationMFALockout(t *testing.T) {
	ctx := context.Background()
	pool := storetest.New(t)
	q := store.New(pool)
	clk := &movingClock{}
	sessions := NewSessions(q, SessionOptions{Idle: testIdle, Absolute: testAbsolute, Secure: true, Now: clk.Now})
	box, err := secretbox.New(bytes.Repeat([]byte{3}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	rec := audit.New(q, slog.New(slog.NewTextHandler(io.Discard, nil)))
	recoveryKey, err := RecoveryKey(bytes.Repeat([]byte{3}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	m := NewMFA(q, sessions, rec, clientip.New(nil).IP, MFAOptions{
		Issuer: "Scanopsis", Box: box, Limiter: NewRateLimiter(time.Second, 1000, nil), Now: clk.Now,
		RecoveryKey: recoveryKey,
	})
	user, err := q.CreateLocalUser(ctx, store.CreateLocalUserParams{Username: "root", Role: store.RoleAdmin, PasswordHash: pgtype.Text{String: "x", Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal([]byte("JBSWY3DPEHPK3PXP"), totpPurpose(user.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := q.SetPendingTOTP(ctx, store.SetPendingTOTPParams{SecretEnc: sealed, ID: user.ID}); err != nil {
		t.Fatal(err)
	}
	if err := q.EnableTOTP(ctx, store.EnableTOTPParams{ID: user.ID}); err != nil {
		t.Fatal(err)
	}
	verify := func(body string) (int, string) {
		w := httptest.NewRecorder()
		if err := sessions.Start(w, newRequest(http.MethodPost, "/", nil), user.ID); err != nil {
			t.Fatal(err)
		}
		r := newRequest(http.MethodPost, "/", strings.NewReader(body))
		r.RemoteAddr = "203.0.113.9:1234"
		r.AddCookie(w.Result().Cookies()[0])
		p, err := sessions.Authenticate(r)
		if err != nil {
			t.Fatal(err)
		}
		w = httptest.NewRecorder()
		m.VerifyHandler().ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
		return w.Code, errCode(t, w)
	}
	counter := func() int {
		var n int
		if err := pool.QueryRow(ctx, "SELECT mfa_failed_attempts FROM users WHERE id = $1", user.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	for i := 1; i < mfaMaxFailures; i++ {
		if code, _ := verify(codeBody("000000")); code != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d", i, code)
		}
	}
	if counter() != mfaMaxFailures-1 {
		t.Fatalf("counter %d", counter())
	}
	if code, ec := verify(codeBody("000000")); code != http.StatusLocked || ec != "mfa_locked" {
		t.Fatalf("tenth failure: %d %s", code, ec)
	}
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id = $1", user.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%d sessions left, %v", n, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_log WHERE action = 'mfa.locked'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d mfa.locked rows, %v", n, err)
	}
	if code, ec := verify(codeBody("000000")); code != http.StatusLocked || ec != "mfa_locked" {
		t.Fatalf("while locked: %d %s", code, ec)
	}

	// Resetting two-factor authentication clears the lock.
	if err := store.ResetMFA(ctx, pool, user.ID); err != nil {
		t.Fatal(err)
	}
	if counter() != 0 {
		t.Fatalf("counter %d after ResetMFA", counter())
	}
}
