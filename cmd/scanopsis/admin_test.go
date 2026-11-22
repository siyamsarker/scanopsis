// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

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
		if code != 0 || errs != "" || !strings.Contains(out, "reset-password <username>") || !strings.Contains(out, "reset-2fa") ||
			!strings.Contains(out, "disable-user") || !strings.Contains(out, "enable-user") || !strings.Contains(out, "delete-user") ||
			!strings.Contains(out, "export-report <scan-id>") {
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
		"export none":           {"export-report"},
		"export two":            {"export-report", "1", "2"},
		"export not a number":   {"export-report", "abc"},
		"export zero":           {"export-report", "0"},
		"export negative":       {"export-report", "-5"},
		"export flag":           {"export-report", "--all"},
		"disable-user none":     {"disable-user"},
		"enable-user bare name": {"enable-user", "alice"},
		"delete-user two":       {"delete-user", "local:a", "local:b"},
		"delete-user bad":       {"delete-user", "ldap:a"},
		"reset-2fa bad id":      {"reset-2fa", "gitlab-id:abc"},
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
	for in, want := range map[string]accountRef{
		"gitlab:ada:lovelace": {kind: "gitlab", name: "ada:lovelace"},
		"local:root":          {kind: "local", name: "root"},
		"gitlab-id:42":        {kind: "gitlab-id", num: 42},
		"id:7":                {kind: "id", num: 7},
	} {
		if got, ok := parseAccount(in); !ok || got != want || got.String() != in {
			t.Errorf("%q: %+v %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "root", "local:", "gitlab-id:", "gitlab-id:x", "gitlab-id:0", "id:-3", "ldap:x", "gitlab-id:1.5"} {
		if got, ok := parseAccount(bad); ok {
			t.Errorf("%q accepted as %+v", bad, got)
		}
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
	// access records disable (+id), enable (-id) and delete (x id) calls.
	access    []string
	accessErr error
}

func (f *fakeBackend) gitlab(match func(store.User) bool) ([]store.User, error) {
	f.lookups++
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []store.User
	for _, u := range f.users {
		if u.Source == "gitlab" && match(u) {
			out = append(out, u)
		}
	}
	return out, nil
}

func gitlabUser(id int64, name, issuer string, gitlabID int64) store.User {
	return store.User{
		ID: id, Source: "gitlab", Username: name,
		GitlabIssuer: pgtype.Text{String: issuer, Valid: true},
		GitlabUserID: pgtype.Int8{Int64: gitlabID, Valid: true},
	}
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
		userByID: func(_ context.Context, id int64) (store.User, error) {
			for _, u := range f.users {
				if u.ID == id {
					return u, nil
				}
			}
			return store.User{}, pgx.ErrNoRows
		},
		gitlabByName: func(_ context.Context, name string) ([]store.User, error) {
			return f.gitlab(func(u store.User) bool { return u.Username == name })
		},
		gitlabByID: func(_ context.Context, id int64) ([]store.User, error) {
			return f.gitlab(func(u store.User) bool { return u.GitlabUserID.Int64 == id })
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
		setDisabled: func(_ context.Context, id int64, disabled bool) (store.User, error) {
			if f.accessErr != nil {
				return store.User{}, f.accessErr
			}
			f.access = append(f.access, map[bool]string{true: "+", false: "-"}[disabled]+strconv.FormatInt(id, 10))
			return store.User{ID: id}, nil
		},
		deleteUser: func(_ context.Context, id int64) (store.User, error) {
			if f.accessErr != nil {
				return store.User{}, f.accessErr
			}
			f.access = append(f.access, "x"+strconv.FormatInt(id, 10))
			return store.User{ID: id}, nil
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

func ref(t *testing.T, s string) accountRef {
	t.Helper()
	r, ok := parseAccount(s)
	if !ok {
		t.Fatalf("bad account %q", s)
	}
	return r
}

func TestReset2FACommand(t *testing.T) {
	f := &fakeBackend{users: []store.User{
		{ID: 3, Source: "local", Username: "ada"},
		gitlabUser(4, "ada", "https://gitlab.example.com", 9),
	}}
	b := f.backend()
	ctx := context.Background()
	var out bytes.Buffer
	if err := b.reset2FA(ctx, ref(t, "gitlab:ada"), &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.resets, []int64{4}) || !strings.Contains(out.String(), "gitlab:ada") {
		t.Fatalf("resets %v output %q", f.resets, out.String())
	}
	out.Reset()
	if err := b.reset2FA(ctx, ref(t, "local:ada"), &out); err != nil || !reflect.DeepEqual(f.resets, []int64{4, 3}) {
		t.Fatalf("%v resets %v", err, f.resets)
	}
	for _, r := range []string{"gitlab-id:9", "id:4"} {
		if err := b.reset2FA(ctx, ref(t, r), &out); err != nil {
			t.Fatalf("%s: %v", r, err)
		}
	}
	if !reflect.DeepEqual(f.resets, []int64{4, 3, 4, 4}) {
		t.Fatalf("resets %v", f.resets)
	}
	if len(f.rec.events) != 4 || f.rec.events[0] != "cli mfa.reset ada" {
		t.Fatalf("audit %v", f.rec.events)
	}
	for r, want := range map[string]string{
		"local:ghost":  `no local account named "ghost"`,
		"gitlab:ghost": `no gitlab account named "ghost"`,
		"gitlab-id:77": "no gitlab account with GitLab user id 77",
		"id:77":        "no account with id 77",
	} {
		if err := b.reset2FA(ctx, ref(t, r), &out); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v", r, err)
		}
	}
	f.resetErr = store.ErrNotFound
	if err := b.reset2FA(ctx, ref(t, "local:ada"), &out); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("%v", err)
	}
	f.resetErr = errors.New("boom")
	if err := b.reset2FA(ctx, ref(t, "local:ada"), &out); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("%v", err)
	}
	if len(f.rec.events) != 4 {
		t.Fatalf("a failed reset was audited: %v", f.rec.events)
	}
	f.listErr = errors.New("list failed")
	if err := b.reset2FA(ctx, ref(t, "gitlab:ada"), &out); err == nil || !strings.Contains(err.Error(), "list failed") {
		t.Fatalf("%v", err)
	}
	b.localUser = func(context.Context, string) (store.User, error) { return store.User{}, errors.New("db down") }
	if err := b.reset2FA(ctx, ref(t, "local:ada"), &out); err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("%v", err)
	}
}

func TestGitLabNameOfTwoInstancesIsRefused(t *testing.T) {
	f := &fakeBackend{users: []store.User{
		gitlabUser(4, "ada", "https://gitlab.example.com", 9),
		gitlabUser(5, "ada", "https://gitlab.other.example", 12),
		gitlabUser(6, "bob", "https://gitlab.other.example", 9),
	}}
	b := f.backend()
	ctx := context.Background()
	var out bytes.Buffer
	err := b.reset2FA(ctx, ref(t, "gitlab:ada"), &out)
	if err == nil || out.Len() != 0 || len(f.resets) != 0 {
		t.Fatalf("an ambiguous name was accepted: %v, resets %v", err, f.resets)
	}
	for _, want := range []string{"gitlab:ada matches 2 accounts", "gitlab-id:9 (gitlab:ada, GitLab https://gitlab.example.com)", "gitlab-id:12 (gitlab:ada, GitLab https://gitlab.other.example)"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q:\n%s", want, err)
		}
	}
	if err := b.reset2FA(ctx, ref(t, "gitlab-id:12"), &out); err != nil || !reflect.DeepEqual(f.resets, []int64{5}) {
		t.Fatalf("gitlab-id: %v, resets %v", err, f.resets)
	}
	// The same GitLab id on two instances: name the account by its id.
	err = b.changeAccess(ctx, "disable-user", ref(t, "gitlab-id:9"), &out)
	if err == nil || !strings.Contains(err.Error(), "id:4 (gitlab:ada") || !strings.Contains(err.Error(), "id:6 (gitlab:bob") {
		t.Fatalf("ambiguous GitLab id: %v", err)
	}
	if err := b.changeAccess(ctx, "disable-user", ref(t, "id:6"), &out); err != nil || !reflect.DeepEqual(f.access, []string{"+6"}) {
		t.Fatalf("id: %v, access %v", err, f.access)
	}
}

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestExportReport(t *testing.T) {
	const report = `{"SchemaVersion":2,"ArtifactName":"app:1","Results":[]}`
	stored := map[int64][]byte{1: gz(t, report), 2: {}, 3: []byte("not gzip"), 4: gz(t, report)[:20]}
	rec := &recordingAudit{}
	b := &adminBackend{audit: rec, rawReport: func(_ context.Context, id int64) ([]byte, error) {
		if id == 9 {
			return nil, errors.New("connection reset")
		}
		raw, ok := stored[id]
		if !ok {
			return nil, pgx.ErrNoRows
		}
		return raw, nil
	}}
	cases := []struct {
		id      int64
		out     string
		wantErr string
	}{
		{1, report, ""},
		{2, "", "scan 2 was cleared by data retention"},
		{3, "", "scan 3 is not readable"},
		{4, "", "scan 4 is not readable"},
		{5, "", "there is no scan with ID 5"},
		{9, "", "read the report: connection reset"},
	}
	for _, c := range cases {
		var out bytes.Buffer
		err := b.exportReport(context.Background(), c.id, &out)
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("scan %d: %v", c.id, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("scan %d: error %v, want %q", c.id, err, c.wantErr)
		}
		if out.String() != c.out {
			t.Errorf("scan %d: stdout %q, want %q", c.id, out.String(), c.out)
		}
	}
	// One event, for the one successful export, with the ID and nothing else.
	if len(rec.events) != 1 || rec.events[0] != `cli report.exported map[scan_id:1]` {
		t.Errorf("audit events = %q", rec.events)
	}
}

type recordingAudit struct{ events []string }

func (r *recordingAudit) Record(_ context.Context, actor, action string, detail map[string]any) {
	r.events = append(r.events, fmt.Sprint(actor, " ", action, " ", detail))
}

func TestAccessCommands(t *testing.T) {
	f := &fakeBackend{users: []store.User{
		{ID: 3, Source: "local", Username: "root"},
		gitlabUser(4, "ada", "https://gitlab.example.com", 9),
	}}
	b := f.backend()
	ctx := context.Background()
	var out bytes.Buffer
	for _, c := range []struct{ cmd, source, name, want string }{
		{"disable-user", "gitlab", "ada", "The account gitlab:ada was disabled and signed out everywhere."},
		{"enable-user", "gitlab", "ada", "The account gitlab:ada was enabled and can sign in again."},
		{"delete-user", "local", "root", "The account local:root was deleted."},
	} {
		out.Reset()
		if err := b.changeAccess(ctx, c.cmd, ref(t, c.source+":"+c.name), &out); err != nil || strings.TrimSpace(out.String()) != c.want {
			t.Fatalf("%s: %v, output %q", c.cmd, err, out.String())
		}
	}
	if !reflect.DeepEqual(f.access, []string{"+4", "-4", "x3"}) {
		t.Fatalf("calls %v", f.access)
	}
	if !reflect.DeepEqual(f.rec.events, []string{"cli user.disabled ada", "cli user.enabled ada", "cli user.deleted root"}) {
		t.Fatalf("audit %v", f.rec.events)
	}
	if err := b.changeAccess(ctx, "disable-user", ref(t, "gitlab:ghost"), &out); err == nil || !strings.Contains(err.Error(), "no gitlab account") {
		t.Fatalf("ghost: %v", err)
	}
	for err, want := range map[error]string{
		store.ErrLastAdmin: "last enabled administrator",
		store.ErrNotFound:  "no longer exists",
		errors.New("boom"): "boom",
	} {
		f.accessErr = err
		if got := b.changeAccess(ctx, "delete-user", ref(t, "local:root"), &out); got == nil || !strings.Contains(got.Error(), want) {
			t.Fatalf("%v: %v", err, got)
		}
	}
	if len(f.rec.events) != 3 {
		t.Fatalf("a failed change was audited: %v", f.rec.events)
	}
}
