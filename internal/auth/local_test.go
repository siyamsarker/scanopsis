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
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/store"
)

var (
	_ LocalUserStore = (*store.Queries)(nil)
	_ SessionStarter = (*Sessions)(nil)
)

type fakeUsers struct {
	users     map[string]store.User
	lookups   []string
	touched   []int64
	lookupErr error
	touchErr  error
}

func (f *fakeUsers) GetLocalUserByUsername(_ context.Context, username string) (store.User, error) {
	f.lookups = append(f.lookups, username)
	if f.lookupErr != nil {
		return store.User{}, f.lookupErr
	}
	u, ok := f.users[username]
	if !ok {
		return store.User{}, pgx.ErrNoRows
	}
	return u, nil
}

func (f *fakeUsers) TouchLastLogin(_ context.Context, id int64) error {
	f.touched = append(f.touched, id)
	return f.touchErr
}

type fakeStarter struct {
	started []int64
	err     error
}

func (f *fakeStarter) Start(w http.ResponseWriter, _ *http.Request, userID int64) error {
	if f.err != nil {
		return f.err
	}
	f.started = append(f.started, userID)
	http.SetCookie(w, &http.Cookie{Name: "scanopsis_session", Value: "tok"})
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

type verifyCall struct{ hash, password string }

// loginFixture wires a handler to fakes. verifyPassword is wrapped to record
// its calls; a stored hash is cheap to compute, the dummy hash is the real one.
type loginFixture struct {
	h      http.Handler
	users  *fakeUsers
	start  *fakeStarter
	audit  *fakeAudit
	calls  []verifyCall
	limit  *RateLimiter
	byUser *RateLimiter
	clock  *fakeClock
	userID int64
}

const (
	goodUser = "admin"
	goodPass = "s3cret-pass"
)

func newLoginFixture(t *testing.T, every time.Duration, burst int) *loginFixture {
	t.Helper()
	f := &loginFixture{
		users:  &fakeUsers{},
		start:  &fakeStarter{},
		audit:  &fakeAudit{},
		clock:  newFakeClock(),
		userID: 42,
	}
	f.users.users = map[string]store.User{
		goodUser: {ID: f.userID, Source: SourceLocal, Username: goodUser, Role: store.RoleAdmin,
			PasswordHash: pgtype.Text{String: cheapHash(goodPass, 8, 1, 1), Valid: true}},
		"nohash":    {ID: 43, Source: SourceLocal, Username: "nohash", Role: store.RoleViewer},
		"corrupted": {ID: 44, Source: SourceLocal, Username: "corrupted", PasswordHash: pgtype.Text{String: "garbage", Valid: true}},
	}
	f.limit = NewRateLimiter(every, burst, f.clock.Now)
	ip := func(r *http.Request) netip.Addr { return netip.MustParseAddrPort(r.RemoteAddr).Addr() }
	f.byUser = NewRateLimiter(every, burst, f.clock.Now)
	f.h = NewLocalLogin(f.users, f.start, f.audit, ip, f.limit, f.byUser, nil)

	orig := verifyPassword
	verifyPassword = func(ctx context.Context, encoded, password string) (bool, error) {
		f.calls = append(f.calls, verifyCall{encoded, password})
		return orig(ctx, encoded, password)
	}
	t.Cleanup(func() { verifyPassword = orig })
	return f
}

func (f *loginFixture) do(method, body, remote string) *httptest.ResponseRecorder {
	r := newRequest(method, "/api/v1/auth/local/login", strings.NewReader(body))
	if remote != "" {
		r.RemoteAddr = remote
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

func (f *loginFixture) post(body string) *httptest.ResponseRecorder {
	return f.do(http.MethodPost, body, "192.0.2.1:5555")
}

func creds(user, pass string) string {
	b, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	return string(b)
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) (code, message string) {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type %q; want application/json", ct)
	}
	var body struct {
		Error struct{ Code, Message string }
	}
	dec := json.NewDecoder(strings.NewReader(w.Body.String()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		t.Fatalf("body %q is not the Error shape: %v", w.Body.String(), err)
	}
	if body.Error.Code == "" || body.Error.Message == "" {
		t.Fatalf("body %q lacks code or message", w.Body.String())
	}
	return body.Error.Code, body.Error.Message
}

func TestLocalLoginSuccess(t *testing.T) {
	f := newLoginFixture(t, 12*time.Second, 10)
	w := f.post(creds(goodUser, goodPass))
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("status %d body %q; want 204 and no body", w.Code, w.Body.String())
	}
	if !reflect.DeepEqual(f.users.touched, []int64{42}) || !reflect.DeepEqual(f.start.started, []int64{42}) {
		t.Fatalf("touched %v started %v; want [42] each", f.users.touched, f.start.started)
	}
	if len(w.Result().Cookies()) != 1 {
		t.Fatal("the session cookie was not passed through to the response")
	}
	want := []auditEvent{{
		Actor: "local:admin", Action: "login.succeeded",
		Detail: map[string]any{"method": "local", "username": "admin", "ip": "192.0.2.1"},
	}}
	if !reflect.DeepEqual(f.audit.events, want) {
		t.Fatalf("audit %+v; want %+v", f.audit.events, want)
	}
	if len(f.calls) != 1 || f.calls[0].password != goodPass {
		t.Fatalf("verify calls %+v", f.calls)
	}
}

func TestLocalLoginMethodNotAllowed(t *testing.T) {
	f := newLoginFixture(t, 12*time.Second, 10)
	for _, m := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodHead} {
		w := f.do(m, creds(goodUser, goodPass), "")
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "POST" {
			t.Errorf("%s: status %d Allow %q", m, w.Code, w.Header().Get("Allow"))
		}
	}
	if len(f.users.lookups) != 0 || len(f.start.started) != 0 || len(f.calls) != 0 {
		t.Fatal("a non-POST request reached the credential check")
	}
	w := f.do(http.MethodGet, "", "")
	if code, _ := errorCode(t, w); code != "method_not_allowed" {
		t.Errorf("code %q", code)
	}
}

func TestLocalLoginWrongPassword(t *testing.T) {
	f := newLoginFixture(t, 12*time.Second, 10)
	w := f.post(creds(goodUser, "wrong"))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
	code, msg := errorCode(t, w)
	if code != "invalid_credentials" || msg != "Wrong username or password." {
		t.Fatalf("code %q message %q", code, msg)
	}
	if len(f.start.started) != 0 || len(f.users.touched) != 0 || len(w.Result().Cookies()) != 0 {
		t.Fatal("a failed login started a session or touched the user")
	}
	if len(f.audit.events) != 1 || f.audit.events[0].Actor != "local:admin" || f.audit.events[0].Action != "login.failed" ||
		!reflect.DeepEqual(f.audit.events[0].Detail, map[string]any{"method": "local", "username": "admin", "ip": "192.0.2.1"}) {
		t.Fatalf("audit %+v", f.audit.events)
	}
}

func TestLocalLoginUnknownUserDoesTheSameWork(t *testing.T) {
	f := newLoginFixture(t, 12*time.Second, 10)
	wrong := f.post(creds(goodUser, "wrong"))
	if len(f.calls) != 1 {
		t.Fatalf("known user: %d verifications; want 1", len(f.calls))
	}
	f.calls = nil

	unknown := f.post(creds("nobody", "whatever"))
	if unknown.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", unknown.Code)
	}
	if len(f.calls) != 1 || f.calls[0].hash != dummyHash || f.calls[0].password != "whatever" {
		t.Fatalf("unknown user: verify calls %+v; want one against the dummy hash", f.calls)
	}
	if unknown.Body.String() != wrong.Body.String() {
		t.Fatalf("responses differ:\n%q\n%q", unknown.Body.String(), wrong.Body.String())
	}
	if len(f.start.started) != 0 || len(f.users.touched) != 0 {
		t.Fatal("unknown user got a session")
	}
	if last := f.audit.events[len(f.audit.events)-1]; last.Action != "login.failed" || last.Detail["username"] != "nobody" {
		t.Fatalf("audit %+v", last)
	}
}

func TestLocalLoginUnusableStoredHash(t *testing.T) {
	f := newLoginFixture(t, 12*time.Second, 10)
	for _, name := range []string{"nohash", "corrupted"} {
		f.calls = nil
		w := f.post(creds(name, "anything"))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d", name, w.Code)
		}
		if len(f.calls) != 1 {
			t.Errorf("%s: %d verifications; want 1", name, len(f.calls))
		}
	}
	if len(f.start.started) != 0 {
		t.Fatal("a session was started")
	}
	if f.calls[0].hash != "garbage" {
		t.Errorf("corrupted hash was not the one verified: %+v", f.calls)
	}
}

func TestLocalLoginInvalidRequests(t *testing.T) {
	long := strings.Repeat("a", 201)
	cases := map[string]string{
		"empty body":         "",
		"not json":           "username=admin&password=x",
		"truncated json":     `{"username":"a"`,
		"null":               "null",
		"array":              `[1]`,
		"unknown field":      `{"username":"a","password":"b","remember":true}`,
		"missing username":   `{"password":"b"}`,
		"missing password":   `{"username":"a"}`,
		"empty username":     `{"username":"","password":"b"}`,
		"empty password":     `{"username":"a","password":""}`,
		"null password":      `{"username":"a","password":null}`,
		"numeric password":   `{"username":"a","password":123}`,
		"username too long":  creds(long, "b"),
		"password too long":  creds("a", strings.Repeat("p", 1025)),
		"username with NUL":  `{"username":"a\u0000b","password":"b"}`,
		"trailing data":      creds("a", "b") + ` {}`,
		"trailing garbage":   creds("a", "b") + `x`,
		"oversized body":     `{"username":"a","password":"b","pad":"` + strings.Repeat("x", 9000) + `"}`,
		"oversized password": creds("a", strings.Repeat("p", 9000)),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			f := newLoginFixture(t, 12*time.Second, 100)
			w := f.post(body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status %d; want 400", w.Code)
			}
			if code, _ := errorCode(t, w); code != "invalid_request" {
				t.Fatalf("code %q", code)
			}
			if len(f.users.lookups) != 0 || len(f.calls) != 0 || len(f.audit.events) != 0 {
				t.Fatal("an invalid request reached the credential check or the audit log")
			}
		})
	}
}

func TestLocalLoginLengthLimitsAreInclusive(t *testing.T) {
	f := newLoginFixture(t, 12*time.Second, 100)
	w := f.post(creds(strings.Repeat("u", 200), strings.Repeat("p", 1024)))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d; limits of 200 and 1024 bytes must be accepted", w.Code)
	}
}

func TestLocalLoginRateLimit(t *testing.T) {
	f := newLoginFixture(t, 12*time.Second, 2)
	for range 2 {
		if w := f.post(creds(goodUser, "wrong")); w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d within the burst", w.Code)
		}
	}
	f.calls = nil
	w := f.post(creds(goodUser, goodPass)) // would succeed, but is limited
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d; want 429", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "12" {
		t.Fatalf("Retry-After %q; want 12", got)
	}
	if code, _ := errorCode(t, w); code != "too_many_attempts" {
		t.Fatalf("code %q", code)
	}
	if len(f.calls) != 0 || len(f.users.lookups) != 2 || len(f.start.started) != 0 {
		t.Fatal("a limited request was hashed, looked up or logged in")
	}
	// The limit applies before the body is looked at.
	if w := f.post("garbage"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d for a limited invalid request", w.Code)
	}
	// Another address has its own budget.
	if w := f.do(http.MethodPost, creds("other", "x"), "198.51.100.7:1234"); w.Code != http.StatusUnauthorized {
		t.Fatalf("other address: status %d", w.Code)
	}
	// The same address recovers after the advertised time.
	f.clock.Advance(12 * time.Second)
	if w := f.post(creds(goodUser, goodPass)); w.Code != http.StatusNoContent {
		t.Fatalf("after waiting: status %d", w.Code)
	}
}

func TestLocalLoginRetryAfterRoundsUp(t *testing.T) {
	f := newLoginFixture(t, 1500*time.Millisecond, 1)
	f.post(creds(goodUser, "wrong"))
	w := f.post(creds(goodUser, "wrong"))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("status %d Retry-After %q; want 429 and 2", w.Code, w.Header().Get("Retry-After"))
	}
	f.clock.Advance(1499*time.Millisecond + 500*time.Microsecond)
	w = f.post(creds(goodUser, "wrong"))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("a sub-second wait must still be advertised as 1s: status %d Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
}

func TestLocalLoginInternalErrors(t *testing.T) {
	boom := errors.New("boom")
	t.Run("lookup", func(t *testing.T) {
		f := newLoginFixture(t, 12*time.Second, 10)
		f.users.lookupErr = boom
		w := f.post(creds(goodUser, goodPass))
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status %d", w.Code)
		}
		if code, _ := errorCode(t, w); code != "internal_error" {
			t.Fatalf("code %q", code)
		}
		if len(f.calls) != 0 || len(f.start.started) != 0 {
			t.Fatal("login went on after a lookup failure")
		}
	})
	t.Run("touch", func(t *testing.T) {
		f := newLoginFixture(t, 12*time.Second, 10)
		f.users.touchErr = boom
		if w := f.post(creds(goodUser, goodPass)); w.Code != http.StatusInternalServerError {
			t.Fatalf("status %d", w.Code)
		}
		if len(f.start.started) != 0 || len(f.audit.events) != 0 {
			t.Fatal("session started or success audited after a failure")
		}
	})
	t.Run("session", func(t *testing.T) {
		f := newLoginFixture(t, 12*time.Second, 10)
		f.start.err = boom
		w := f.post(creds(goodUser, goodPass))
		if w.Code != http.StatusInternalServerError || len(w.Result().Cookies()) != 0 {
			t.Fatalf("status %d cookies %d", w.Code, len(w.Result().Cookies()))
		}
		if len(f.audit.events) != 0 {
			t.Fatalf("success audited although no session exists: %+v", f.audit.events)
		}
	})
}

func TestLocalLoginNeverRecordsThePassword(t *testing.T) {
	f := newLoginFixture(t, 12*time.Second, 100)
	const secret = "Tr0ub4dor&3-unique-marker"
	responses := []*httptest.ResponseRecorder{
		f.post(creds(goodUser, secret)),
		f.post(creds("nobody", secret)),
		f.post(creds("nohash", secret)),
		f.post(creds(strings.Repeat("u", 201), secret)),
	}
	f.users.users[goodUser] = store.User{ID: 42, Username: goodUser,
		PasswordHash: pgtype.Text{String: cheapHash(secret, 8, 1, 1), Valid: true}}
	responses = append(responses, f.post(creds(goodUser, secret)))
	if len(f.audit.events) != 4 {
		t.Fatalf("%d audit events; want 4", len(f.audit.events))
	}
	events, _ := json.Marshal(f.audit.events)
	if strings.Contains(string(events), secret) {
		t.Fatalf("password found in audit events: %s", events)
	}
	for i, w := range responses {
		if strings.Contains(w.Body.String(), secret) || strings.Contains(w.Header().Get("Set-Cookie"), secret) {
			t.Errorf("response %d contains the password", i)
		}
	}
}

func TestLocalLoginLogsServerErrors(t *testing.T) {
	f := newLoginFixture(t, 12*time.Second, 10)
	var buf bytes.Buffer
	f.users.lookupErr = errors.New("database is down")
	f.h = NewLocalLogin(f.users, f.start, f.audit, func(*http.Request) netip.Addr { return netip.MustParseAddr("192.0.2.1") },
		f.limit, f.byUser, slog.New(slog.NewTextHandler(&buf, nil)))
	if w := f.post(creds(goodUser, goodPass)); w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", w.Code)
	}
	if !strings.Contains(buf.String(), "cannot look up user") || !strings.Contains(buf.String(), "database is down") {
		t.Fatalf("server error not logged: %q", buf.String())
	}
	if strings.Contains(buf.String(), goodPass) {
		t.Fatal("password written to the log")
	}
}

func TestLocalLoginAuditsUnknownUserUnderGivenName(t *testing.T) {
	f := newLoginFixture(t, 12*time.Second, 10)
	f.post(creds("Ghost", "x"))
	if len(f.audit.events) != 1 || f.audit.events[0].Actor != "local:Ghost" {
		t.Fatalf("audit %+v; want actor local:Ghost", f.audit.events)
	}
}

func TestLocalLoginRateLimitPerUsernameChargesOnlyFailures(t *testing.T) {
	f := newLoginFixture(t, 12*time.Second, 2)
	addr := func(i int) string { return "198.51.100." + string(rune('0'+i)) + ":1000" }
	for i := range 2 {
		if w := f.do(http.MethodPost, creds(goodUser, "wrong"), addr(i)); w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d within the burst", w.Code)
		}
	}
	// The account's budget of failures is spent: further wrong guesses from
	// any address get 429 ...
	w := f.do(http.MethodPost, creds(goodUser, "wrong"), addr(3))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "12" {
		t.Fatalf("status %d Retry-After %q; want 429 and 12", w.Code, w.Header().Get("Retry-After"))
	}
	if code, _ := errorCode(t, w); code != "too_many_attempts" {
		t.Fatalf("code %q", code)
	}
	if len(f.start.started) != 0 {
		t.Fatal("a wrong password started a session")
	}
	// ... but the correct password is never refused, so nobody can lock the
	// real user out from outside.
	if w := f.do(http.MethodPost, creds(goodUser, goodPass), addr(4)); w.Code != http.StatusNoContent {
		t.Fatalf("correct password after the bucket was exhausted: status %d; want 204", w.Code)
	}
	if !reflect.DeepEqual(f.start.started, []int64{42}) {
		t.Fatalf("started %v", f.start.started)
	}
	// The bucket is keyed by the exact username: another spelling is another key.
	if w := f.do(http.MethodPost, creds("ADMIN", "wrong"), addr(5)); w.Code != http.StatusUnauthorized {
		t.Fatalf("other spelling: status %d; want 401", w.Code)
	}
	if w := f.do(http.MethodPost, creds("other", "x"), addr(6)); w.Code != http.StatusUnauthorized {
		t.Fatalf("other user: status %d", w.Code)
	}
}

func TestLocalLoginSuccessDoesNotSpendTheUsernameBudget(t *testing.T) {
	f := newLoginFixture(t, time.Hour, 1)
	for i := range 3 {
		addr := "198.51.100." + string(rune('1'+i)) + ":1000"
		if w := f.do(http.MethodPost, creds(goodUser, goodPass), addr); w.Code != http.StatusNoContent {
			t.Fatalf("sign-in %d: status %d", i, w.Code)
		}
	}
	// The single failure token is still there.
	if w := f.do(http.MethodPost, creds(goodUser, "wrong"), "198.51.100.9:1000"); w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d; want 401", w.Code)
	}
}

func TestLocalLoginIPv6SharesTheSlash64(t *testing.T) {
	f := newLoginFixture(t, 12*time.Second, 1)
	if w := f.do(http.MethodPost, creds("a", "x"), "[2001:db8:1:2::1]:1000"); w.Code != http.StatusUnauthorized {
		t.Fatalf("first: status %d", w.Code)
	}
	if w := f.do(http.MethodPost, creds("b", "x"), "[2001:db8:1:2:ffff::9]:1000"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("same /64: status %d; want 429", w.Code)
	}
	if w := f.do(http.MethodPost, creds("c", "x"), "[2001:db8:1:3::1]:1000"); w.Code != http.StatusUnauthorized {
		t.Fatalf("other /64: status %d", w.Code)
	}
}

func TestLocalLoginCancelledWhileWaitingForASlot(t *testing.T) {
	f := newLoginFixture(t, 12*time.Second, 10)
	for range cap(hashSlots) {
		hashSlots <- struct{}{}
	}
	t.Cleanup(func() {
		for range cap(hashSlots) {
			<-hashSlots
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := newRequest(http.MethodPost, "/api/v1/auth/local/login", strings.NewReader(creds(goodUser, "wrong"))).WithContext(ctx)
	r.RemoteAddr = "192.0.2.1:5555"
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d; want 503", w.Code)
	}
	if code, msg := errorCode(t, w); code != "busy" || msg != "The server is busy. Try again in a moment." {
		t.Fatalf("code %q message %q", code, msg)
	}
	if len(f.audit.events) != 0 || len(f.start.started) != 0 {
		t.Fatalf("a busy response was audited or logged in: %+v", f.audit.events)
	}
}
