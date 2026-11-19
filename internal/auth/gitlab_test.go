// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package auth_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/testutil/fakegitlab"
)

const (
	publicURL = "https://scanopsis.example.com"
	callback  = publicURL + "/api/v1/auth/gitlab/callback"
)

var (
	alice = fakegitlab.User{ID: 42, Username: "alice", Name: "Alice Example", Email: "alice@example.com", Groups: []string{"platform"}}
	ip    = netip.MustParseAddr("192.0.2.10")
)

type userStore struct {
	mu    sync.Mutex
	calls []store.UpsertGitLabUserParams
	err   error
}

func (s *userStore) UpsertGitLabUser(_ context.Context, arg store.UpsertGitLabUserParams) (store.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return store.User{}, s.err
	}
	s.calls = append(s.calls, arg)
	return store.User{ID: 1000 + arg.GitlabUserID.Int64, Username: arg.Username}, nil
}

type sessions struct {
	mu    sync.Mutex
	users []int64
	err   error
}

func (s *sessions) Start(w http.ResponseWriter, _ *http.Request, userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.users = append(s.users, userID)
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "started"})
	return nil
}

type event struct {
	Actor, Action string
	Detail        map[string]any
}

type recorder struct {
	mu     sync.Mutex
	events []event
}

func (r *recorder) Record(_ context.Context, actor, action string, detail map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event{actor, action, detail})
}

type env struct {
	t        *testing.T
	fake     *fakegitlab.Server
	ts       *httptest.Server
	users    *userStore
	sessions *sessions
	audit    *recorder
	gl       *auth.GitLab
	hits     atomic.Int64 // requests that reached the fake
	down     atomic.Bool
}

func newEnv(t *testing.T, mutate func(*auth.GitLabOptions)) *env {
	t.Helper()
	e := &env{t: t, users: &userStore{}, sessions: &sessions{}, audit: &recorder{}}
	e.ts = httptest.NewUnstartedServer(nil)
	var err error
	e.fake, err = fakegitlab.New("http://" + e.ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	e.fake.AddClient("cid", "secret", callback)
	e.fake.SetUser(alice)
	inner := e.fake.Handler()
	e.ts.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.hits.Add(1)
		if e.down.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		inner.ServeHTTP(w, r)
	})
	e.ts.Start()
	t.Cleanup(e.ts.Close)

	opts := auth.GitLabOptions{
		IssuerURL: e.fake.IssuerURL(), ClientID: "cid", ClientSecret: "secret",
		PublicURL: publicURL, Secure: true,
	}
	if mutate != nil {
		mutate(&opts)
	}
	e.gl = auth.NewGitLab(opts, e.users, e.sessions, e.audit, func(*http.Request) netip.Addr { return ip })
	return e
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func serve(h http.Handler, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func (e *env) cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// login runs the login handler and returns the authorize URL and the cookie.
func (e *env) login(returnTo string) (*url.URL, *http.Cookie) {
	e.t.Helper()
	target := "/api/v1/auth/gitlab/login"
	if returnTo != "" {
		target += "?return_to=" + url.QueryEscape(returnTo)
	}
	rec := serve(e.gl.LoginHandler(), target)
	if rec.Code != http.StatusFound {
		e.t.Fatalf("login status = %d, body %s", rec.Code, rec.Body)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		e.t.Fatal(err)
	}
	return loc, e.cookieNamed(rec, "__Host-scanopsis_oidc")
}

// authorize follows the authorize URL at the fake and returns the callback
// query it redirects to.
func (e *env) authorize(loc *url.URL) url.Values {
	e.t.Helper()
	req, err := http.NewRequestWithContext(e.t.Context(), http.MethodGet, loc.String(), nil)
	if err != nil {
		e.t.Fatal(err)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		e.t.Fatalf("authorize status = %d", resp.StatusCode)
	}
	back, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		e.t.Fatal(err)
	}
	if got := back.Scheme + "://" + back.Host + back.Path; got != callback {
		e.t.Fatalf("authorize redirected to %s", got)
	}
	return back.Query()
}

// signIn runs the whole flow and returns the callback response.
func (e *env) signIn(returnTo string) *httptest.ResponseRecorder {
	e.t.Helper()
	loc, ck := e.login(returnTo)
	q := e.authorize(loc)
	return serve(e.gl.CallbackHandler(), "/api/v1/auth/gitlab/callback?"+q.Encode(), ck)
}

func (e *env) wantRedirect(rec *httptest.ResponseRecorder, want string) {
	e.t.Helper()
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
		e.t.Errorf("callback = %d %q, want 302 %q", rec.Code, rec.Header().Get("Location"), want)
	}
	cleared := e.cookieNamed(rec, "__Host-scanopsis_oidc")
	if cleared == nil || cleared.MaxAge != -1 || cleared.Value != "" || cleared.Path != "/" {
		e.t.Errorf("oidc cookie not cleared: %+v", cleared)
	}
}

func (e *env) wantNoSignIn() {
	e.t.Helper()
	if len(e.users.calls) != 0 || len(e.sessions.users) != 0 {
		e.t.Errorf("user upserted or session started: %+v %v", e.users.calls, e.sessions.users)
	}
}

func TestSuccessfulSignIn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		admins []string
		want   bool
	}{
		{"viewer", []string{"bob"}, false},
		{"forced admin", []string{"bob", "alice"}, true},
		{"admin match is exact", []string{"Alice", "alice2"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, func(o *auth.GitLabOptions) { o.AdminUsernames = tc.admins })
			rec := e.signIn("/services?tab=scans")
			e.wantRedirect(rec, "/services?tab=scans")
			if c := e.cookieNamed(rec, "session"); c == nil || c.Value != "started" {
				t.Error("session cookie was not set")
			}
			if len(e.users.calls) != 1 {
				t.Fatalf("upserts = %d", len(e.users.calls))
			}
			got := e.users.calls[0]
			want := store.UpsertGitLabUserParams{
				GitlabUserID: got.GitlabUserID, Username: "alice", Name: "Alice Example",
				Email: "alice@example.com", ForceAdmin: tc.want,
			}
			if !got.GitlabUserID.Valid || got.GitlabUserID.Int64 != 42 || !reflect.DeepEqual(got, want) {
				t.Errorf("upsert = %+v, want %+v", got, want)
			}
			if !reflect.DeepEqual(e.sessions.users, []int64{1042}) {
				t.Errorf("sessions started for %v, want [1042]", e.sessions.users)
			}
			wantEvents := []event{{"alice", "login.succeeded", map[string]any{"method": "gitlab", "username": "alice", "ip": "192.0.2.10"}}}
			if !reflect.DeepEqual(e.audit.events, wantEvents) {
				t.Errorf("audit = %+v, want %+v", e.audit.events, wantEvents)
			}
			if e.fake.UserinfoCalls() != 0 {
				t.Error("userinfo called although no allow-list is set")
			}
		})
	}
}

func TestLoginRedirectAndCookie(t *testing.T) {
	for _, secure := range []bool{true, false} {
		e := newEnv(t, func(o *auth.GitLabOptions) { o.Secure = secure })
		rec := serve(e.gl.LoginHandler(), "/api/v1/auth/gitlab/login?return_to=%2Fservices")
		name := "scanopsis_oidc"
		if secure {
			name = "__Host-scanopsis_oidc"
		}
		ck := e.cookieNamed(rec, name)
		if ck == nil {
			t.Fatalf("secure=%v: cookie %s missing", secure, name)
		}
		if ck.Path != "/" || ck.MaxAge != 600 || !ck.HttpOnly || ck.Secure != secure || ck.SameSite != http.SameSiteLaxMode || ck.Domain != "" {
			t.Errorf("secure=%v: cookie = %+v", secure, ck)
		}
		if n := len(rec.Result().Cookies()); n != 1 {
			t.Errorf("secure=%v: %d cookies set, want 1", secure, n)
		}

		loc, err := url.Parse(rec.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		q := loc.Query()
		if loc.Scheme+"://"+loc.Host+loc.Path != e.fake.IssuerURL()+"/oauth/authorize" {
			t.Errorf("authorize URL = %s", loc)
		}
		if q.Get("client_id") != "cid" || q.Get("redirect_uri") != callback || q.Get("response_type") != "code" ||
			q.Get("scope") != "openid profile email" || q.Get("code_challenge_method") != "S256" ||
			q.Get("code_challenge") == "" || len(q.Get("state")) != 43 || len(q.Get("nonce")) != 43 {
			t.Errorf("authorize query = %v", q)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Error("login response is cacheable")
		}
	}
}

func TestPKCEVerifierIsSent(t *testing.T) {
	// The fake rejects a token request whose verifier does not match the
	// challenge, so a successful sign-in proves the verifier was sent. Prove
	// the fake would notice by tampering with the challenge.
	e := newEnv(t, nil)
	loc, ck := e.login("")
	q := loc.Query()
	q.Set("code_challenge", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	loc.RawQuery = q.Encode()
	cb := e.authorize(loc)
	rec := serve(e.gl.CallbackHandler(), "/api/v1/auth/gitlab/callback?"+cb.Encode(), ck)
	e.wantRedirect(rec, "/login?error=login_failed")
	e.wantNoSignIn()

	rec = e.signIn("")
	e.wantRedirect(rec, "/")
}

func TestMissingAndMismatchedState(t *testing.T) {
	e := newEnv(t, nil)
	loc, ck := e.login("")
	q := e.authorize(loc)

	// No cookie.
	e.wantRedirect(serve(e.gl.CallbackHandler(), "/cb?"+q.Encode()), "/login?error=login_expired")
	// Garbage cookie.
	bad := &http.Cookie{Name: ck.Name, Value: "!!!"}
	e.wantRedirect(serve(e.gl.CallbackHandler(), "/cb?"+q.Encode(), bad), "/login?error=login_expired")
	// State from another flow.
	q.Set("state", "someone-elses-state")
	e.wantRedirect(serve(e.gl.CallbackHandler(), "/cb?"+q.Encode(), ck), "/login?error=login_expired")
	// No state at all.
	q.Del("state")
	e.wantRedirect(serve(e.gl.CallbackHandler(), "/cb?"+q.Encode(), ck), "/login?error=login_expired")
	e.wantNoSignIn()
	if len(e.audit.events) != 0 {
		t.Errorf("unauthenticated failures were audited: %+v", e.audit.events)
	}
}

func TestTokenValidationFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		hook func(*testing.T, *env)
	}{
		{"nonce mismatch", func(_ *testing.T, e *env) { e.fake.SetNonceOverride("not-the-nonce") }},
		{"foreign signing key", func(t *testing.T, e *env) {
			if err := e.fake.UseForeignKey(); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrong audience", func(_ *testing.T, e *env) { e.fake.SetAudienceOverride("another-client") }},
		{"expired token", func(_ *testing.T, e *env) {
			e.fake.SetNow(func() time.Time { return time.Now().Add(-time.Hour) })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, nil)
			tc.hook(t, e)
			e.wantRedirect(e.signIn("/services"), "/login?error=login_failed")
			e.wantNoSignIn()
		})
	}
}

func TestMissingClaims(t *testing.T) {
	for name, u := range map[string]fakegitlab.User{
		"no username": {ID: 5, Name: "N", Email: "n@example.com"},
		"zero id":     {ID: 0, Username: "carol"},
		"negative id": {ID: -3, Username: "carol"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, nil)
			e.fake.SetUser(u)
			e.wantRedirect(e.signIn(""), "/login?error=login_failed")
			e.wantNoSignIn()
		})
	}
}

func TestAccessDenied(t *testing.T) {
	e := newEnv(t, nil)
	loc, ck := e.login("")
	q := loc.Query()
	q.Set("deny", "1")
	loc.RawQuery = q.Encode()
	cb := e.authorize(loc)
	if cb.Get("error") != "access_denied" {
		t.Fatalf("fake did not deny: %v", cb)
	}
	e.wantRedirect(serve(e.gl.CallbackHandler(), "/cb?"+cb.Encode(), ck), "/login?error=access_denied")
	e.wantNoSignIn()
}

func TestOtherProviderErrorsAndMissingCode(t *testing.T) {
	e := newEnv(t, nil)
	loc, ck := e.login("")
	state := loc.Query().Get("state")
	e.wantRedirect(serve(e.gl.CallbackHandler(), "/cb?error=server_error&state="+state, ck), "/login?error=login_failed")
	e.wantRedirect(serve(e.gl.CallbackHandler(), "/cb?state="+state, ck), "/login?error=login_failed")
	e.wantRedirect(serve(e.gl.CallbackHandler(), "/cb?code=bogus&state="+state, ck), "/login?error=login_failed")
	e.wantNoSignIn()
}

func TestGroupAllowList(t *testing.T) {
	for _, tc := range []struct {
		name    string
		groups  []string
		allowed []string
		ok      bool
	}{
		{"exact group", []string{"security"}, []string{"security"}, true},
		{"subgroup", []string{"security/appsec"}, []string{"security"}, true},
		{"nested subgroup", []string{"security/appsec/tools"}, []string{"security"}, true},
		{"second allowed path", []string{"platform"}, []string{"security", "platform"}, true},
		{"subgroup path itself", []string{"security/appsec"}, []string{"security/appsec"}, true},
		{"prefix is not a subgroup", []string{"securityteam"}, []string{"security"}, false},
		{"parent is not allowed by child", []string{"security"}, []string{"security/appsec"}, false},
		{"case differs", []string{"Security"}, []string{"security"}, false},
		{"no groups", nil, []string{"security"}, false},
		{"other group", []string{"platform"}, []string{"security"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, func(o *auth.GitLabOptions) { o.AllowedGroups = tc.allowed })
			u := alice
			u.Groups = tc.groups
			e.fake.SetUser(u)
			rec := e.signIn("/services")
			if e.fake.UserinfoCalls() != 1 {
				t.Errorf("userinfo calls = %d, want 1", e.fake.UserinfoCalls())
			}
			if tc.ok {
				e.wantRedirect(rec, "/services")
				if len(e.users.calls) != 1 || len(e.sessions.users) != 1 {
					t.Errorf("not signed in: %v %v", e.users.calls, e.sessions.users)
				}
				return
			}
			e.wantRedirect(rec, "/login?error=group_not_allowed")
			e.wantNoSignIn()
			want := []event{{"alice", "login.failed", map[string]any{"method": "gitlab", "username": "alice", "reason": "group_not_allowed", "ip": "192.0.2.10"}}}
			if !reflect.DeepEqual(e.audit.events, want) {
				t.Errorf("audit = %+v, want %+v", e.audit.events, want)
			}
		})
	}
}

func TestReturnToSanitised(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/services", "/services"},
		{"/a/b?c=d#e", "/a/b?c=d#e"},
		{"", "/"},
		{"//evil.com", "/"},
		{"//evil.com/path", "/"},
		{"https://evil.com", "/"},
		{`/\evil.com`, "/"},
		{"javascript:alert(1)", "/"},
		{"services", "/"},
		{"/ok\r\nSet-Cookie: x=y", "/"},
		{"/ok\x00", "/"},
		{"/" + strings.Repeat("a", 600), "/"},
	} {
		e := newEnv(t, nil)
		e.wantRedirect(e.signIn(tc.in), tc.want)
		if len(e.sessions.users) != 1 {
			t.Errorf("return_to %q: sign-in failed", tc.in)
		}
	}
}

func TestTamperedCookieReturnToIsSanitised(t *testing.T) {
	// The cookie is not signed, so return_to is sanitised again on read.
	e := newEnv(t, nil)
	loc, ck := e.login("/ok")
	q := e.authorize(loc)
	forged := forgeCookie(t, ck, "https://evil.com")
	rec := serve(e.gl.CallbackHandler(), "/cb?"+q.Encode(), forged)
	e.wantRedirect(rec, "/")
}

func TestStoreAndSessionErrors(t *testing.T) {
	t.Run("store", func(t *testing.T) {
		e := newEnv(t, nil)
		e.users.err = errors.New("db down")
		e.wantRedirect(e.signIn(""), "/login?error=login_failed")
		want := []event{{"alice", "login.failed", map[string]any{"method": "gitlab", "username": "alice", "reason": "login_failed", "ip": "192.0.2.10"}}}
		if !reflect.DeepEqual(e.audit.events, want) {
			t.Errorf("audit = %+v", e.audit.events)
		}
	})
	t.Run("session", func(t *testing.T) {
		e := newEnv(t, nil)
		e.sessions.err = errors.New("no session")
		rec := e.signIn("")
		e.wantRedirect(rec, "/login?error=login_failed")
		if c := e.cookieNamed(rec, "session"); c != nil {
			t.Error("session cookie set despite failure")
		}
		if len(e.audit.events) != 1 || e.audit.events[0].Action != "login.failed" {
			t.Errorf("audit = %+v", e.audit.events)
		}
	})
}

func TestAuditHasNoSecrets(t *testing.T) {
	e := newEnv(t, nil)
	loc, ck := e.login("")
	q := e.authorize(loc)
	serve(e.gl.CallbackHandler(), "/cb?"+q.Encode(), ck)
	b, _ := json.Marshal(e.audit.events)
	for _, secret := range []string{q.Get("code"), q.Get("state"), loc.Query().Get("nonce"), ck.Value, "secret"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("audit events contain %q: %s", secret, b)
		}
	}
}

func TestDiscoveryIsLazyAndRetried(t *testing.T) {
	now := time.Now()
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); defer mu.Unlock(); now = now.Add(d) }

	e := newEnv(t, func(o *auth.GitLabOptions) { o.Now = clock })
	if n := e.hits.Load(); n != 0 {
		t.Fatalf("GitLab contacted %d times at construction", n)
	}
	e.down.Store(true)

	for i := range 2 { // second request is inside the retry window
		rec := serve(e.gl.LoginHandler(), "/api/v1/auth/gitlab/login")
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("request %d: %d %s", i, rec.Code, rec.Header().Get("Content-Type"))
		}
		var body struct {
			Error struct{ Code, Message string }
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != "gitlab_unavailable" || body.Error.Message == "" {
			t.Fatalf("request %d: body = %s", i, rec.Body)
		}
		if e.cookieNamed(rec, "__Host-scanopsis_oidc") != nil {
			t.Error("oidc cookie set although GitLab is unavailable")
		}
	}
	if n := e.hits.Load(); n != 1 {
		t.Errorf("discovery attempted %d times within the retry window, want 1", n)
	}

	// GitLab is back, but the retry window has not passed.
	e.down.Store(false)
	advance(5 * time.Second)
	if rec := serve(e.gl.LoginHandler(), "/api/v1/auth/gitlab/login"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("inside window: status = %d", rec.Code)
	}

	advance(6 * time.Second)
	e.wantRedirect(e.signIn("/services"), "/services")

	// The provider is cached: no further discovery requests.
	before := e.hits.Load()
	e.gl.LoginHandler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/login", nil))
	if e.hits.Load() != before {
		t.Errorf("login after success contacted GitLab (%d requests)", e.hits.Load()-before)
	}
}

func TestCallbackWhenGitLabUnavailable(t *testing.T) {
	e := newEnv(t, nil)
	loc, ck := e.login("")
	q := e.authorize(loc)
	// Fresh instance: its provider was never discovered, and GitLab is down.
	e2 := newEnv(t, nil)
	e2.down.Store(true)
	rec := serve(e2.gl.CallbackHandler(), "/cb?"+q.Encode(), ck)
	e2.wantRedirect(rec, "/login?error=login_failed")
}

// forgeCookie returns ck with return_to replaced, as an attacker who can set
// cookies might.
func forgeCookie(t *testing.T, ck *http.Cookie, returnTo string) *http.Cookie {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(ck.Value)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m["r"] = returnTo
	raw, _ = json.Marshal(m)
	return &http.Cookie{Name: ck.Name, Value: base64.RawURLEncoding.EncodeToString(raw)}
}
