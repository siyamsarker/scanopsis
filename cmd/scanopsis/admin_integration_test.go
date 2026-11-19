// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package main

import (
	"bytes"
	"context"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

// adminEnv is a migrated database plus a fake environment pointing at it.
type adminEnv struct {
	pool *pgxpool.Pool
	q    *store.Queries
	env  map[string]string
}

func newAdminEnv(t *testing.T) *adminEnv {
	t.Helper()
	pool := storetest.New(t)
	cc := pool.Config().ConnConfig // ConnString() would name the maintenance database
	dbURL := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cc.User, cc.Password),
		Host:     net.JoinHostPort(cc.Host, strconv.Itoa(int(cc.Port))),
		Path:     "/" + cc.Database,
		RawQuery: "sslmode=disable",
	}
	return &adminEnv{
		pool: pool,
		q:    store.New(pool),
		env:  map[string]string{"SCANOPSIS_DATABASE_URL": dbURL.String()},
	}
}

func (e *adminEnv) run(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = runAdmin(context.Background(), args, &out, &errb, envOf(e.env))
	return code, out.String(), errb.String()
}

func (e *adminEnv) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *adminEnv) localUser(t *testing.T, name, password string) store.User {
	t.Helper()
	hash, err := auth.HashPassword(context.Background(), password)
	if err != nil {
		t.Fatal(err)
	}
	u, err := e.q.CreateLocalUser(context.Background(), store.CreateLocalUserParams{
		Username: name, Role: store.RoleAdmin, PasswordHash: pgtype.Text{String: hash, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (e *adminEnv) session(t *testing.T, userID int64, tag byte) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		"INSERT INTO sessions (id_hash, user_id, expires_at) VALUES ($1, $2, now() + interval '1 hour')",
		bytes.Repeat([]byte{tag}, 32), userID); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationAdminResetPassword(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	u := e.localUser(t, "root", "the old passphrase")
	other := e.localUser(t, "other", "another passphrase")
	e.session(t, u.ID, 1)
	e.session(t, u.ID, 2)
	e.session(t, other.ID, 3)
	for _, id := range []int64{u.ID, other.ID} {
		if _, err := e.pool.Exec(ctx, "UPDATE users SET mfa_failed_attempts = 10 WHERE id = $1", id); err != nil {
			t.Fatal(err)
		}
	}

	code, out, errs := e.run("reset-password", "root")
	if code != 0 {
		t.Fatalf("code %d stderr %q", code, errs)
	}
	if strings.Contains(errs, "passphrase") {
		t.Fatalf("stderr %q", errs)
	}
	lines := strings.Split(out, "\n")
	temp := lines[2]
	if len(temp) != 20 || strings.Count(out, temp) != 1 || !strings.Contains(out, "must be changed at the next sign-in") {
		t.Fatalf("stdout %q", out)
	}

	got, err := e.q.GetLocalUserByUsername(ctx, "root")
	if err != nil {
		t.Fatal(err)
	}
	if !got.MustChangePassword {
		t.Fatal("must_change_password not set")
	}
	if ok, err := auth.VerifyPassword(ctx, got.PasswordHash.String, temp); err != nil || !ok {
		t.Fatalf("temporary password does not verify: %v, %v", ok, err)
	}
	if ok, _ := auth.VerifyPassword(ctx, got.PasswordHash.String, "the old passphrase"); ok {
		t.Fatal("the old password still works")
	}
	if n := e.count(t, "SELECT count(*) FROM sessions WHERE user_id = $1", u.ID); n != 0 {
		t.Fatalf("%d sessions of the user left", n)
	}
	if n := e.count(t, "SELECT count(*) FROM sessions WHERE user_id = $1", other.ID); n != 1 {
		t.Fatalf("%d sessions of another user; want 1", n)
	}
	if got.MfaFailedAttempts != 0 {
		t.Fatalf("failed-code count %d after a password reset; want 0", got.MfaFailedAttempts)
	}
	o, _ := e.q.GetLocalUserByUsername(ctx, "other")
	if o.MustChangePassword || o.PasswordHash != other.PasswordHash || o.MfaFailedAttempts != 10 {
		t.Fatal("another account was changed")
	}
	if n := e.count(t, "SELECT count(*) FROM audit_log WHERE action = 'user.password_reset' AND actor = 'cli' AND detail->>'username' = 'root'"); n != 1 {
		t.Fatalf("%d audit rows; want 1", n)
	}
	if n := e.count(t, "SELECT count(*) FROM audit_log WHERE detail::text LIKE '%' || $1 || '%'", temp); n != 0 {
		t.Fatal("the temporary password is in the audit log")
	}

	// A second reset gives a different password.
	_, out2, _ := e.run("reset-password", "root")
	if strings.Split(out2, "\n")[2] == temp {
		t.Fatal("the same temporary password twice")
	}
}

func TestIntegrationAdminResetPasswordFailures(t *testing.T) {
	e := newAdminEnv(t)
	if _, err := e.pool.Exec(context.Background(),
		"INSERT INTO users (source, gitlab_user_id, username, name, email, role) VALUES ('gitlab', 9, 'ada', 'Ada', 'a@example.com', 'viewer')"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"nobody", "ada"} { // unknown, and a GitLab account
		code, out, errs := e.run("reset-password", name)
		if code != 1 || out != "" || !strings.Contains(errs, "no local account") {
			t.Fatalf("%s: code %d stdout %q stderr %q", name, code, out, errs)
		}
	}
	if n := e.count(t, "SELECT count(*) FROM audit_log"); n != 0 {
		t.Fatalf("%d audit rows after failures", n)
	}
}

func TestIntegrationAdminReset2FA(t *testing.T) {
	e := newAdminEnv(t)
	ctx := context.Background()
	u := e.localUser(t, "root", "the old passphrase")
	if _, err := e.pool.Exec(ctx, "UPDATE users SET totp_secret_enc = '\\x0102', totp_enabled_at = now(), totp_last_step = 5, mfa_failed_attempts = 10 WHERE id = $1", u.ID); err != nil {
		t.Fatal(err)
	}
	e.session(t, u.ID, 1)
	var glID int64
	if err := e.pool.QueryRow(ctx,
		"INSERT INTO users (source, gitlab_user_id, username, name, email, role, totp_secret_enc, totp_enabled_at) VALUES ('gitlab', 9, 'ada', 'Ada', 'a@example.com', 'viewer', '\\x0304', now()) RETURNING id").Scan(&glID); err != nil {
		t.Fatal(err)
	}
	e.session(t, glID, 2)

	code, out, errs := e.run("reset-2fa", "local:root")
	if code != 0 || !strings.Contains(out, "local:root") {
		t.Fatalf("code %d stdout %q stderr %q", code, out, errs)
	}
	got, _ := e.q.GetUser(ctx, u.ID)
	if got.TotpSecretEnc != nil || got.TotpEnabledAt.Valid || got.TotpLastStep != 0 || got.MfaFailedAttempts != 0 {
		t.Fatalf("TOTP not reset: %+v", got)
	}
	if n := e.count(t, "SELECT count(*) FROM sessions WHERE user_id = $1", u.ID); n != 0 {
		t.Fatalf("%d sessions left", n)
	}
	// The GitLab account is untouched until it is reset too.
	if n := e.count(t, "SELECT count(*) FROM users WHERE id = $1 AND totp_enabled_at IS NOT NULL", glID); n != 1 {
		t.Fatal("another account was reset")
	}
	code, out, errs = e.run("reset-2fa", "gitlab:ada")
	if code != 0 || !strings.Contains(out, "gitlab:ada") {
		t.Fatalf("code %d stdout %q stderr %q", code, out, errs)
	}
	if n := e.count(t, "SELECT count(*) FROM users WHERE id = $1 AND totp_enabled_at IS NULL AND totp_secret_enc IS NULL", glID); n != 1 {
		t.Fatal("GitLab account not reset")
	}
	if n := e.count(t, "SELECT count(*) FROM sessions"); n != 0 {
		t.Fatalf("%d sessions left", n)
	}
	if n := e.count(t, "SELECT count(*) FROM audit_log WHERE action = 'mfa.reset' AND actor = 'cli'"); n != 2 {
		t.Fatalf("%d audit rows; want 2", n)
	}

	for _, target := range []string{"local:nobody", "gitlab:nobody", "gitlab:root", "local:ada"} {
		code, out, errs := e.run("reset-2fa", target)
		if code != 1 || out != "" || !strings.Contains(errs, "no ") {
			t.Fatalf("%s: code %d stdout %q stderr %q", target, code, out, errs)
		}
	}
	if n := e.count(t, "SELECT count(*) FROM audit_log WHERE action = 'mfa.reset'"); n != 2 {
		t.Fatalf("%d audit rows after failures; want 2", n)
	}
}

func TestIntegrationAdminWithoutSchema(t *testing.T) {
	e := newAdminEnv(t)
	if _, err := e.pool.Exec(context.Background(), "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	code, out, errs := e.run("reset-password", "root")
	if code != 1 || out != "" || !strings.Contains(errs, "run the server once first") {
		t.Fatalf("code %d stdout %q stderr %q", code, out, errs)
	}
}
