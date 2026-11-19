// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/store"
)

var _ PasswordStore = (*store.Queries)(nil)

type fakePasswordStore struct {
	user      store.User
	getErr    error
	missing   bool
	setErr    error
	deleteErr error
	set       []store.SetPasswordParams
	deleted   []store.DeleteOtherUserSessionsParams
}

func (f *fakePasswordStore) GetUser(context.Context, int64) (store.User, error) {
	if f.missing {
		return store.User{}, pgx.ErrNoRows
	}
	return f.user, f.getErr
}

func (f *fakePasswordStore) SetPassword(_ context.Context, arg store.SetPasswordParams) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.set = append(f.set, arg)
	return nil
}

func (f *fakePasswordStore) DeleteOtherUserSessions(_ context.Context, arg store.DeleteOtherUserSessionsParams) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, arg)
	return nil
}

const (
	pwUser    = "root"
	pwCurrent = "current passphrase!"
	pwNew     = "a brand new passphrase"
)

type pwFixture struct {
	h     http.Handler
	store *fakePasswordStore
	audit *fakeAudit
	clock *fakeClock
	p     *Principal
	log   bytes.Buffer
}

func newPWFixture(t *testing.T, burst int) *pwFixture {
	t.Helper()
	f := &pwFixture{audit: &fakeAudit{}, clock: newFakeClock()}
	f.store = &fakePasswordStore{user: store.User{
		ID: 7, Source: SourceLocal, Username: pwUser, Role: store.RoleAdmin, MustChangePassword: true,
		PasswordHash: pgtype.Text{String: cheapHash(pwCurrent, 8, 1, 1), Valid: true},
	}}
	f.p = &Principal{UserID: 7, Source: SourceLocal, Username: pwUser, Role: RoleAdmin,
		SessionID: []byte("session-hash"), MFAEnrolled: true, MFAVerified: true, MustChangePassword: true}
	f.h = NewPasswordChange(f.store, f.audit, NewRateLimiter(12*time.Second, burst, f.clock.Now), slog.New(slog.NewTextHandler(&f.log, nil)))
	return f
}

func pwBody(current, next string) string {
	b, _ := json.Marshal(map[string]string{"current_password": current, "new_password": next})
	return string(b)
}

func (f *pwFixture) do(method, body string, p *Principal) *httptest.ResponseRecorder {
	r := newRequest(method, "/api/v1/me/password", strings.NewReader(body))
	if p != nil {
		r = r.WithContext(WithPrincipal(r.Context(), p))
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

func (f *pwFixture) post(body string) *httptest.ResponseRecorder {
	return f.do(http.MethodPost, body, f.p)
}

func TestPasswordChangeSuccess(t *testing.T) {
	f := newPWFixture(t, 10)
	w := f.post(pwBody(pwCurrent, pwNew))
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("status %d body %q", w.Code, w.Body.String())
	}
	if len(f.store.set) != 1 {
		t.Fatalf("%d SetPassword calls", len(f.store.set))
	}
	set := f.store.set[0]
	if set.ID != 7 || set.MustChange || !set.PasswordHash.Valid {
		t.Fatalf("SetPassword %+v", set)
	}
	if ok, err := VerifyPassword(context.Background(), set.PasswordHash.String, pwNew); err != nil || !ok {
		t.Fatalf("stored hash does not verify the new password: %v, %v", ok, err)
	}
	want := []store.DeleteOtherUserSessionsParams{{UserID: 7, KeepIDHash: []byte("session-hash")}}
	if !reflect.DeepEqual(f.store.deleted, want) {
		t.Fatalf("deleted %+v; want %+v (other sessions only, the current one kept)", f.store.deleted, want)
	}
	wantAudit := []auditEvent{{Actor: "local:root", Action: "user.password_changed", Detail: map[string]any{"username": "root"}}}
	if !reflect.DeepEqual(f.audit.events, wantAudit) {
		t.Fatalf("audit %+v", f.audit.events)
	}
	for _, s := range []string{pwCurrent, pwNew} {
		if strings.Contains(f.log.String(), s) || strings.Contains(w.Body.String(), s) {
			t.Fatal("a password leaked into the log or response")
		}
	}
}

func TestPasswordChangeNoPrincipal(t *testing.T) {
	f := newPWFixture(t, 10)
	w := f.do(http.MethodPost, pwBody(pwCurrent, pwNew), nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
	if code, _ := errorCode(t, w); code != "unauthenticated" {
		t.Fatalf("code %q", code)
	}
}

func TestPasswordChangeMethod(t *testing.T) {
	f := newPWFixture(t, 10)
	w := f.do(http.MethodGet, "", f.p)
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "POST" {
		t.Fatalf("status %d Allow %q", w.Code, w.Header().Get("Allow"))
	}
}

func TestPasswordChangeNotLocal(t *testing.T) {
	f := newPWFixture(t, 10)
	f.p.Source = SourceGitLab
	w := f.post(pwBody(pwCurrent, pwNew))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
	if code, _ := errorCode(t, w); code != "not_local" {
		t.Fatalf("code %q", code)
	}
	// The stored user decides too, not only the session's copy.
	f = newPWFixture(t, 10)
	f.store.user.Source = SourceGitLab
	if w := f.post(pwBody(pwCurrent, pwNew)); w.Code != http.StatusForbidden {
		t.Fatalf("stored non-local user: status %d", w.Code)
	}
	if len(f.store.set) != 0 {
		t.Fatal("password set for a non-local user")
	}
}

func TestPasswordChangeWrongCurrent(t *testing.T) {
	f := newPWFixture(t, 10)
	w := f.post(pwBody("not my password", pwNew))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
	if code, _ := errorCode(t, w); code != "invalid_credentials" {
		t.Fatalf("code %q", code)
	}
	// Account without a password hash, corrupted hash, overlong current password.
	f.store.user.PasswordHash = pgtype.Text{}
	if w := f.post(pwBody(pwCurrent, pwNew)); w.Code != http.StatusUnauthorized {
		t.Fatalf("no hash: status %d", w.Code)
	}
	f.store.user.PasswordHash = pgtype.Text{String: "garbage", Valid: true}
	if w := f.post(pwBody(pwCurrent, pwNew)); w.Code != http.StatusUnauthorized {
		t.Fatalf("corrupted hash: status %d", w.Code)
	}
	if w := f.post(pwBody(strings.Repeat("p", 1025), pwNew)); w.Code != http.StatusUnauthorized {
		t.Fatalf("overlong current: status %d", w.Code)
	}
	if len(f.store.set) != 0 || len(f.store.deleted) != 0 || len(f.audit.events) != 0 {
		t.Fatal("a failed change had effects")
	}
}

func TestPasswordChangeWeakNewPassword(t *testing.T) {
	cases := map[string]string{
		"short":     "short",
		"username":  "ROOTROOTROOT",
		"repeated":  strings.Repeat("z", 20),
		"too long":  strings.Repeat("ab", 513),
		"unchanged": pwCurrent,
	}
	for name, next := range cases {
		t.Run(name, func(t *testing.T) {
			f := newPWFixture(t, 10)
			if name == "username" {
				f.store.user.Username = next
			}
			w := f.post(pwBody(pwCurrent, next))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status %d", w.Code)
			}
			code, msg := errorCode(t, w)
			if code != "weak_password" {
				t.Fatalf("code %q", code)
			}
			if name == "unchanged" && !strings.Contains(msg, "different") {
				t.Fatalf("message %q", msg)
			}
			if len(f.store.set) != 0 || len(f.store.deleted) != 0 || len(f.audit.events) != 0 {
				t.Fatal("a rejected change had effects")
			}
		})
	}
}

func TestPasswordChangeInvalidRequests(t *testing.T) {
	cases := map[string]string{
		"empty":         "",
		"not json":      "x",
		"unknown field": `{"current_password":"a","new_password":"b","x":1}`,
		"missing new":   `{"current_password":"a"}`,
		"missing old":   `{"new_password":"a"}`,
		"empty new":     `{"current_password":"a","new_password":""}`,
		"null":          `{"current_password":null,"new_password":"b"}`,
		"trailing":      pwBody("a", "b") + `{}`,
		"oversized":     `{"current_password":"a","new_password":"` + strings.Repeat("p", 9000) + `"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			f := newPWFixture(t, 10)
			w := f.post(body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status %d", w.Code)
			}
			if code, _ := errorCode(t, w); code != "invalid_request" {
				t.Fatalf("code %q", code)
			}
		})
	}
}

func TestPasswordChangeRateLimitPerUser(t *testing.T) {
	f := newPWFixture(t, 2)
	for range 2 {
		if w := f.post(pwBody("wrong", pwNew)); w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d within the burst", w.Code)
		}
	}
	w := f.post(pwBody(pwCurrent, pwNew))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "12" {
		t.Fatalf("status %d Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
	if code, _ := errorCode(t, w); code != "too_many_attempts" {
		t.Fatalf("code %q", code)
	}
	if len(f.store.set) != 0 {
		t.Fatal("a limited request changed the password")
	}
	other := *f.p
	other.UserID = 8
	if w := f.do(http.MethodPost, pwBody("wrong", pwNew), &other); w.Code != http.StatusUnauthorized {
		t.Fatalf("other user: status %d", w.Code)
	}
	f.clock.Advance(12 * time.Second)
	if w := f.post(pwBody(pwCurrent, pwNew)); w.Code != http.StatusNoContent {
		t.Fatalf("after waiting: status %d", w.Code)
	}
}

func TestPasswordChangeInternalErrors(t *testing.T) {
	boom := errors.New("boom")
	cases := map[string]func(*fakePasswordStore){
		"get":    func(s *fakePasswordStore) { s.getErr = boom },
		"set":    func(s *fakePasswordStore) { s.setErr = boom },
		"delete": func(s *fakePasswordStore) { s.deleteErr = boom },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newPWFixture(t, 10)
			mutate(f.store)
			w := f.post(pwBody(pwCurrent, pwNew))
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status %d", w.Code)
			}
			if code, _ := errorCode(t, w); code != "internal_error" {
				t.Fatalf("code %q", code)
			}
			if len(f.audit.events) != 0 {
				t.Fatal("success audited after a failure")
			}
			if !strings.Contains(f.log.String(), "boom") || strings.Contains(f.log.String(), pwNew) {
				t.Fatalf("log %q", f.log.String())
			}
		})
	}
}

func TestPasswordChangeUserGone(t *testing.T) {
	f := newPWFixture(t, 10)
	f.store.missing = true
	w := f.post(pwBody(pwCurrent, pwNew))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
	if code, _ := errorCode(t, w); code != "unauthenticated" {
		t.Fatalf("code %q", code)
	}
}

func TestPasswordChangeBusy(t *testing.T) {
	f := newPWFixture(t, 10)
	for range cap(hashSlots) {
		hashSlots <- struct{}{}
	}
	defer func() {
		for range cap(hashSlots) {
			<-hashSlots
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := newRequest(http.MethodPost, "/", strings.NewReader(pwBody(pwCurrent, pwNew))).WithContext(WithPrincipal(ctx, f.p))
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || len(f.store.set) != 0 {
		t.Fatalf("status %d", w.Code)
	}
}
