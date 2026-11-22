// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/store"
)

// roleHeader selects the fake principal: "admin" and "viewer" (complete
// sessions), "first-unenrolled", "first-enrolled", "pwchange" or absent.
const roleHeader = "X-Test-Role"

type fakeSessions struct {
	ended    int
	endError error
	authErr  error // returned by Authenticate instead of the role lookup
}

func (f *fakeSessions) Authenticate(r *http.Request) (*auth.Principal, error) {
	if f.authErr != nil {
		return nil, f.authErr
	}
	switch r.Header.Get(roleHeader) {
	case "admin":
		return &auth.Principal{UserID: 1, Source: "local", Username: "root", Role: auth.RoleAdmin, MFAEnrolled: true, MFAVerified: true}, nil
	case "viewer":
		return &auth.Principal{UserID: 2, Source: "gitlab", Username: "vera", Role: auth.RoleViewer, MFAEnrolled: true, MFAVerified: true}, nil
	case "first-unenrolled":
		return &auth.Principal{UserID: 1, Source: "local", Username: "root", Role: auth.RoleAdmin}, nil
	case "first-enrolled":
		return &auth.Principal{UserID: 1, Source: "local", Username: "root", Role: auth.RoleAdmin, MFAEnrolled: true}, nil
	case "pwchange":
		return &auth.Principal{UserID: 1, Source: "local", Username: "root", Role: auth.RoleAdmin, MFAEnrolled: true, MFAVerified: true, MustChangePassword: true}, nil
	}
	return nil, auth.ErrNoSession
}

func (f *fakeSessions) End(http.ResponseWriter, *http.Request) error {
	f.ended++
	return f.endError
}

type fakeUsers struct {
	users      []store.User
	listErr    error
	getErr     error
	setErr     error
	resetErr   error
	resetIDs   []int64
	lastParams store.ListUsersParams
}

func (f *fakeUsers) ListUsers(_ context.Context, arg store.ListUsersParams) ([]store.User, error) {
	f.lastParams = arg
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := []store.User{}
	for _, u := range f.users {
		if u.ID > arg.AfterID && int32(len(out)) < arg.MaxRows {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *fakeUsers) find(id int64) (int, bool) {
	for i, u := range f.users {
		if u.ID == id {
			return i, true
		}
	}
	return 0, false
}

func (f *fakeUsers) GetUser(_ context.Context, id int64) (store.User, error) {
	if f.getErr != nil {
		return store.User{}, f.getErr
	}
	i, ok := f.find(id)
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	return f.users[i], nil
}

func (f *fakeUsers) SetUserRole(_ context.Context, id int64, role string) (store.User, error) {
	if f.setErr != nil {
		return store.User{}, f.setErr
	}
	i, ok := f.find(id)
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	f.users[i].Role = role
	return f.users[i], nil
}

func (f *fakeUsers) SetUserDisabled(_ context.Context, id int64, disabled bool) (store.User, error) {
	if f.setErr != nil {
		return store.User{}, f.setErr
	}
	i, ok := f.find(id)
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	f.users[i].DisabledAt = pgtype.Timestamptz{Time: testTime, Valid: disabled}
	return f.users[i], nil
}

func (f *fakeUsers) DeleteUser(_ context.Context, id int64) (store.User, error) {
	if f.setErr != nil {
		return store.User{}, f.setErr
	}
	i, ok := f.find(id)
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	u := f.users[i]
	f.users = append(f.users[:i], f.users[i+1:]...)
	return u, nil
}

func (f *fakeUsers) ResetMFA(_ context.Context, id int64) error {
	if f.resetErr != nil {
		return f.resetErr
	}
	if _, ok := f.find(id); !ok {
		return store.ErrNotFound
	}
	f.resetIDs = append(f.resetIDs, id)
	return nil
}

type auditEvent struct {
	Actor, Action string
	Detail        map[string]any
}

type fakeAudit struct{ events []auditEvent }

func (f *fakeAudit) Record(_ context.Context, actor, action string, detail map[string]any) {
	f.events = append(f.events, auditEvent{actor, action, detail})
}

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

var testTime = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

func testUser(id int64, username, role string) store.User {
	return store.User{
		ID: id, Source: "gitlab", Username: username, Name: strings.ToUpper(username),
		Email: username + "@example.com", Role: role,
		CreatedAt: pgtype.Timestamptz{Time: testTime, Valid: true},
	}
}

func testAssets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":         {Data: []byte("<html>app</html>")},
		"assets/app.js":      {Data: []byte("console.log(1)")},
		"assets/.secret":     {Data: []byte("x")},
		".env":               {Data: []byte("SECRET=1")},
		"favicon.svg":        {Data: []byte("<svg/>")},
		"bare/file.txt":      {Data: []byte("x")},
		"docs/index.html":    {Data: []byte("<html>docs</html>")},
		"source/.git/config": {Data: []byte("x")},
	}
}

// env bundles a handler with its fakes.
type env struct {
	h        http.Handler
	sessions *fakeSessions
	users    *fakeUsers
	audit    *fakeAudit
	logs     *bytes.Buffer
}

func newEnv(t *testing.T, mutate func(*Deps)) *env {
	t.Helper()
	e := &env{
		sessions: &fakeSessions{},
		users: &fakeUsers{users: []store.User{
			testUser(1, "root", "admin"), testUser(2, "vera", "viewer"), testUser(3, "wes", "viewer"),
		}},
		audit: &fakeAudit{},
		logs:  &bytes.Buffer{},
	}
	d := Deps{
		Log:      slog.New(slog.NewJSONHandler(e.logs, nil)),
		DB:       fakePinger{},
		Users:    e.users,
		Sessions: e.sessions,
		Audit:    e.audit,
		ClientIP: func(*http.Request) netip.Addr { return netip.MustParseAddr("192.0.2.7") },
		Assets:   testAssets(),
		Build:    BuildInfo{Version: "1.2.3", Commit: "abc123", Go: "go1.27"},
	}
	if mutate != nil {
		mutate(&d)
	}
	e.h = New(d)
	return e
}

// do sends a request. hdr holds alternating header names and values.
func (e *env) do(method, target, role, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	if role != "" {
		req.Header.Set(roleHeader, role)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, rec.Body.String())
	}
	return v
}

func assertError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) apiErrorBody {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	e := decode[apiError](t, rec).Error
	if e.Code != code || e.Message == "" {
		t.Fatalf("error = %+v, want code %q with a message", e, code)
	}
	return e
}
