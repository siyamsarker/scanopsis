// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/store"
)

var _ SetupStore = (*store.Queries)(nil)

type fakeSetupStore struct {
	mu       sync.Mutex
	admins   int64
	created  []store.CreateLocalUserParams
	countErr error
	makeErr  error
	touchErr error
	touched  []int64
}

func (f *fakeSetupStore) CountAdmins(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.admins, f.countErr
}

func (f *fakeSetupStore) CreateLocalUser(_ context.Context, arg store.CreateLocalUserParams) (store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.makeErr != nil {
		return store.User{}, f.makeErr
	}
	f.created = append(f.created, arg)
	f.admins++
	return store.User{ID: int64(100 + len(f.created)), Source: SourceLocal, Username: arg.Username, Role: arg.Role}, nil
}

func (f *fakeSetupStore) TouchLastLogin(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.touchErr != nil {
		return f.touchErr
	}
	f.touched = append(f.touched, id)
	return nil
}

// lockedAudit is a fakeAudit safe for concurrent use.
type lockedAudit struct {
	mu sync.Mutex
	fakeAudit
}

func (l *lockedAudit) Record(ctx context.Context, actor, action string, detail map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fakeAudit.Record(ctx, actor, action, detail)
}

// lockedStarter is a fakeStarter safe for concurrent use.
type lockedStarter struct {
	mu sync.Mutex
	fakeStarter
}

func (l *lockedStarter) Start(w http.ResponseWriter, r *http.Request, userID int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fakeStarter.Start(w, r, userID)
}

type setupFixture struct {
	s     *Setup
	h     http.Handler
	store *fakeSetupStore
	start *lockedStarter
	audit *lockedAudit
	clock *fakeClock
	token string
}

func newSetupFixture(t *testing.T, burst int) *setupFixture {
	t.Helper()
	f := &setupFixture{store: &fakeSetupStore{}, start: &lockedStarter{}, audit: &lockedAudit{}, clock: newFakeClock()}
	ip := func(r *http.Request) netip.Addr { return netip.MustParseAddrPort(r.RemoteAddr).Addr() }
	f.s = NewSetup(f.store, f.start, f.audit, ip, NewRateLimiter(12*time.Second, burst, f.clock.Now), nil)
	f.h = f.s.Handler()
	tok, err := f.s.PrepareToken(context.Background())
	if err != nil || tok == "" {
		t.Fatalf("PrepareToken: %q, %v", tok, err)
	}
	f.token = tok
	return f
}

func setupBody(token, user, pass string) string {
	b, _ := json.Marshal(map[string]string{"token": token, "username": user, "password": pass})
	return string(b)
}

func (f *setupFixture) do(method, body, remote string) *httptest.ResponseRecorder {
	r := newRequest(method, "/api/v1/setup", strings.NewReader(body))
	if remote == "" {
		remote = "192.0.2.1:5555"
	}
	r.RemoteAddr = remote
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

const goodSetupPass = "a long enough passphrase"

func TestSetupPrepareToken(t *testing.T) {
	f := newSetupFixture(t, 5)
	if len(f.token) != 43 { // 32 bytes, unpadded base64url
		t.Fatalf("token %q has length %d; want 43", f.token, len(f.token))
	}
	if _, err := f.s.PrepareToken(context.Background()); err == nil {
		t.Fatal("a second token was issued by the same process")
	}
	if f.s.tokenHash == [32]byte{} {
		t.Fatal("token hash not kept")
	}

	// No token once an administrator exists.
	s2 := NewSetup(&fakeSetupStore{admins: 1}, &f.start.fakeStarter, f.audit, nil, nil, nil)
	if tok, err := s2.PrepareToken(context.Background()); tok != "" || err != nil {
		t.Fatalf("PrepareToken with an admin: %q, %v", tok, err)
	}
	boom := errors.New("boom")
	s3 := NewSetup(&fakeSetupStore{countErr: boom}, &f.start.fakeStarter, f.audit, nil, nil, nil)
	if _, err := s3.PrepareToken(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("error %v; want boom", err)
	}
	// Two processes never share a token.
	s4 := NewSetup(&fakeSetupStore{}, &f.start.fakeStarter, f.audit, nil, nil, nil)
	if tok, _ := s4.PrepareToken(context.Background()); tok == "" || tok == f.token {
		t.Fatalf("token %q", tok)
	}
}

func TestSetupStatus(t *testing.T) {
	f := newSetupFixture(t, 5)
	get := func() (bool, *httptest.ResponseRecorder) {
		w := f.do(http.MethodGet, "", "")
		var body map[string]bool
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || len(body) != 1 {
			t.Fatalf("body %q: %v", w.Body.String(), err)
		}
		return body["required"], w
	}
	if req, w := get(); !req || w.Code != 200 || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("required %v status %d headers %v", req, w.Code, w.Header())
	}
	f.store.admins = 1
	if req, _ := get(); req {
		t.Fatal("required although an administrator exists")
	}
	f.store.countErr = errors.New("db down")
	if w := f.do(http.MethodGet, "", ""); w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", w.Code)
	}
}

func TestSetupMethodNotAllowed(t *testing.T) {
	f := newSetupFixture(t, 5)
	for _, m := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch} {
		w := f.do(m, "", "")
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET, POST" {
			t.Errorf("%s: status %d Allow %q", m, w.Code, w.Header().Get("Allow"))
		}
	}
}

func TestSetupSuccess(t *testing.T) {
	f := newSetupFixture(t, 5)
	w := f.do(http.MethodPost, setupBody(f.token, "root", goodSetupPass), "")
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("status %d body %q", w.Code, w.Body.String())
	}
	if len(f.store.created) != 1 {
		t.Fatalf("%d users created", len(f.store.created))
	}
	c := f.store.created[0]
	if c.Username != "root" || c.Role != store.RoleAdmin || !c.PasswordHash.Valid || strings.Contains(c.PasswordHash.String, goodSetupPass) {
		t.Fatalf("created %+v", c)
	}
	if ok, err := VerifyPassword(context.Background(), c.PasswordHash.String, goodSetupPass); err != nil || !ok {
		t.Fatalf("stored hash does not verify: %v, %v", ok, err)
	}
	if !reflect.DeepEqual(f.start.started, []int64{101}) || len(w.Result().Cookies()) != 1 {
		t.Fatalf("started %v cookies %d", f.start.started, len(w.Result().Cookies()))
	}
	// Setup signs the administrator in: that is their first sign-in.
	if !reflect.DeepEqual(f.store.touched, []int64{101}) {
		t.Fatalf("sign-in time recorded for %v; want [101]", f.store.touched)
	}
	want := []auditEvent{{Actor: "local:root", Action: "setup.completed",
		Detail: map[string]any{"username": "root", "ip": "192.0.2.1"}}}
	if !reflect.DeepEqual(f.audit.events, want) {
		t.Fatalf("audit %+v; want %+v", f.audit.events, want)
	}

	// The token is spent: a second attempt with it is a conflict, and
	// the token stays dead even if the administrator disappears.
	w = f.do(http.MethodPost, setupBody(f.token, "again", goodSetupPass), "")
	if w.Code != http.StatusConflict {
		t.Fatalf("second setup: status %d; want 409", w.Code)
	}
	if code, _ := errorCode(t, w); code != "setup_complete" {
		t.Fatalf("code %q", code)
	}
	f.store.admins = 0
	if w := f.do(http.MethodPost, setupBody(f.token, "again", goodSetupPass), ""); w.Code != http.StatusForbidden {
		t.Fatalf("spent token: status %d; want 403", w.Code)
	}
	if len(f.store.created) != 1 {
		t.Fatalf("%d users created", len(f.store.created))
	}
}

func TestSetupWrongToken(t *testing.T) {
	f := newSetupFixture(t, 50)
	for name, tok := range map[string]string{
		"wrong":     "nope",
		"empty":     "",
		"prefix":    f.token[:42],
		"extended":  f.token + "x",
		"uppercase": strings.ToUpper(f.token),
	} {
		w := f.do(http.MethodPost, setupBody(tok, "root", goodSetupPass), "")
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: status %d; want 403", name, w.Code)
			continue
		}
		if code, _ := errorCode(t, w); code != "invalid_setup_token" {
			t.Errorf("%s: code %q", name, code)
		}
	}
	if len(f.store.created) != 0 || len(f.start.started) != 0 || len(f.audit.events) != 0 {
		t.Fatal("a wrong token created an administrator")
	}
	// A server without a token (setup was never prepared) refuses everything.
	s := NewSetup(&fakeSetupStore{}, &f.start.fakeStarter, f.audit, func(*http.Request) netip.Addr { return netip.MustParseAddr("192.0.2.1") },
		NewRateLimiter(time.Second, 5, nil), nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, newRequest(http.MethodPost, "/", strings.NewReader(setupBody("", "root", goodSetupPass))))
	if w.Code != http.StatusForbidden {
		t.Fatalf("no token issued: status %d; want 403", w.Code)
	}
}

func TestSetupConflictWhenAdminExists(t *testing.T) {
	f := newSetupFixture(t, 5)
	f.store.admins = 1
	w := f.do(http.MethodPost, setupBody(f.token, "root", goodSetupPass), "")
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d; want 409", w.Code)
	}
	// Even a wrong token only learns that setup is over.
	if w := f.do(http.MethodPost, setupBody("wrong", "root", goodSetupPass), ""); w.Code != http.StatusConflict {
		t.Fatalf("status %d; want 409", w.Code)
	}
	if len(f.store.created) != 0 {
		t.Fatal("administrator created")
	}
}

func TestSetupConcurrentPostsCreateOneAdmin(t *testing.T) {
	f := newSetupFixture(t, 100)
	const n = 6
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- f.do(http.MethodPost, setupBody(f.token, "root", goodSetupPass), "").Code
		}()
	}
	wg.Wait()
	close(codes)
	got := map[int]int{}
	for c := range codes {
		got[c]++
	}
	if got[http.StatusNoContent] != 1 || got[http.StatusConflict] != n-1 {
		t.Fatalf("status counts %v; want one 204 and %d 409", got, n-1)
	}
	if len(f.store.created) != 1 {
		t.Fatalf("%d administrators created", len(f.store.created))
	}
}

func TestSetupWeakPasswords(t *testing.T) {
	f := newSetupFixture(t, 50)
	for name, pass := range map[string]string{
		"short":    "short",
		"username": "rootrootroot",
		"repeated": strings.Repeat("x", 30),
		"too long": strings.Repeat("ab", 513),
	} {
		user := "root"
		if name == "username" {
			user = pass
		}
		w := f.do(http.MethodPost, setupBody(f.token, user, pass), "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d; want 400", name, w.Code)
			continue
		}
		code, msg := errorCode(t, w)
		if code != "weak_password" || msg != CheckPassword(pass, user).Error() {
			t.Errorf("%s: code %q message %q", name, code, msg)
		}
	}
	if len(f.store.created) != 0 {
		t.Fatal("a weak password created an administrator")
	}
	// The token survives rejected attempts.
	if w := f.do(http.MethodPost, setupBody(f.token, "root", goodSetupPass), ""); w.Code != http.StatusNoContent {
		t.Fatalf("status %d after weak attempts", w.Code)
	}
}

func TestSetupInvalidRequests(t *testing.T) {
	cases := map[string]string{
		"empty body":       "",
		"not json":         "token=x",
		"null":             "null",
		"unknown field":    `{"token":"x","username":"a","password":"b","extra":1}`,
		"missing token":    `{"username":"a","password":"b"}`,
		"missing username": `{"token":"x","password":"b"}`,
		"missing password": `{"token":"x","username":"a"}`,
		"null token":       `{"token":null,"username":"a","password":"b"}`,
		"numeric username": `{"token":"x","username":1,"password":"b"}`,
		"trailing data":    setupBody("x", "a", "b") + ` {}`,
		"oversized":        `{"token":"x","username":"a","password":"` + strings.Repeat("p", 9000) + `"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSetupFixture(t, 5)
			w := f.do(http.MethodPost, body, "")
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status %d; want 400", w.Code)
			}
			if code, _ := errorCode(t, w); code != "invalid_request" {
				t.Fatalf("code %q", code)
			}
			if len(f.store.created) != 0 {
				t.Fatal("administrator created")
			}
		})
	}
}

func TestSetupUsernameRules(t *testing.T) {
	cases := map[string]bool{
		"root":                   true,
		"Root User":              true,
		"ünïcode":                true,
		strings.Repeat("u", 200): true,
		strings.Repeat("u", 201): false,
		"":                       false,
		"a\x00b":                 false,
		"a\nb":                   false,
		"tab\there":              false,
		"del\x7f":                false,
		"c1\u0085control":        false,
		strings.Repeat("é", 100): true, // 200 bytes
		strings.Repeat("é", 101): false,
	}
	for name, ok := range cases {
		f := newSetupFixture(t, 5)
		w := f.do(http.MethodPost, setupBody(f.token, name, goodSetupPass), "")
		if ok != (w.Code == http.StatusNoContent) {
			t.Errorf("username %q: status %d; accepted should be %v", name, w.Code, ok)
		}
		if !ok && w.Code == http.StatusBadRequest {
			if code, _ := errorCode(t, w); code != "invalid_request" {
				t.Errorf("username %q: code %q", name, code)
			}
		}
	}
}

func TestSetupRateLimit(t *testing.T) {
	f := newSetupFixture(t, 2)
	for range 2 {
		if w := f.do(http.MethodPost, setupBody("wrong", "root", goodSetupPass), ""); w.Code != http.StatusForbidden {
			t.Fatalf("status %d within the burst", w.Code)
		}
	}
	// Limited before the token is looked at, even with the right one.
	w := f.do(http.MethodPost, setupBody(f.token, "root", goodSetupPass), "")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "12" {
		t.Fatalf("status %d Retry-After %q; want 429 and 12", w.Code, w.Header().Get("Retry-After"))
	}
	if code, _ := errorCode(t, w); code != "too_many_attempts" {
		t.Fatalf("code %q", code)
	}
	if len(f.store.created) != 0 {
		t.Fatal("a limited request created an administrator")
	}
	// GET is not limited, another address has its own budget, time heals.
	if w := f.do(http.MethodGet, "", ""); w.Code != http.StatusOK {
		t.Fatalf("GET status %d", w.Code)
	}
	if w := f.do(http.MethodPost, setupBody("wrong", "root", goodSetupPass), "198.51.100.7:1"); w.Code != http.StatusForbidden {
		t.Fatalf("other address: status %d", w.Code)
	}
	f.clock.Advance(12 * time.Second)
	if w := f.do(http.MethodPost, setupBody(f.token, "root", goodSetupPass), ""); w.Code != http.StatusNoContent {
		t.Fatalf("after waiting: status %d", w.Code)
	}
}

func TestSetupInternalErrors(t *testing.T) {
	boom := errors.New("boom")
	t.Run("count", func(t *testing.T) {
		f := newSetupFixture(t, 5)
		f.store.countErr = boom
		if w := f.do(http.MethodPost, setupBody(f.token, "root", goodSetupPass), ""); w.Code != http.StatusInternalServerError {
			t.Fatalf("status %d", w.Code)
		}
	})
	t.Run("create", func(t *testing.T) {
		f := newSetupFixture(t, 5)
		f.store.makeErr = boom
		w := f.do(http.MethodPost, setupBody(f.token, "root", goodSetupPass), "")
		if w.Code != http.StatusInternalServerError || len(f.audit.events) != 0 || len(f.start.started) != 0 {
			t.Fatalf("status %d audit %v started %v", w.Code, f.audit.events, f.start.started)
		}
		// The token still works once the database recovers.
		f.store.makeErr = nil
		if w := f.do(http.MethodPost, setupBody(f.token, "root", goodSetupPass), ""); w.Code != http.StatusNoContent {
			t.Fatalf("retry: status %d", w.Code)
		}
	})
	t.Run("session", func(t *testing.T) {
		f := newSetupFixture(t, 5)
		f.start.err = boom
		w := f.do(http.MethodPost, setupBody(f.token, "root", goodSetupPass), "")
		if w.Code != http.StatusInternalServerError || len(w.Result().Cookies()) != 0 {
			t.Fatalf("status %d", w.Code)
		}
		// The administrator exists, so setup is over.
		if w := f.do(http.MethodPost, setupBody(f.token, "root", goodSetupPass), ""); w.Code != http.StatusConflict {
			t.Fatalf("status %d; want 409", w.Code)
		}
		if len(f.store.touched) != 0 {
			t.Fatalf("sign-in time recorded without a session: %v", f.store.touched)
		}
	})
	t.Run("sign-in time", func(t *testing.T) {
		// Not recording the time does not undo a setup that signed the user in.
		f := newSetupFixture(t, 5)
		f.store.touchErr = boom
		w := f.do(http.MethodPost, setupBody(f.token, "root", goodSetupPass), "")
		if w.Code != http.StatusNoContent || len(w.Result().Cookies()) != 1 {
			t.Fatalf("status %d cookies %d", w.Code, len(w.Result().Cookies()))
		}
	})
	t.Run("busy", func(t *testing.T) {
		f := newSetupFixture(t, 5)
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
		r := newRequest(http.MethodPost, "/", strings.NewReader(setupBody(f.token, "root", goodSetupPass))).WithContext(ctx)
		r.RemoteAddr = "192.0.2.1:1"
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, r)
		if w.Code != http.StatusServiceUnavailable || len(f.store.created) != 0 {
			t.Fatalf("status %d", w.Code)
		}
	})
}

func TestSetupCheckToken(t *testing.T) {
	f := newSetupFixture(t, 4)
	ctx := context.Background()
	if err := f.s.CheckToken(ctx, f.token); err != nil {
		t.Fatalf("valid token: %v", err)
	}
	// Checking does not spend the token.
	if err := f.s.CheckToken(ctx, f.token); err != nil {
		t.Fatalf("valid token again: %v", err)
	}
	if err := f.s.CheckToken(ctx, "wrong"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("wrong token: %v", err)
	}
	if err := f.s.CheckToken(ctx, ""); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("empty token: %v", err)
	}
	// The limiter is the setup limiter: attempts run out.
	if err := f.s.CheckToken(ctx, f.token); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("over the limit: %v", err)
	}
	f.clock.Advance(time.Minute)
	f.store.admins = 1
	if err := f.s.CheckToken(ctx, f.token); !errors.Is(err, ErrSetupDone) {
		t.Fatalf("after an administrator exists: %v", err)
	}
}

func TestSetupCheckTokenWithoutToken(t *testing.T) {
	s := NewSetup(&fakeSetupStore{}, &fakeStarter{}, &fakeAudit{}, nil, NewRateLimiter(time.Second, 5, nil), nil)
	if err := s.CheckToken(context.Background(), "anything"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("no token issued: %v", err)
	}
}

func TestSetupPrepareTokenAgainAfterRestore(t *testing.T) {
	f := newSetupFixture(t, 5)
	ctx := context.Background()
	// The database was replaced by one with an administrator: the old token
	// is dropped, no new one is issued.
	f.store.admins = 1
	if tok, err := f.s.PrepareToken(ctx); tok != "" || err != nil {
		t.Fatalf("PrepareToken with an admin: %q, %v", tok, err)
	}
	f.store.admins = 0
	if err := f.s.CheckToken(ctx, f.token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("old token still accepted: %v", err)
	}
	if tok, err := f.s.PrepareToken(ctx); tok == "" || tok == f.token || err != nil {
		t.Fatalf("new token %q, %v", tok, err)
	}
}
