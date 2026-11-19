// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/store"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func runAdminArgs(env map[string]string, args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = runAdmin(context.Background(), args, &out, &errb, envOf(env))
	return code, out.String(), errb.String()
}

func TestAdminHelp(t *testing.T) {
	for _, a := range []string{"-h", "--help", "help", "-help"} {
		code, out, errs := runAdminArgs(nil, a)
		if code != 0 || errs != "" || !strings.Contains(out, "reset-password <username>") || !strings.Contains(out, "reset-2fa") {
			t.Errorf("%s: code %d stdout %q stderr %q", a, code, out, errs)
		}
	}
}

func TestAdminUsageErrors(t *testing.T) {
	cases := map[string][]string{
		"no arguments":          {},
		"unknown command":       {"frobnicate"},
		"reset-password none":   {"reset-password"},
		"reset-password two":    {"reset-password", "a", "b"},
		"reset-password empty":  {"reset-password", ""},
		"reset-password flag":   {"reset-password", "--force"},
		"reset-2fa none":        {"reset-2fa"},
		"reset-2fa two":         {"reset-2fa", "local:a", "local:b"},
		"reset-2fa bare name":   {"reset-2fa", "alice"},
		"reset-2fa no username": {"reset-2fa", "local:"},
		"reset-2fa bad source":  {"reset-2fa", "ldap:alice"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			// A database URL that would fail proves the arguments are checked first.
			code, out, errs := runAdminArgs(map[string]string{"SCANOPSIS_DATABASE_URL": "postgres://127.0.0.1:1/x"}, args...)
			if code != 2 || out != "" || errs == "" {
				t.Fatalf("code %d stdout %q stderr %q; want 2, empty, a message", code, out, errs)
			}
		})
	}
	if _, _, errs := runAdminArgs(nil); !strings.Contains(errs, "Usage: scanopsis admin") {
		t.Errorf("no-argument usage %q", errs)
	}
}

func TestAdminDatabaseURL(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "url")
	if err := os.WriteFile(file, []byte("postgres://from-file\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := databaseURL(envOf(map[string]string{"SCANOPSIS_DATABASE_URL": "postgres://env"})); err != nil || got != "postgres://env" {
		t.Fatalf("env: %q, %v", got, err)
	}
	if got, err := databaseURL(envOf(map[string]string{"SCANOPSIS_DATABASE_URL_FILE": file})); err != nil || got != "postgres://from-file" {
		t.Fatalf("file: %q, %v", got, err)
	}
	for name, env := range map[string]map[string]string{
		"unset":   {},
		"both":    {"SCANOPSIS_DATABASE_URL": "x", "SCANOPSIS_DATABASE_URL_FILE": file},
		"missing": {"SCANOPSIS_DATABASE_URL_FILE": filepath.Join(dir, "nope")},
		"empty":   {"SCANOPSIS_DATABASE_URL_FILE": writeFile(t, dir, "empty", "\n")},
	} {
		if _, err := databaseURL(envOf(env)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// Nothing but the database settings is needed or consulted.
	code, _, errs := runAdminArgs(map[string]string{}, "reset-password", "alice")
	if code != 1 || !strings.Contains(errs, "SCANOPSIS_DATABASE_URL") {
		t.Fatalf("code %d stderr %q", code, errs)
	}
	code, _, errs = runAdminArgs(map[string]string{"SCANOPSIS_DATABASE_URL": "postgres://scanopsis@127.0.0.1:1/x?connect_timeout=1"}, "reset-password", "alice")
	if code != 1 || errs == "" {
		t.Fatalf("unreachable database: code %d stderr %q", code, errs)
	}
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNewTempPassword(t *testing.T) {
	seen := map[string]bool{}
	counts := map[rune]int{}
	for range 500 {
		p, err := newTempPassword()
		if err != nil || len(p) != 20 {
			t.Fatalf("password %q, %v", p, err)
		}
		for _, c := range p {
			if !strings.ContainsRune(tempPasswordAlphabet, c) {
				t.Fatalf("%q contains %q outside the alphabet", p, c)
			}
			counts[c]++
		}
		if seen[p] {
			t.Fatalf("duplicate password %q", p)
		}
		seen[p] = true
		if err := auth.CheckPassword(p, "admin"); err != nil {
			t.Fatalf("%q fails the password policy: %v", p, err)
		}
	}
	if len(counts) != len(tempPasswordAlphabet) {
		t.Fatalf("%d distinct characters in 10000 draws; want all %d", len(counts), len(tempPasswordAlphabet))
	}
	for _, bad := range "0O1lI" {
		if strings.ContainsRune(tempPasswordAlphabet, bad) {
			t.Fatalf("alphabet contains the look-alike %q", bad)
		}
	}
}

func TestParseAccount(t *testing.T) {
	if s, u, ok := parseAccount("gitlab:ada:lovelace"); !ok || s != "gitlab" || u != "ada:lovelace" {
		t.Fatalf("%q %q %v", s, u, ok)
	}
	if s, u, ok := parseAccount("local:root"); !ok || s != "local" || u != "root" {
		t.Fatalf("%q %q %v", s, u, ok)
	}
}

func TestDescribeAdminError(t *testing.T) {
	err := &pgconn.PgError{Code: "42P01", Message: `relation "users" does not exist`}
	if msg := describeAdminError(errors.Join(errors.New("x"), err)); !strings.Contains(msg, "run the server once first") {
		t.Fatalf("message %q", msg)
	}
	if msg := describeAdminError(errors.New("boom")); msg != "boom" {
		t.Fatalf("message %q", msg)
	}
}

type recAudit struct{ events []string }

func (r *recAudit) Record(_ context.Context, actor, action string, detail map[string]any) {
	r.events = append(r.events, actor+" "+action+" "+detail["username"].(string))
}

type fakeBackend struct {
	users    []store.User
	lookups  int
	setHash  map[int64]string
	resets   []int64
	setErr   error
	resetErr error
	listErr  error
	rec      *recAudit
}

func (f *fakeBackend) backend() *adminBackend {
	f.rec = &recAudit{}
	f.setHash = map[int64]string{}
	return &adminBackend{
		localUser: func(_ context.Context, username string) (store.User, error) {
			for _, u := range f.users {
				if u.Source == "local" && u.Username == username {
					return u, nil
				}
			}
			return store.User{}, pgx.ErrNoRows
		},
		listUsers: func(_ context.Context, after int64, limit int32) ([]store.User, error) {
			f.lookups++
			if f.listErr != nil {
				return nil, f.listErr
			}
			var out []store.User
			for _, u := range f.users {
				if u.ID > after && len(out) < int(limit) {
					out = append(out, u)
				}
			}
			return out, nil
		},
		setPassword: func(_ context.Context, id int64, hash string) error {
			if f.setErr != nil {
				return f.setErr
			}
			f.setHash[id] = hash
			return nil
		},
		resetMFA: func(_ context.Context, id int64) error {
			if f.resetErr != nil {
				return f.resetErr
			}
			f.resets = append(f.resets, id)
			return nil
		},
		audit: f.rec,
	}
}

func TestResetPasswordCommand(t *testing.T) {
	f := &fakeBackend{users: []store.User{{ID: 3, Source: "local", Username: "root"}, {ID: 4, Source: "gitlab", Username: "ada"}}}
	b := f.backend()
	var out bytes.Buffer
	if err := b.resetPassword(context.Background(), "root", &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out.String(), "\n")
	temp := lines[2]
	if len(temp) != 20 || strings.Count(out.String(), temp) != 1 {
		t.Fatalf("output %q", out.String())
	}
	if !strings.Contains(out.String(), "must be changed at the next sign-in") {
		t.Fatalf("output lacks the explanation: %q", out.String())
	}
	if ok, err := auth.VerifyPassword(context.Background(), f.setHash[3], temp); err != nil || !ok {
		t.Fatalf("stored hash does not verify the printed password: %v, %v", ok, err)
	}
	if !reflect.DeepEqual(f.rec.events, []string{"cli user.password_reset root"}) {
		t.Fatalf("audit %v", f.rec.events)
	}

	// Unknown user, GitLab user (no password), storage failure.
	for _, name := range []string{"nobody", "ada"} {
		out.Reset()
		if err := b.resetPassword(context.Background(), name, &out); err == nil || !strings.Contains(err.Error(), "no local account") || out.Len() != 0 {
			t.Fatalf("%s: %v, output %q", name, err, out.String())
		}
	}
	f.setErr = errors.New("boom")
	out.Reset()
	if err := b.resetPassword(context.Background(), "root", &out); err == nil || out.Len() != 0 || len(f.rec.events) != 1 {
		t.Fatalf("%v, output %q, audit %v", err, out.String(), f.rec.events)
	}
}

func TestResetPasswordLookupFailure(t *testing.T) {
	b := (&fakeBackend{}).backend()
	b.localUser = func(context.Context, string) (store.User, error) { return store.User{}, errors.New("db down") }
	if err := b.resetPassword(context.Background(), "root", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("%v", err)
	}
}

func TestReset2FACommand(t *testing.T) {
	f := &fakeBackend{users: []store.User{
		{ID: 3, Source: "local", Username: "ada"},
		{ID: 4, Source: "gitlab", Username: "ada"},
	}}
	b := f.backend()
	var out bytes.Buffer
	if err := b.reset2FA(context.Background(), "gitlab", "ada", &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.resets, []int64{4}) || !strings.Contains(out.String(), "gitlab:ada") {
		t.Fatalf("resets %v output %q", f.resets, out.String())
	}
	out.Reset()
	if err := b.reset2FA(context.Background(), "local", "ada", &out); err != nil || !reflect.DeepEqual(f.resets, []int64{4, 3}) {
		t.Fatalf("%v resets %v", err, f.resets)
	}
	if !reflect.DeepEqual(f.rec.events, []string{"cli mfa.reset ada", "cli mfa.reset ada"}) {
		t.Fatalf("audit %v", f.rec.events)
	}
	for _, src := range []string{"local", "gitlab"} {
		if err := b.reset2FA(context.Background(), src, "ghost", &out); err == nil || !strings.Contains(err.Error(), "no "+src+" account") {
			t.Fatalf("%s ghost: %v", src, err)
		}
	}
	f.resetErr = store.ErrNotFound
	if err := b.reset2FA(context.Background(), "local", "ada", &out); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("%v", err)
	}
	f.resetErr = errors.New("boom")
	if err := b.reset2FA(context.Background(), "local", "ada", &out); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("%v", err)
	}
	if len(f.rec.events) != 2 {
		t.Fatalf("a failed reset was audited: %v", f.rec.events)
	}
	f.listErr = errors.New("list failed")
	if err := b.reset2FA(context.Background(), "gitlab", "ada", &out); err == nil || !strings.Contains(err.Error(), "list failed") {
		t.Fatalf("%v", err)
	}
	b.localUser = func(context.Context, string) (store.User, error) { return store.User{}, errors.New("db down") }
	if err := b.reset2FA(context.Background(), "local", "ada", &out); err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("%v", err)
	}
}

func TestReset2FAPagesThroughUsers(t *testing.T) {
	f := &fakeBackend{}
	for i := 1; i <= 1200; i++ {
		f.users = append(f.users, store.User{ID: int64(i), Source: "gitlab", Username: "user" + string(rune('a'+i%26)) + strings.Repeat("x", i%7)})
	}
	f.users = append(f.users, store.User{ID: 5000, Source: "gitlab", Username: "the-one"})
	b := f.backend()
	if err := b.reset2FA(context.Background(), "gitlab", "the-one", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.resets, []int64{5000}) || f.lookups != 3 {
		t.Fatalf("resets %v after %d pages; want [5000] after 3", f.resets, f.lookups)
	}
}
