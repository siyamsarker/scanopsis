// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/store"
)

var _ SessionStore = (*store.Queries)(nil)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newRequest(method, target string, body io.Reader) *http.Request {
	return httptest.NewRequestWithContext(context.Background(), method, target, body)
}

// fakeSessionStore mirrors the semantics of the SQL queries in memory.
type fakeSessionStore struct {
	sessions map[string]store.Session // keyed by string(id_hash)
	users    map[int64]store.User
	touches  []store.TouchSessionParams
	deleted  [][]byte
	getErr   error
	delErr   error
	createFn error
	markErr  error
	now      func() time.Time
	nextID   int64
}

func newFakeSessionStore() *fakeSessionStore {
	return &fakeSessionStore{
		sessions: map[string]store.Session{},
		users: map[int64]store.User{
			7: {ID: 7, Source: SourceGitLab, Username: "ada", Name: "Ada L", Email: "ada@example.com", Role: store.RoleAdmin},
			8: {ID: 8, Source: SourceLocal, Username: "vic", Role: store.RoleViewer},
			9: {ID: 9, Source: SourceLocal, Username: "eve", Role: store.RoleViewer, MustChangePassword: true,
				TotpEnabledAt: pgtype.Timestamptz{Time: time.Unix(1, 0), Valid: true}},
		},
	}
}

func (f *fakeSessionStore) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

func (f *fakeSessionStore) CreateSession(_ context.Context, a store.CreateSessionParams) error {
	if f.createFn != nil {
		return f.createFn
	}
	f.nextID++
	f.sessions[string(a.IDHash)] = store.Session{
		IDHash: a.IDHash, UserID: a.UserID, ExpiresAt: a.ExpiresAt,
		CreatedAt: ts(f.clock()), LastSeenAt: ts(f.clock()),
		ID: f.nextID, ClientIp: a.ClientIp, UserAgent: a.UserAgent,
	}
	return nil
}

func (f *fakeSessionStore) GetSession(_ context.Context, a store.GetSessionParams) (store.GetSessionRow, error) {
	if f.getErr != nil {
		return store.GetSessionRow{}, f.getErr
	}
	s, ok := f.sessions[string(a.IDHash)]
	if !ok || !s.ExpiresAt.Time.After(a.Now.Time) || !s.LastSeenAt.Time.After(a.IdleCutoff.Time) {
		return store.GetSessionRow{}, pgx.ErrNoRows
	}
	return store.GetSessionRow{Session: s, User: f.users[s.UserID]}, nil
}

func (f *fakeSessionStore) TouchSession(_ context.Context, a store.TouchSessionParams) error {
	f.touches = append(f.touches, a)
	s := f.sessions[string(a.IDHash)]
	s.LastSeenAt = a.Now
	f.sessions[string(a.IDHash)] = s
	return nil
}

func (f *fakeSessionStore) DeleteSession(_ context.Context, id []byte) error {
	if f.delErr != nil {
		return f.delErr
	}
	f.deleted = append(f.deleted, id)
	delete(f.sessions, string(id))
	return nil
}

func (f *fakeSessionStore) PromoteSession(_ context.Context, a store.PromoteSessionParams) (pgtype.Timestamptz, error) {
	if f.markErr != nil {
		return pgtype.Timestamptz{}, f.markErr
	}
	s, ok := f.sessions[string(a.OldIDHash)]
	if !ok {
		return pgtype.Timestamptz{}, pgx.ErrNoRows
	}
	delete(f.sessions, string(a.OldIDHash))
	s.IDHash = a.NewIDHash
	s.MfaVerified = true
	f.sessions[string(a.NewIDHash)] = s
	return s.ExpiresAt, nil
}

func (f *fakeSessionStore) DeleteExpiredSessions(_ context.Context, a store.DeleteExpiredSessionsParams) (int64, error) {
	var n int64
	for k, s := range f.sessions {
		if !s.ExpiresAt.Time.After(a.Now.Time) || !s.LastSeenAt.Time.After(a.IdleCutoff.Time) {
			delete(f.sessions, k)
			n++
		}
	}
	return n, nil
}

const (
	testIdle     = 30 * time.Minute
	testAbsolute = 12 * time.Hour
)

func newTestSessions(t *testing.T, secure bool) (*Sessions, *fakeSessionStore, *fakeClock) {
	t.Helper()
	fs := newFakeSessionStore()
	clk := newFakeClock()
	fs.now = clk.Now
	return NewSessions(fs, SessionOptions{Idle: testIdle, Absolute: testAbsolute, Secure: secure, Now: clk.Now}), fs, clk
}

// start opens a session for user 7 and returns the cookie it set.
func start(t *testing.T, s *Sessions, userID int64, existing *http.Cookie) *http.Cookie {
	t.Helper()
	r := newRequest(http.MethodPost, "/", nil)
	if existing != nil {
		r.AddCookie(existing)
	}
	w := httptest.NewRecorder()
	if err := s.Start(w, r, userID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cs := w.Result().Cookies()
	if len(cs) != 1 {
		t.Fatalf("Start set %d cookies; want 1", len(cs))
	}
	return cs[0]
}

func requestWith(c *http.Cookie) *http.Request {
	r := newRequest(http.MethodGet, "/", nil)
	if c != nil {
		r.AddCookie(c)
	}
	return r
}

func TestCookieName(t *testing.T) {
	if got := CookieName(true); got != "__Host-scanopsis_session" {
		t.Errorf("CookieName(true) = %q", got)
	}
	if got := CookieName(false); got != "scanopsis_session" {
		t.Errorf("CookieName(false) = %q", got)
	}
}

func TestStartCookieAttributes(t *testing.T) {
	for _, secure := range []bool{true, false} {
		s, fs, clk := newTestSessions(t, secure)
		w := httptest.NewRecorder()
		if err := s.Start(w, newRequest(http.MethodPost, "/", nil), 7); err != nil {
			t.Fatal(err)
		}
		c := w.Result().Cookies()[0]
		if c.Name != CookieName(secure) {
			t.Errorf("secure=%v: name %q", secure, c.Name)
		}
		if c.Path != "/" || c.Domain != "" || !c.HttpOnly || c.Secure != secure || c.SameSite != http.SameSiteLaxMode {
			t.Errorf("secure=%v: attributes %+v", secure, c)
		}
		// A browser-session cookie: the server alone decides when it ends.
		if !c.Expires.IsZero() || c.MaxAge != 0 {
			t.Errorf("secure=%v: Expires %v, MaxAge %d; want neither", secure, c.Expires, c.MaxAge)
		}
		for _, stored := range fs.sessions {
			if want := clk.Now().Add(testAbsolute); !stored.ExpiresAt.Time.Equal(want) {
				t.Errorf("secure=%v: stored expiry %v; want %v", secure, stored.ExpiresAt.Time, want)
			}
		}
		raw := w.Header().Get("Set-Cookie")
		for _, want := range []string{"Path=/", "HttpOnly", "SameSite=Lax"} {
			if !strings.Contains(raw, want) {
				t.Errorf("secure=%v: %q lacks %q", secure, raw, want)
			}
		}
		if strings.Contains(raw, "Domain") || strings.Contains(raw, "Max-Age") || strings.Contains(raw, "Expires") {
			t.Errorf("secure=%v: %q has Domain, Max-Age or Expires", secure, raw)
		}
		if strings.Contains(raw, "Secure") != secure {
			t.Errorf("secure=%v: %q", secure, raw)
		}

		// The token is 32 random bytes; only its SHA-256 is stored.
		tok, err := base64.RawURLEncoding.DecodeString(c.Value)
		if err != nil || len(tok) != 32 {
			t.Fatalf("secure=%v: cookie value %q is not a 32-byte base64url token", secure, c.Value)
		}
		sum := sha256.Sum256(tok)
		stored, ok := fs.sessions[string(sum[:])]
		if !ok || len(fs.sessions) != 1 {
			t.Fatalf("secure=%v: store holds %d sessions, none keyed by sha256(token)", secure, len(fs.sessions))
		}
		if stored.UserID != 7 || !stored.ExpiresAt.Time.Equal(clk.Now().Add(testAbsolute)) {
			t.Errorf("secure=%v: stored %+v", secure, stored)
		}
		for k := range fs.sessions {
			if strings.Contains(k, c.Value) || k == string(tok) {
				t.Error("raw token stored")
			}
		}
	}
}

func TestStartTokensDiffer(t *testing.T) {
	s, _, _ := newTestSessions(t, false)
	a, b := start(t, s, 7, nil), start(t, s, 7, nil)
	if a.Value == b.Value {
		t.Fatal("two sessions share a token")
	}
}

func TestStartReplacesExistingSession(t *testing.T) {
	s, fs, _ := newTestSessions(t, false)
	old := start(t, s, 8, nil)
	tok, _ := base64.RawURLEncoding.DecodeString(old.Value)
	oldHash := sha256.Sum256(tok)

	fresh := start(t, s, 7, old)
	if fresh.Value == old.Value {
		t.Fatal("session id was reused after login")
	}
	if _, ok := fs.sessions[string(oldHash[:])]; ok {
		t.Fatal("old session still in the store")
	}
	if len(fs.deleted) != 1 || string(fs.deleted[0]) != string(oldHash[:]) {
		t.Fatalf("deleted %x; want %x", fs.deleted, oldHash)
	}
	if len(fs.sessions) != 1 {
		t.Fatalf("store holds %d sessions; want 1", len(fs.sessions))
	}
	if _, err := s.Authenticate(requestWith(old)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("old cookie still authenticates: %v", err)
	}
}

func TestStartIgnoresMalformedExistingCookie(t *testing.T) {
	s, fs, _ := newTestSessions(t, false)
	start(t, s, 7, &http.Cookie{Name: CookieName(false), Value: "junk"})
	if len(fs.deleted) != 0 {
		t.Fatalf("deleted %v for a malformed cookie", fs.deleted)
	}
}

func TestStartErrors(t *testing.T) {
	s, fs, _ := newTestSessions(t, false)
	boom := errors.New("boom")
	fs.createFn = boom
	w := httptest.NewRecorder()
	if err := s.Start(w, requestWith(nil), 7); !errors.Is(err, boom) {
		t.Fatalf("create failure: %v", err)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("cookie set although the session was not stored")
	}
	fs.createFn = nil
	good := start(t, s, 7, nil)
	fs.delErr = boom
	w = httptest.NewRecorder()
	if err := s.Start(w, requestWith(good), 7); !errors.Is(err, boom) {
		t.Fatalf("delete failure: %v", err)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("cookie set although the old session could not be removed")
	}
}

func TestAuthenticateSuccess(t *testing.T) {
	s, _, _ := newTestSessions(t, true)
	c := start(t, s, 7, nil)
	p, err := s.Authenticate(requestWith(c))
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := base64.RawURLEncoding.DecodeString(c.Value)
	sum := sha256.Sum256(tok)
	if p.UserID != 7 || p.Source != SourceGitLab || p.Username != "ada" || p.Name != "Ada L" ||
		p.Email != "ada@example.com" || p.Role != RoleAdmin {
		t.Fatalf("principal %+v", *p)
	}
	if !bytes.Equal(p.SessionID, sum[:]) {
		t.Fatalf("SessionID %x; want sha256(token) %x", p.SessionID, sum)
	}
	if p.MFAEnrolled || p.MFAVerified || p.MustChangePassword || p.Complete() || p.IsAdmin() {
		t.Fatalf("a new session must start without the second factor: %+v", *p)
	}
	c = start(t, s, 8, nil)
	p, err = s.Authenticate(requestWith(c))
	if err != nil || p.Role != RoleViewer || p.IsAdmin() || p.Source != SourceLocal {
		t.Fatalf("viewer principal %+v, %v", p, err)
	}
}

func TestAuthenticateFillsMFAFields(t *testing.T) {
	s, _, _ := newTestSessions(t, true)
	c := start(t, s, 9, nil)
	p, err := s.Authenticate(requestWith(c))
	if err != nil {
		t.Fatal(err)
	}
	if !p.MFAEnrolled || p.MFAVerified || !p.MustChangePassword {
		t.Fatalf("principal %+v; want enrolled, unverified, must change password", *p)
	}
	rec := httptest.NewRecorder()
	if err := s.PromoteMFAVerified(context.Background(), rec, p.SessionID); err != nil {
		t.Fatal(err)
	}
	// Another session of the same user stays unverified.
	other, _ := s.Authenticate(requestWith(start(t, s, 9, nil)))
	if other.MFAVerified {
		t.Fatal("PromoteMFAVerified leaked to another session")
	}
	p, err = s.Authenticate(requestWith(rec.Result().Cookies()[0]))
	if err != nil || !p.MFAVerified || p.Complete() {
		t.Fatalf("after PromoteMFAVerified: %+v, %v (password change still pending, so not complete)", p, err)
	}
}

func TestPromoteMFAVerifiedReplacesTheToken(t *testing.T) {
	s, fs, clk := newTestSessions(t, false)
	old := start(t, s, 8, nil)
	before, err := s.Authenticate(requestWith(old))
	if err != nil {
		t.Fatal(err)
	}
	created := fs.sessions[string(before.SessionID)]
	clk.Advance(20 * time.Minute)

	rec := httptest.NewRecorder()
	if err := s.PromoteMFAVerified(context.Background(), rec, before.SessionID); err != nil {
		t.Fatal(err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != CookieName(false) || cookies[0].Value == old.Value || !cookies[0].HttpOnly {
		t.Fatalf("cookies %+v; want one new session cookie", cookies)
	}
	if !cookies[0].Expires.IsZero() || cookies[0].MaxAge != 0 {
		t.Fatalf("cookie Expires %v, MaxAge %d; want a browser-session cookie", cookies[0].Expires, cookies[0].MaxAge)
	}
	if _, err := s.Authenticate(requestWith(old)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("old cookie after promotion: %v; want ErrNoSession", err)
	}
	after, err := s.Authenticate(requestWith(cookies[0]))
	if err != nil || after.UserID != before.UserID || !after.MFAVerified {
		t.Fatalf("new cookie: %+v, %v", after, err)
	}
	if got := fs.sessions[string(after.SessionID)]; !got.ExpiresAt.Time.Equal(created.ExpiresAt.Time) {
		t.Fatalf("expiry %v; want the original %v (the absolute timeout does not restart)", got.ExpiresAt.Time, created.ExpiresAt.Time)
	}
	if len(fs.sessions) != 1 {
		t.Fatalf("%d sessions; want the old row gone", len(fs.sessions))
	}
}

func TestPromoteMFAVerifiedError(t *testing.T) {
	s, fs, _ := newTestSessions(t, true)
	fs.markErr = errors.New("db down")
	rec := httptest.NewRecorder()
	if err := s.PromoteMFAVerified(context.Background(), rec, []byte("x")); err == nil || !errors.Is(err, fs.markErr) {
		t.Fatalf("err %v; want wrapped db error", err)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("a cookie was set although promotion failed")
	}
}

func TestAuthenticateAbsentAndMalformedMakeNoDBCall(t *testing.T) {
	s, fs, _ := newTestSessions(t, false)
	fs.getErr = errors.New("database must not be consulted")
	name := CookieName(false)
	valid43 := strings.Repeat("A", 43)
	for label, c := range map[string]*http.Cookie{
		"absent":         nil,
		"wrong name":     {Name: "other", Value: valid43},
		"empty":          {Name: name, Value: ""},
		"not base64":     {Name: name, Value: "!!!!"},
		"too short":      {Name: name, Value: "AAAA"},
		"too long":       {Name: name, Value: strings.Repeat("A", 44)},
		"padded":         {Name: name, Value: valid43 + "="},
		"std alphabet":   {Name: name, Value: strings.Repeat("+", 43)},
		"non-canonical":  {Name: name, Value: strings.Repeat("A", 42) + "B"},
		"secure cookie":  {Name: CookieName(true), Value: valid43},
		"whitespace pad": {Name: name, Value: " " + valid43},
	} {
		p, err := s.Authenticate(requestWith(c))
		if p != nil || !errors.Is(err, ErrNoSession) {
			t.Errorf("%s: got %v, %v; want ErrNoSession", label, p, err)
		}
	}
}

func TestAuthenticateUnknownToken(t *testing.T) {
	s, _, _ := newTestSessions(t, false)
	c := &http.Cookie{Name: CookieName(false), Value: strings.Repeat("A", 43)}
	if _, err := s.Authenticate(requestWith(c)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("unknown token: %v", err)
	}
}

func TestAuthenticateDatabaseErrorIsNotNoSession(t *testing.T) {
	s, fs, _ := newTestSessions(t, false)
	c := start(t, s, 7, nil)
	boom := errors.New("connection reset")
	fs.getErr = boom
	_, err := s.Authenticate(requestWith(c))
	if !errors.Is(err, boom) || errors.Is(err, ErrNoSession) {
		t.Fatalf("got %v; want the database error, not ErrNoSession", err)
	}
}

func TestAuthenticateAbsoluteExpiry(t *testing.T) {
	s, _, clk := newTestSessions(t, false)
	c := start(t, s, 7, nil)
	// Stay active so that only the absolute limit can end the session.
	for elapsed := time.Duration(0); elapsed < testAbsolute-10*time.Minute; elapsed += 10 * time.Minute {
		clk.Advance(10 * time.Minute)
		if _, err := s.Authenticate(requestWith(c)); err != nil {
			t.Fatalf("active session rejected after %v: %v", elapsed+10*time.Minute, err)
		}
	}
	clk.Advance(10*time.Minute + time.Second)
	if _, err := s.Authenticate(requestWith(c)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("session past its absolute expiry: %v", err)
	}
}

func TestAuthenticateIdleExpiry(t *testing.T) {
	s, _, clk := newTestSessions(t, false)
	c := start(t, s, 7, nil)
	clk.Advance(testIdle - time.Second)
	if _, err := s.Authenticate(requestWith(c)); err != nil {
		t.Fatalf("session just inside the idle limit rejected: %v", err)
	}
	clk.Advance(testIdle + time.Second) // 1s past the idle limit since the touch above
	if _, err := s.Authenticate(requestWith(c)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("idle session accepted: %v", err)
	}
}

func TestAuthenticateTouchThrottling(t *testing.T) {
	s, fs, clk := newTestSessions(t, false)
	c := start(t, s, 7, nil)

	for _, d := range []time.Duration{0, 20 * time.Second, 39 * time.Second} {
		clk.Advance(d)
		if _, err := s.Authenticate(requestWith(c)); err != nil {
			t.Fatal(err)
		}
	}
	if len(fs.touches) != 0 {
		t.Fatalf("%d touches within one minute; want 0", len(fs.touches))
	}

	clk.Advance(2 * time.Second) // 61s since the session started
	if _, err := s.Authenticate(requestWith(c)); err != nil {
		t.Fatal(err)
	}
	if len(fs.touches) != 1 {
		t.Fatalf("%d touches after a minute; want 1", len(fs.touches))
	}
	if !fs.touches[0].Now.Time.Equal(clk.Now()) {
		t.Errorf("touched with %v; want %v", fs.touches[0].Now.Time, clk.Now())
	}
	tok, _ := base64.RawURLEncoding.DecodeString(c.Value)
	if sum := sha256.Sum256(tok); string(fs.touches[0].IDHash) != string(sum[:]) {
		t.Error("touched the wrong session")
	}

	// The touch resets the throttle.
	clk.Advance(30 * time.Second)
	if _, err := s.Authenticate(requestWith(c)); err != nil {
		t.Fatal(err)
	}
	if len(fs.touches) != 1 {
		t.Fatalf("%d touches 30s after a touch; want still 1", len(fs.touches))
	}
}

func TestEnd(t *testing.T) {
	for _, secure := range []bool{true, false} {
		s, fs, _ := newTestSessions(t, secure)
		c := start(t, s, 7, nil)
		w := httptest.NewRecorder()
		if err := s.End(w, requestWith(c)); err != nil {
			t.Fatal(err)
		}
		if len(fs.sessions) != 0 {
			t.Fatalf("secure=%v: session survived End", secure)
		}
		if _, err := s.Authenticate(requestWith(c)); !errors.Is(err, ErrNoSession) {
			t.Fatalf("secure=%v: cookie still valid after End: %v", secure, err)
		}
		cleared := w.Result().Cookies()
		if len(cleared) != 1 {
			t.Fatalf("secure=%v: %d cookies", secure, len(cleared))
		}
		cc := cleared[0]
		if cc.Name != CookieName(secure) || cc.Value != "" || cc.MaxAge != -1 || cc.Path != "/" ||
			!cc.HttpOnly || cc.Secure != secure || cc.SameSite != http.SameSiteLaxMode || cc.Domain != "" {
			t.Errorf("secure=%v: clearing cookie %+v", secure, cc)
		}
		if raw := w.Header().Get("Set-Cookie"); !strings.Contains(raw, "Max-Age=0") {
			t.Errorf("secure=%v: %q lacks Max-Age=0", secure, raw)
		}
	}
}

func TestEndWithoutSession(t *testing.T) {
	s, fs, _ := newTestSessions(t, false)
	w := httptest.NewRecorder()
	if err := s.End(w, requestWith(nil)); err != nil {
		t.Fatal(err)
	}
	if len(fs.deleted) != 0 || len(w.Result().Cookies()) != 1 {
		t.Fatalf("deleted=%d cookies=%d; want 0 deletes and a clearing cookie", len(fs.deleted), len(w.Result().Cookies()))
	}
}

func TestEndDeleteFailure(t *testing.T) {
	s, fs, _ := newTestSessions(t, false)
	c := start(t, s, 7, nil)
	boom := errors.New("boom")
	fs.delErr = boom
	if err := s.End(httptest.NewRecorder(), requestWith(c)); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
}

func TestRemoveExpired(t *testing.T) {
	s, fs, clk := newTestSessions(t, false)
	start(t, s, 7, nil)
	clk.Advance(testIdle + time.Minute)
	start(t, s, 8, nil)
	n, err := s.RemoveExpired(context.Background())
	if err != nil || n != 1 || len(fs.sessions) != 1 {
		t.Fatalf("RemoveExpired = %d, %v with %d left; want 1, nil, 1", n, err, len(fs.sessions))
	}
}

func TestNewSessionsDefaultsClock(t *testing.T) {
	fs := newFakeSessionStore()
	s := NewSessions(fs, SessionOptions{Idle: time.Hour, Absolute: time.Hour})
	before := time.Now()
	w := httptest.NewRecorder()
	if err := s.Start(w, requestWith(nil), 7); err != nil {
		t.Fatal(err)
	}
	for _, stored := range fs.sessions {
		if exp := stored.ExpiresAt.Time; exp.Before(before.Add(time.Hour)) {
			t.Fatalf("expiry %v is not based on the wall clock", exp)
		}
	}
}

func TestStartRecordsClientAndShortUserAgent(t *testing.T) {
	fs := newFakeSessionStore()
	addr := netip.MustParseAddr("2001:db8::7")
	s := NewSessions(fs, SessionOptions{Idle: time.Hour, Absolute: time.Hour,
		ClientIP: func(*http.Request) netip.Addr { return addr }})
	r := requestWith(nil)
	// 199 ASCII bytes and then a two-byte character that would straddle the limit.
	r.Header.Set("User-Agent", strings.Repeat("a", 199)+"é and more")
	if err := s.Start(httptest.NewRecorder(), r, 7); err != nil {
		t.Fatal(err)
	}
	for _, stored := range fs.sessions {
		if stored.ClientIp != "2001:db8::7" {
			t.Errorf("client IP %q", stored.ClientIp)
		}
		if stored.UserAgent != strings.Repeat("a", 199) {
			t.Errorf("user agent %q (%d bytes); want cut before the split character", stored.UserAgent, len(stored.UserAgent))
		}
	}

	fs = newFakeSessionStore()
	s = NewSessions(fs, SessionOptions{Idle: time.Hour, Absolute: time.Hour})
	r = requestWith(nil)
	r.Header.Set("User-Agent", "curl/8 \xff")
	if err := s.Start(httptest.NewRecorder(), r, 7); err != nil {
		t.Fatal(err)
	}
	for _, stored := range fs.sessions {
		if stored.ClientIp != "" || stored.UserAgent != "curl/8 \uFFFD" {
			t.Errorf("without a resolver: IP %q, user agent %q", stored.ClientIp, stored.UserAgent)
		}
	}
}

func TestSetTimeoutsAppliesToLaterRequests(t *testing.T) {
	st := newFakeSessionStore()
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	st.now = now
	s := NewSessions(st, SessionOptions{Idle: time.Hour, Absolute: 2 * time.Hour, Now: now})
	s.SetTimeouts(10*time.Minute, 30*time.Minute)

	w := httptest.NewRecorder()
	if err := s.Start(w, httptest.NewRequestWithContext(t.Context(), "POST", "/", nil), 7); err != nil {
		t.Fatal(err)
	}
	for _, sess := range st.sessions {
		if got := sess.ExpiresAt.Time.Sub(clock); got != 30*time.Minute {
			t.Fatalf("absolute expiry = %v after SetTimeouts, want 30m", got)
		}
	}
	cookie := w.Result().Cookies()[0]
	authAt := func(d time.Duration) error {
		clock = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).Add(d)
		r := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
		r.AddCookie(cookie)
		_, err := s.Authenticate(r)
		return err
	}
	if err := authAt(9 * time.Minute); err != nil {
		t.Fatalf("session idle for 9m rejected with a 10m idle timeout: %v", err)
	}
	// The original one-hour idle timeout would accept this; the new one does not.
	if err := authAt(9*time.Minute + 11*time.Minute); !errors.Is(err, ErrNoSession) {
		t.Fatalf("session idle for 11m accepted with a 10m idle timeout: %v", err)
	}
}
