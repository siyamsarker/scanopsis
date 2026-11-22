// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/store"
)

// principals of the RBAC matrix, in column order.
var rbacPrincipals = []string{"", "first-unenrolled", "first-enrolled", "pwchange", "viewer", "admin"}

// rbacRoutes lists every route with the expected outcome per principal:
// anonymous, first factor only (not enrolled), first factor only (enrolled),
// verified with a pending password change, complete viewer, complete admin.
// A cell is a status, optionally followed by ":" and the error code.
var rbacRoutes = []struct {
	method, target, body string
	want                 [6]string
}{
	{"GET", "/healthz", "", [6]string{"200", "200", "200", "200", "200", "200"}},
	{"GET", "/readyz", "", [6]string{"200", "200", "200", "200", "200", "200"}},
	{"GET", "/api/v1/version", "", [6]string{"200", "200", "200", "200", "200", "200"}},
	{"GET", "/api/v1/auth/methods", "", [6]string{"200", "200", "200", "200", "200", "200"}},
	{"GET", "/api/v1/auth/gitlab/login", "", [6]string{"204", "204", "204", "204", "204", "204"}},
	{"GET", "/api/v1/auth/gitlab/callback", "", [6]string{"204", "204", "204", "204", "204", "204"}},
	{"POST", "/api/v1/auth/local/login", "{}", [6]string{"204", "204", "204", "204", "204", "204"}},
	{"POST", "/api/v1/auth/logout", "", [6]string{"204", "204", "204", "204", "204", "204"}},
	{"GET", "/api/v1/setup", "", [6]string{"204", "204", "204", "204", "204", "204"}},
	{"POST", "/api/v1/setup", "{}", [6]string{"204", "204", "204", "204", "204", "204"}},
	{"POST", "/api/v1/auth/mfa/verify", "{}", [6]string{"401:unauthenticated", "204", "204", "204", "204", "204"}},
	{"POST", "/api/v1/auth/mfa/enrollment", "", [6]string{"401:unauthenticated", "204", "204", "204", "204", "204"}},
	{"POST", "/api/v1/auth/mfa/enrollment/confirm", "{}", [6]string{"401:unauthenticated", "204", "204", "204", "204", "204"}},
	{"GET", "/api/v1/me", "", [6]string{"401:unauthenticated", "200", "200", "200", "200", "200"}},
	{"POST", "/api/v1/me/password", "{}", [6]string{
		"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "204", "204", "204"}},
	{"GET", "/api/v1/me/sessions", "", [6]string{
		"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "204", "204"}},
	{"DELETE", "/api/v1/me/sessions", "", [6]string{
		"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "204", "204"}},
	{"DELETE", "/api/v1/me/sessions/3", "", [6]string{
		"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "204", "204"}},
	{"GET", "/api/v1/users", "", [6]string{
		"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "403:forbidden", "200"}},
	{"PATCH", "/api/v1/users/3", `{"role":"viewer"}`, [6]string{
		"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "403:forbidden", "200"}},
	{"DELETE", "/api/v1/users/3", "", [6]string{
		"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "403:forbidden", "204"}},
	{"DELETE", "/api/v1/users/3/mfa", "", [6]string{
		"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "403:forbidden", "204"}},
	{"GET", "/api/v1/settings", "", [6]string{
		"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "403:forbidden", "204"}},
	{"PATCH", "/api/v1/settings", "{}", [6]string{
		"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "403:forbidden", "204"}},
	{"POST", "/api/v1/ingest/trivy", "", [6]string{"204", "204", "204", "204", "204", "204"}},
	{"POST", "/api/v1/hooks/sonarqube", "", [6]string{"204", "204", "204", "204", "204", "204"}},
	{"GET", "/api/v1/portfolio", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "204", "204"}},
	{"GET", "/api/v1/portfolio/history", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "204", "204"}},
	{"GET", "/api/v1/services", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "204", "204"}},
	{"POST", "/api/v1/services", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "403:forbidden", "204"}},
	{"GET", "/api/v1/services/3", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "204", "204"}},
	{"PATCH", "/api/v1/services/3", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "403:forbidden", "204"}},
	{"DELETE", "/api/v1/services/3", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "403:forbidden", "204"}},
	{"GET", "/api/v1/services/3/overview", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "204", "204"}},
	{"GET", "/api/v1/services/3/scans", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "204", "204"}},
	{"GET", "/api/v1/scans/3", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "204", "204"}},
	{"GET", "/api/v1/scans/3/diff", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "204", "204"}},
	{"GET", "/api/v1/scans/3/earlier", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "204", "204"}},
	{"GET", "/api/v1/services/3/vulnerabilities", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "204", "204"}},
	{"GET", "/api/v1/services/3/secrets", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "204", "204"}},
	{"GET", "/api/v1/services/3/trends", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "204", "204"}},
	{"GET", "/api/v1/services/3/sonar", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "204", "204"}},
	{"GET", "/api/v1/search/vulnerabilities?id=CVE-2024-3094", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "204", "204"}},
	{"GET", "/api/v1/admin/audit", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "403:forbidden", "204"}},
	{"GET", "/api/v1/admin/integrations/trivy", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "403:forbidden", "204"}},
	{"GET", "/api/v1/admin/integrations/sonarqube", "", [6]string{"401:unauthenticated", "403:mfa_enrollment_required", "403:mfa_required", "403:password_change_required", "403:forbidden", "204"}},
}

func TestRBACMatrix(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, tt := range rbacRoutes {
		for i, role := range rbacPrincipals {
			t.Run(tt.method+" "+tt.target+" as "+role, func(t *testing.T) {
				// A fresh environment per cell: some routes change state.
				e := newEnv(t, func(d *Deps) {
					d.LocalLogin, d.GitLabLogin, d.GitLabCallback = ok, ok, ok
					d.Setup, d.MFAVerify, d.MFAStartEnrollment, d.MFAConfirmEnrollment = ok, ok, ok, ok
					d.PasswordChange, d.SettingsList, d.SettingsUpdate = ok, ok, ok
					d.MySessionsList, d.MySessionsEndOthers, d.MySessionEnd = ok, ok, ok
					d.Features = map[string]http.Handler{}
					for _, id := range featureIDs {
						d.Features[id] = ok
					}
				})
				rec := e.do(tt.method, tt.target, role, tt.body)
				status, code, _ := strings.Cut(tt.want[i], ":")
				if status != fmt.Sprint(rec.Code) {
					t.Fatalf("status = %d, want %s; body %s", rec.Code, status, rec.Body.String())
				}
				if code != "" {
					assertError(t, rec, rec.Code, code)
				}
			})
		}
	}
}

func TestRBACMatrixCoversEveryRoute(t *testing.T) {
	// Guards the matrix above: every registered route must appear in it.
	covered := map[string]bool{}
	ids := regexp.MustCompile(`/[0-9]+(/|$)`)
	for _, tt := range rbacRoutes {
		path, _, _ := strings.Cut(tt.target, "?")
		covered[tt.method+" "+ids.ReplaceAllString(path, "/{id}$1")] = true
	}
	s := &server{}
	for _, rt := range s.routes() {
		if !covered[rt.method+" "+rt.pattern] {
			t.Errorf("route %s %s is missing from TestRBACMatrix", rt.method, rt.pattern)
		}
	}
	if len(covered) != len(s.routes()) {
		t.Errorf("matrix lists %d routes, server registers %d", len(covered), len(s.routes()))
	}
}

func TestErrorMessages(t *testing.T) {
	e := newEnv(t, nil)
	if m := assertError(t, e.do("GET", "/api/v1/me", "", ""), 401, "unauthenticated").Message; m != "Sign in to continue." {
		t.Errorf("401 message = %q", m)
	}
	if m := assertError(t, e.do("GET", "/api/v1/users", "viewer", ""), 403, "forbidden").Message; m != "This action requires the admin role." {
		t.Errorf("403 message = %q", m)
	}
}

func TestHealthAndReady(t *testing.T) {
	e := newEnv(t, nil)
	rec := e.do("GET", "/healthz", "", "")
	if rec.Code != 200 || rec.Body.String() != `{"status":"ok"}`+"\n" || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("healthz = %d %q %q", rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
	}
	rec = e.do("GET", "/readyz", "", "")
	if rec.Code != 200 || rec.Body.String() != `{"status":"ready"}`+"\n" {
		t.Fatalf("readyz = %d %q", rec.Code, rec.Body.String())
	}

	e = newEnv(t, func(d *Deps) { d.DB = fakePinger{err: errors.New("dial tcp 10.0.0.5: password=hunter2")} })
	rec = e.do("GET", "/readyz", "", "")
	if rec.Code != 503 || rec.Body.String() != `{"status":"not_ready","reason":"database unreachable"}`+"\n" {
		t.Fatalf("readyz failing = %d %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(e.logs.String(), "hunter2") {
		t.Error("ping error should be logged")
	}
}

func TestVersion(t *testing.T) {
	rec := newEnv(t, nil).do("GET", "/api/v1/version", "", "")
	if got := decode[BuildInfo](t, rec); got != (BuildInfo{Version: "1.2.3", Commit: "abc123", Go: "go1.27"}) {
		t.Fatalf("version = %+v", got)
	}
}

func TestAuthMethods(t *testing.T) {
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, tt := range []struct {
		local, gitlab http.Handler
		want          string
	}{
		{nil, nil, `{"gitlab":false,"local":false}`},
		{h, nil, `{"gitlab":false,"local":true}`},
		{nil, h, `{"gitlab":true,"local":false}`},
	} {
		e := newEnv(t, func(d *Deps) { d.LocalLogin, d.GitLabLogin = tt.local, tt.gitlab })
		if got := strings.TrimSpace(e.do("GET", "/api/v1/auth/methods", "", "").Body.String()); got != tt.want {
			t.Errorf("methods = %s, want %s", got, tt.want)
		}
	}
}

func TestDisabledLoginsAreNotEnabled(t *testing.T) {
	e := newEnv(t, nil)
	for _, r := range [][2]string{
		{"GET", "/api/v1/auth/gitlab/login"},
		{"GET", "/api/v1/auth/gitlab/callback"},
		{"POST", "/api/v1/auth/local/login"},
	} {
		assertError(t, e.do(r[0], r[1], "", "{}"), 404, "not_enabled")
	}
}

func TestLoginHandlersAreDelegated(t *testing.T) {
	redirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://gitlab.example.com/auth?x=1", http.StatusFound)
	})
	e := newEnv(t, func(d *Deps) { d.GitLabLogin, d.GitLabCallback, d.LocalLogin = redirect, redirect, redirect })
	rec := e.do("GET", "/api/v1/auth/gitlab/login?return_to=/x", "", "")
	if rec.Code != 302 || rec.Header().Get("Location") != "https://gitlab.example.com/auth?x=1" {
		t.Fatalf("login = %d %v", rec.Code, rec.Header())
	}
	if rec := e.do("POST", "/api/v1/auth/local/login", "", `{}`); rec.Code != 302 {
		t.Fatalf("local login = %d", rec.Code)
	}
}

func TestLogout(t *testing.T) {
	e := newEnv(t, nil)
	if rec := e.do("POST", "/api/v1/auth/logout", "viewer", ""); rec.Code != 204 || rec.Body.Len() != 0 {
		t.Fatalf("logout = %d %q", rec.Code, rec.Body.String())
	}
	if rec := e.do("POST", "/api/v1/auth/logout", "", ""); rec.Code != 204 {
		t.Fatalf("anonymous logout = %d", rec.Code)
	}
	// Only signed-in users are audited; an anonymous logout records nothing.
	want := []auditEvent{{"gitlab:vera", "logout", nil}}
	if !reflect.DeepEqual(e.audit.events, want) {
		t.Fatalf("audit = %+v, want %+v", e.audit.events, want)
	}
	if e.sessions.ended != 2 {
		t.Fatalf("End called %d times, want 2", e.sessions.ended)
	}
}

func TestLogoutEndFailure(t *testing.T) {
	e := newEnv(t, nil)
	e.sessions.endError = errors.New("db down")
	assertError(t, e.do("POST", "/api/v1/auth/logout", "viewer", ""), 500, "internal_error")
	if len(e.audit.events) != 0 {
		t.Fatalf("no audit expected on failure, got %+v", e.audit.events)
	}
}

func TestLogoutSessionLookupFailureDoesNotBlock(t *testing.T) {
	e := newEnv(t, nil)
	e.sessions.authErr = errors.New("db down")
	if rec := e.do("POST", "/api/v1/auth/logout", "viewer", ""); rec.Code != 204 {
		t.Fatalf("logout = %d, want 204", rec.Code)
	}
	if e.sessions.ended != 1 {
		t.Fatalf("End called %d times, want 1", e.sessions.ended)
	}
	if len(e.audit.events) != 0 {
		t.Fatalf("no actor is known, got audit %+v", e.audit.events)
	}
	if !strings.Contains(e.logs.String(), "db down") {
		t.Fatalf("lookup error not logged: %s", e.logs.String())
	}
}

func TestSessionStoreFailureIsNotUnauthenticated(t *testing.T) {
	e := newEnv(t, nil)
	e.sessions.authErr = errors.New("db down")
	assertError(t, e.do("GET", "/api/v1/me", "viewer", ""), 500, "internal_error")
	if !strings.Contains(e.logs.String(), "db down") {
		t.Fatalf("error not logged: %s", e.logs.String())
	}
	// No session at all is still a 401.
	e.sessions.authErr = nil
	assertError(t, e.do("GET", "/api/v1/me", "", ""), 401, "unauthenticated")
	e.sessions.authErr = fmt.Errorf("wrapped: %w", auth.ErrNoSession)
	assertError(t, e.do("GET", "/api/v1/me", "", ""), 401, "unauthenticated")
}

func TestMe(t *testing.T) {
	e := newEnv(t, nil)
	e.users.users[1].LastLoginAt = pgtype.Timestamptz{Time: testTime, Valid: true}
	rec := e.do("GET", "/api/v1/me", "viewer", "")
	want := `{"id":2,"source":"gitlab","username":"vera","name":"VERA","email":"vera@example.com","role":"viewer",` +
		`"created_at":"2026-03-01T12:00:00Z","last_login_at":"2026-03-01T12:00:00Z","disabled_at":null,` +
		`"mfa_enrolled":true,"mfa_verified":true,"must_change_password":false}` + "\n"
	if rec.Code != 200 || rec.Body.String() != want {
		t.Fatalf("me = %d %s", rec.Code, rec.Body.String())
	}
	rec = e.do("GET", "/api/v1/me", "admin", "")
	if !strings.Contains(rec.Body.String(), `"last_login_at":null`) {
		t.Fatalf("never-signed-in user should have null last_login_at: %s", rec.Body.String())
	}

	e.users.getErr = errors.New("boom")
	assertError(t, e.do("GET", "/api/v1/me", "admin", ""), 500, "internal_error")
	e.users.getErr = nil
	e.users.users = nil // the principal's row was deleted
	assertError(t, e.do("GET", "/api/v1/me", "admin", ""), 401, "unauthenticated")
}

func TestListUsersPaging(t *testing.T) {
	e := newEnv(t, nil)
	ids := func(p userPage) []int64 {
		out := []int64{}
		for _, u := range p.Items {
			out = append(out, u.ID)
		}
		return out
	}

	p := decode[userPage](t, e.do("GET", "/api/v1/users?limit=2", "admin", ""))
	if !reflect.DeepEqual(ids(p), []int64{1, 2}) || p.NextAfter == nil || *p.NextAfter != 2 {
		t.Fatalf("page 1 = %v next %v", ids(p), p.NextAfter)
	}
	if e.users.lastParams.MaxRows != 3 || e.users.lastParams.AfterID != 0 {
		t.Fatalf("store params = %+v, want limit+1 rows", e.users.lastParams)
	}
	p = decode[userPage](t, e.do("GET", "/api/v1/users?limit=2&after=2", "admin", ""))
	if !reflect.DeepEqual(ids(p), []int64{3}) || p.NextAfter != nil {
		t.Fatalf("page 2 = %v next %v", ids(p), p.NextAfter)
	}
	// Exactly limit rows left: no next page.
	p = decode[userPage](t, e.do("GET", "/api/v1/users?limit=3", "admin", ""))
	if len(p.Items) != 3 || p.NextAfter != nil {
		t.Fatalf("exact page = %v next %v", ids(p), p.NextAfter)
	}
	e.do("GET", "/api/v1/users", "admin", "")
	if e.users.lastParams.MaxRows != 51 {
		t.Fatalf("default MaxRows = %d, want 51", e.users.lastParams.MaxRows)
	}
	rec := e.do("GET", "/api/v1/users?after=99", "admin", "")
	if got := rec.Body.String(); got != `{"items":[],"next_after":null}`+"\n" {
		t.Fatalf("empty page = %s", got)
	}
}

func TestListUsersReportsTwoFactor(t *testing.T) {
	e := newEnv(t, nil)
	e.users.users[0].TotpEnabledAt = pgtype.Timestamptz{Time: testTime, Valid: true}
	e.users.users[1].TotpEnabledAt = pgtype.Timestamptz{}
	p := decode[userPage](t, e.do("GET", "/api/v1/users", "admin", ""))
	if len(p.Items) < 2 || !p.Items[0].MFAEnrolled || p.Items[1].MFAEnrolled {
		t.Fatalf("mfa_enrolled = %+v", p.Items)
	}
}

func TestListUsersValidation(t *testing.T) {
	e := newEnv(t, nil)
	for _, q := range []string{"after=-1", "after=x", "limit=0", "limit=201", "limit=-5", "limit=abc", "limit=1.5"} {
		assertError(t, e.do("GET", "/api/v1/users?"+q, "admin", ""), 400, "invalid_parameter")
	}
	for _, q := range []string{"limit=1", "limit=200", "after=0"} {
		if rec := e.do("GET", "/api/v1/users?"+q, "admin", ""); rec.Code != 200 {
			t.Errorf("%s: status %d", q, rec.Code)
		}
	}
	e.users.listErr = errors.New("boom")
	assertError(t, e.do("GET", "/api/v1/users", "admin", ""), 500, "internal_error")
}

func TestUpdateUser(t *testing.T) {
	e := newEnv(t, nil)
	rec := e.do("PATCH", "/api/v1/users/3", "admin", `{"role":"admin"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	if u := decode[userJSON](t, rec); u.ID != 3 || u.Role != "admin" || u.Username != "wes" {
		t.Fatalf("user = %+v", u)
	}
	want := []auditEvent{{"local:root", "user.role_changed", map[string]any{
		"user_id": int64(3), "username": "wes", "from": "viewer", "to": "admin",
	}}}
	if !reflect.DeepEqual(e.audit.events, want) {
		t.Fatalf("audit = %+v, want %+v", e.audit.events, want)
	}
}

func TestUpdateUserValidation(t *testing.T) {
	e := newEnv(t, nil)
	tests := []struct{ name, target, body string }{
		{"bad id", "/api/v1/users/abc", `{"role":"admin"}`},
		{"zero id", "/api/v1/users/0", `{"role":"admin"}`},
		{"negative id", "/api/v1/users/-4", `{"role":"admin"}`},
		{"empty body", "/api/v1/users/3", ``},
		{"not json", "/api/v1/users/3", `role=admin`},
		{"unknown role", "/api/v1/users/3", `{"role":"owner"}`},
		{"neither role nor disabled", "/api/v1/users/3", `{}`},
		{"disabled not a boolean", "/api/v1/users/3", `{"disabled":"yes"}`},
		{"null disabled", "/api/v1/users/3", `{"disabled":null}`},
		{"unknown field", "/api/v1/users/3", `{"role":"admin","name":"x"}`},
		{"trailing data", "/api/v1/users/3", `{"role":"admin"}{"role":"viewer"}`},
		{"oversized", "/api/v1/users/3", `{"role":"admin","pad":"` + strings.Repeat("a", 9<<10) + `"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertError(t, e.do("PATCH", tt.target, "admin", tt.body), 400, "invalid_request")
		})
	}
	if len(e.audit.events) != 0 || e.users.users[2].Role != "viewer" {
		t.Fatal("invalid requests must not change anything")
	}
}

func TestUpdateUserStoreErrors(t *testing.T) {
	e := newEnv(t, nil)
	assertError(t, e.do("PATCH", "/api/v1/users/42", "admin", `{"role":"admin"}`), 404, "not_found")

	e.users.setErr = store.ErrLastAdmin
	msg := assertError(t, e.do("PATCH", "/api/v1/users/1", "admin", `{"role":"viewer"}`), 409, "last_admin").Message
	if msg != "At least one enabled administrator must remain. Promote or enable another administrator first." {
		t.Fatalf("message = %q", msg)
	}
	e.users.setErr = store.ErrNotFound // deleted between read and write
	assertError(t, e.do("PATCH", "/api/v1/users/1", "admin", `{"role":"viewer"}`), 404, "not_found")
	e.users.setErr = errors.New("boom")
	assertError(t, e.do("PATCH", "/api/v1/users/1", "admin", `{"role":"viewer"}`), 500, "internal_error")
	e.users.setErr = nil
	e.users.getErr = errors.New("boom")
	assertError(t, e.do("PATCH", "/api/v1/users/1", "admin", `{"role":"viewer"}`), 500, "internal_error")
	if len(e.audit.events) != 0 {
		t.Fatalf("failed changes must not be audited: %+v", e.audit.events)
	}
}

func TestUnknownAndWrongMethodAPI(t *testing.T) {
	e := newEnv(t, nil)
	assertError(t, e.do("GET", "/api/v1/nope", "admin", ""), 404, "not_found")
	assertError(t, e.do("GET", "/api/", "", ""), 404, "not_found")
	assertError(t, e.do("GET", "/api/v1/users/3/extra", "admin", ""), 404, "not_found")

	rec := e.do("DELETE", "/api/v1/me", "admin", "")
	assertError(t, rec, 405, "method_not_allowed")
	if rec.Header().Get("Allow") != "GET" {
		t.Fatalf("Allow = %q", rec.Header().Get("Allow"))
	}
	assertError(t, e.do("GET", "/api/v1/auth/logout", "", ""), 405, "method_not_allowed")
	assertError(t, e.do("POST", "/api/v1/users/3", "admin", ""), 405, "method_not_allowed")
}

func TestMatchPattern(t *testing.T) {
	if matchPattern("/a/{id}", "/a/") || matchPattern("/a/{id}", "/a") || !matchPattern("/a/{id}", "/a/7") {
		t.Fatal("wildcard matching is wrong")
	}
}

func TestBodyLimit(t *testing.T) {
	var readErr error
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	})
	e := newEnv(t, func(d *Deps) { d.LocalLogin = h })
	e.do("POST", "/api/v1/auth/local/login", "", strings.Repeat("a", 1<<20))
	if readErr != nil {
		t.Fatalf("1 MiB body should be accepted: %v", readErr)
	}
	e.do("POST", "/api/v1/auth/local/login", "", strings.Repeat("a", 1<<20+1))
	if readErr == nil {
		t.Fatal("body over 1 MiB should be rejected")
	}
}

func TestJSONResponsesAreNotStored(t *testing.T) {
	e := newEnv(t, nil)
	for _, tc := range []struct{ path, user string }{
		{"/api/v1/me", "viewer"},
		{"/api/v1/users", "admin"},
		{"/api/v1/me", ""}, // error responses too
		{"/healthz", ""},
	} {
		rec := e.do("GET", tc.path, tc.user, "")
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("GET %s as %q: Cache-Control = %q, want no-store", tc.path, tc.user, got)
		}
	}
}

func TestAuthMethodsFollowsRuntimeSettings(t *testing.T) {
	gitlab, local := false, true
	e := newEnv(t, func(d *Deps) { d.Methods = func() (bool, bool) { return gitlab, local } })
	check := func(want string) {
		t.Helper()
		rec := e.do("GET", "/api/v1/auth/methods", "", "")
		if got := strings.TrimSpace(rec.Body.String()); got != want {
			t.Fatalf("methods = %s, want %s", got, want)
		}
	}
	check(`{"gitlab":false,"local":true}`)
	gitlab, local = true, false
	check(`{"gitlab":true,"local":false}`)
}

func TestNotEnabled(t *testing.T) {
	rec := httptest.NewRecorder()
	NotEnabled("GitLab sign-in").ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/", nil))
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"code":"not_enabled"`) ||
		!strings.Contains(rec.Body.String(), "GitLab sign-in is not enabled on this server.") {
		t.Fatalf("NotEnabled = %d %s", rec.Code, rec.Body.String())
	}
}

var featureIDs = []string{
	"ingestTrivy", "sonarWebhook", "getPortfolio", "getPortfolioHistory", "listServices", "createService", "getService",
	"updateService", "deleteService", "getServiceOverview", "listServiceScans", "getScan", "getScanDiff",
	"listEarlierScans",
	"listServiceVulnerabilities", "listServiceSecrets", "getServiceTrends", "getServiceSonar",
	"searchVulnerabilities", "getTrivyIntegration", "getSonarIntegration", "listAuditEvents",
}

func TestFeatureNotEnabled(t *testing.T) {
	e := newEnv(t, nil)
	assertError(t, e.do("GET", "/api/v1/portfolio", "viewer", ""), 404, "not_enabled")
	assertError(t, e.do("POST", "/api/v1/ingest/trivy", "", ""), 404, "not_enabled")
}

func TestIngestBodyIsNotCappedByTheAPILimit(t *testing.T) {
	var got int64
	e := newEnv(t, func(d *Deps) {
		d.Features = map[string]http.Handler{"ingestTrivy": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n, err := io.Copy(io.Discard, r.Body)
			if err != nil {
				t.Errorf("reading the upload: %v", err)
			}
			got = n
			w.WriteHeader(http.StatusCreated)
		})}
	})
	big := strings.Repeat("x", 3<<20) // larger than the 1 MiB API limit
	if rec := e.do("POST", "/api/v1/ingest/trivy", "", big); rec.Code != http.StatusCreated || got != int64(len(big)) {
		t.Fatalf("status %d, handler read %d bytes, want %d", rec.Code, got, len(big))
	}
}

func TestExportedWriters(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusTeapot, "some_code", "Some message.")
	if rec.Code != http.StatusTeapot || rec.Header().Get("Cache-Control") != "no-store" ||
		strings.TrimSpace(rec.Body.String()) != `{"error":{"code":"some_code","message":"Some message."}}` {
		t.Fatalf("WriteError = %d %q %q", rec.Code, rec.Header(), rec.Body.String())
	}
	rec = httptest.NewRecorder()
	WriteJSON(rec, http.StatusCreated, map[string]int{"n": 1})
	if rec.Code != http.StatusCreated || rec.Header().Get("Content-Type") != "application/json" || strings.TrimSpace(rec.Body.String()) != `{"n":1}` {
		t.Fatalf("WriteJSON = %d %q", rec.Code, rec.Body.String())
	}
}

func TestDisableEnableAndDeleteUser(t *testing.T) {
	e := newEnv(t, nil)
	rec := e.do("PATCH", "/api/v1/users/3", "admin", `{"disabled":true}`)
	if rec.Code != 200 {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body.String())
	}
	if u := decode[userJSON](t, rec); u.DisabledAt == nil || !u.DisabledAt.Equal(testTime) || u.Role != "viewer" {
		t.Fatalf("disabled user = %+v", u)
	}
	rec = e.do("PATCH", "/api/v1/users/3", "admin", `{"disabled":false,"role":"admin"}`)
	if u := decode[userJSON](t, rec); rec.Code != 200 || u.DisabledAt != nil || u.Role != "admin" {
		t.Fatalf("enable and promote: %d %+v", rec.Code, u)
	}
	if rec := e.do("DELETE", "/api/v1/users/3", "admin", ""); rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if _, found := e.users.find(3); found {
		t.Fatal("user 3 still exists")
	}
	assertError(t, e.do("DELETE", "/api/v1/users/3", "admin", ""), 404, "not_found")
	detail := map[string]any{"user_id": int64(3), "username": "wes", "source": "gitlab"}
	want := []auditEvent{
		{"local:root", "user.disabled", detail},
		{"local:root", "user.role_changed", map[string]any{"user_id": int64(3), "username": "wes", "from": "viewer", "to": "admin"}},
		{"local:root", "user.enabled", detail},
		{"local:root", "user.deleted", detail},
	}
	if !reflect.DeepEqual(e.audit.events, want) {
		t.Fatalf("audit = %+v\nwant  %+v", e.audit.events, want)
	}
}

func TestDisableAndDeleteRefuseOwnAccountAndLastAdmin(t *testing.T) {
	e := newEnv(t, nil)
	// The admin principal is user 1.
	assertError(t, e.do("PATCH", "/api/v1/users/1", "admin", `{"disabled":true}`), 409, "own_account")
	assertError(t, e.do("DELETE", "/api/v1/users/1", "admin", ""), 409, "own_account")
	// Enabling oneself is harmless (and impossible while disabled): allowed.
	if rec := e.do("PATCH", "/api/v1/users/1", "admin", `{"disabled":false}`); rec.Code != 200 {
		t.Fatalf("enable self: %d", rec.Code)
	}
	e.users.setErr = store.ErrLastAdmin
	assertError(t, e.do("PATCH", "/api/v1/users/2", "admin", `{"disabled":true}`), 409, "last_admin")
	assertError(t, e.do("DELETE", "/api/v1/users/2", "admin", ""), 409, "last_admin")
	e.users.setErr = errors.New("boom")
	assertError(t, e.do("DELETE", "/api/v1/users/2", "admin", ""), 500, "internal_error")
	assertError(t, e.do("DELETE", "/api/v1/users/x", "admin", ""), 400, "invalid_request")
	if len(e.audit.events) != 1 || e.audit.events[0].Action != "user.enabled" {
		t.Fatalf("audit = %+v", e.audit.events)
	}
}
