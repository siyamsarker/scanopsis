// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/auth"
)

func TestMeSessionState(t *testing.T) {
	e := newEnv(t, nil)
	type me struct {
		ID                 int64 `json:"id"`
		MFAEnrolled        bool  `json:"mfa_enrolled"`
		MFAVerified        bool  `json:"mfa_verified"`
		MustChangePassword bool  `json:"must_change_password"`
	}
	for role, want := range map[string]me{
		"first-unenrolled": {ID: 1},
		"first-enrolled":   {ID: 1, MFAEnrolled: true},
		"pwchange":         {ID: 1, MFAEnrolled: true, MFAVerified: true, MustChangePassword: true},
		"viewer":           {ID: 2, MFAEnrolled: true, MFAVerified: true},
	} {
		rec := e.do("GET", "/api/v1/me", role, "")
		if rec.Code != 200 {
			t.Fatalf("%s: status %d", role, rec.Code)
		}
		if got := decode[me](t, rec); got != want {
			t.Errorf("%s: me = %+v, want %+v", role, got, want)
		}
	}
}

func TestResetMFA(t *testing.T) {
	e := newEnv(t, nil)
	rec := e.do("DELETE", "/api/v1/users/3/mfa", "admin", "")
	if rec.Code != 204 || rec.Body.Len() != 0 {
		t.Fatalf("reset = %d %q", rec.Code, rec.Body.String())
	}
	if !reflect.DeepEqual(e.users.resetIDs, []int64{3}) {
		t.Errorf("reset ids = %v", e.users.resetIDs)
	}
	want := []auditEvent{{"local:root", audit.ActionMFAReset, map[string]any{"user_id": int64(3), "username": "wes"}}}
	if !reflect.DeepEqual(e.audit.events, want) {
		t.Errorf("audit = %+v, want %+v", e.audit.events, want)
	}
	if audit.ActionMFAReset != "mfa.reset" {
		t.Errorf("action = %q", audit.ActionMFAReset)
	}
}

func TestResetMFASelf(t *testing.T) {
	e := newEnv(t, nil)
	if rec := e.do("DELETE", "/api/v1/users/1/mfa", "admin", ""); rec.Code != 204 {
		t.Fatalf("self reset = %d", rec.Code)
	}
	if !reflect.DeepEqual(e.users.resetIDs, []int64{1}) || len(e.audit.events) != 1 || e.sessions.ended != 0 {
		t.Errorf("ids %v, events %v, ended %d", e.users.resetIDs, e.audit.events, e.sessions.ended)
	}
}

func TestResetMFAErrors(t *testing.T) {
	e := newEnv(t, nil)
	assertError(t, e.do("DELETE", "/api/v1/users/99/mfa", "admin", ""), 404, "not_found")
	for _, id := range []string{"0", "-1", "abc", "1.5"} {
		assertError(t, e.do("DELETE", "/api/v1/users/"+id+"/mfa", "admin", ""), 400, "invalid_request")
	}
	e.users.getErr = errors.New("boom")
	assertError(t, e.do("DELETE", "/api/v1/users/3/mfa", "admin", ""), 500, "internal_error")
	e.users.getErr = nil
	e.users.resetErr = errors.New("boom")
	assertError(t, e.do("DELETE", "/api/v1/users/3/mfa", "admin", ""), 500, "internal_error")
	if len(e.audit.events) != 0 {
		t.Errorf("failed resets must not be audited: %+v", e.audit.events)
	}
}

// recorder is a fake handler that notes it ran and which principal it saw.
type recorder struct {
	calls     int
	principal *auth.Principal
	seen      bool
}

func (c *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.calls++
	c.principal, c.seen = auth.PrincipalFrom(r.Context())
	w.WriteHeader(http.StatusAccepted)
}

func TestOptionalHandlersAreMountedWithTheirGate(t *testing.T) {
	setup, verify, start, confirm := &recorder{}, &recorder{}, &recorder{}, &recorder{}
	pw, list, update := &recorder{}, &recorder{}, &recorder{}
	e := newEnv(t, func(d *Deps) {
		d.Setup, d.MFAVerify, d.MFAStartEnrollment, d.MFAConfirmEnrollment = setup, verify, start, confirm
		d.PasswordChange, d.SettingsList, d.SettingsUpdate = pw, list, update
	})
	for _, tt := range []struct {
		method, target, role string
		h                    *recorder
		username             string // principal expected; "" for none
	}{
		{"GET", "/api/v1/setup", "", setup, ""},
		{"POST", "/api/v1/setup", "", setup, ""},
		{"POST", "/api/v1/auth/mfa/verify", "first-enrolled", verify, "root"},
		{"POST", "/api/v1/auth/mfa/enrollment", "first-unenrolled", start, "root"},
		{"POST", "/api/v1/auth/mfa/enrollment/confirm", "first-unenrolled", confirm, "root"},
		{"POST", "/api/v1/me/password", "pwchange", pw, "root"},
		{"GET", "/api/v1/settings", "admin", list, "root"},
		{"PATCH", "/api/v1/settings", "admin", update, "root"},
	} {
		before := tt.h.calls
		rec := e.do(tt.method, tt.target, tt.role, "{}")
		if rec.Code != http.StatusAccepted || tt.h.calls != before+1 {
			t.Errorf("%s %s: status %d, calls %d->%d", tt.method, tt.target, rec.Code, before, tt.h.calls)
			continue
		}
		if tt.username == "" && tt.h.seen {
			t.Errorf("%s %s: public handler saw a principal", tt.method, tt.target)
		}
		if tt.username != "" && (!tt.h.seen || tt.h.principal.Username != tt.username) {
			t.Errorf("%s %s: principal = %+v", tt.method, tt.target, tt.h.principal)
		}
	}

	// A blocked request never reaches the handler.
	calls := list.calls
	assertError(t, e.do("GET", "/api/v1/settings", "viewer", ""), 403, "forbidden")
	assertError(t, e.do("GET", "/api/v1/settings", "first-enrolled", ""), 403, "mfa_required")
	assertError(t, e.do("GET", "/api/v1/settings", "", ""), 401, "unauthenticated")
	if list.calls != calls {
		t.Error("settings handler ran for a blocked request")
	}
}

func TestMissingOptionalHandlersAreNotEnabled(t *testing.T) {
	e := newEnv(t, nil)
	for _, tt := range []struct{ method, target, role string }{
		{"GET", "/api/v1/setup", ""},
		{"POST", "/api/v1/setup", ""},
		{"POST", "/api/v1/auth/mfa/verify", "first-enrolled"},
		{"POST", "/api/v1/auth/mfa/enrollment", "first-unenrolled"},
		{"POST", "/api/v1/auth/mfa/enrollment/confirm", "first-unenrolled"},
		{"POST", "/api/v1/me/password", "pwchange"},
		{"GET", "/api/v1/settings", "admin"},
		{"PATCH", "/api/v1/settings", "admin"},
	} {
		assertError(t, e.do(tt.method, tt.target, tt.role, "{}"), 404, "not_enabled")
	}
}

func TestGateMessagesTellTheNextStep(t *testing.T) {
	e := newEnv(t, nil)
	for role, want := range map[string]string{
		"first-unenrolled": "Set up two-factor authentication to continue.",
		"first-enrolled":   "Enter your two-factor authentication code to continue.",
		"pwchange":         "Change your password to continue.",
	} {
		if got := decode[apiError](t, e.do("GET", "/api/v1/users", role, "")).Error.Message; got != want {
			t.Errorf("%s: message = %q, want %q", role, got, want)
		}
	}
}

func TestGatesReportSessionStoreFailure(t *testing.T) {
	e := newEnv(t, func(d *Deps) { d.PasswordChange = &recorder{} })
	e.sessions.authErr = errors.New("db down")
	for _, r := range [][2]string{{"GET", "/api/v1/users"}, {"DELETE", "/api/v1/users/3/mfa"}, {"POST", "/api/v1/me/password"}} {
		assertError(t, e.do(r[0], r[1], "admin", ""), 500, "internal_error")
	}
}
