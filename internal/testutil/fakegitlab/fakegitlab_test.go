// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package fakegitlab_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/siyamsarker/scanopsis/internal/testutil/fakegitlab"
)

const (
	redirectURI = "https://scanopsis.example.com/api/v1/auth/gitlab/callback"
	verifier    = "v3rifier-v3rifier-v3rifier-v3rifier-v3rifier"
)

var alice = fakegitlab.User{ID: 42, Username: "alice", Name: "Alice Example", Email: "alice@example.com", Groups: []string{"platform"}}

func newFake(t *testing.T) (*fakegitlab.Server, *httptest.Server) {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	f, err := fakegitlab.New("http://" + ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = f.Handler()
	ts.Start()
	t.Cleanup(ts.Close)
	f.AddClient("cid", "secret", redirectURI)
	return f, ts
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func challenge(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func authQuery() url.Values {
	return url.Values{
		"client_id": {"cid"}, "redirect_uri": {redirectURI}, "response_type": {"code"},
		"code_challenge": {challenge(verifier)}, "code_challenge_method": {"S256"},
		"state": {"st"}, "nonce": {"no"}, "scope": {"openid profile email"},
	}
}

type result struct {
	StatusCode int
	Header     http.Header
	Body       string
}

func get(t *testing.T, u string) result {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return result{resp.StatusCode, resp.Header, string(b)}
}

// authorize returns the redirect target of an authorization request.
func authorize(t *testing.T, ts *httptest.Server, q url.Values) *url.URL {
	t.Helper()
	resp := get(t, ts.URL+"/oauth/authorize?"+q.Encode())
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize status = %d", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func tokenRequest(t *testing.T, ts *httptest.Server, form url.Values, basic bool) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if basic {
		req.SetBasicAuth("cid", "secret")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, m
}

func codeForm(code string, basic bool) url.Values {
	f := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}, "code_verifier": {verifier}}
	if !basic {
		f.Set("client_id", "cid")
		f.Set("client_secret", "secret")
	}
	return f
}

func TestDiscoveryAndKeys(t *testing.T) {
	f, ts := newFake(t)
	var d map[string]any
	if err := json.Unmarshal([]byte(get(t, ts.URL+"/.well-known/openid-configuration").Body), &d); err != nil {
		t.Fatal(err)
	}
	if d["issuer"] != f.IssuerURL() || d["jwks_uri"] != f.IssuerURL()+"/oauth/discovery/keys" ||
		d["token_endpoint"] != f.IssuerURL()+"/oauth/token" {
		t.Errorf("discovery = %v", d)
	}
	key := pubKey(t, ts)
	if key.Algorithm != "RS256" || key.KeyID == "" {
		t.Errorf("key = %+v", key)
	}
}

func TestFullFlow(t *testing.T) {
	f, ts := newFake(t)
	f.SetUser(alice)
	loc := authorize(t, ts, authQuery())
	if loc.Query().Get("state") != "st" || loc.Query().Get("code") == "" || loc.Host != "scanopsis.example.com" {
		t.Fatalf("redirect = %s", loc)
	}
	for _, basic := range []bool{true, false} {
		if !basic {
			loc = authorize(t, ts, authQuery())
		}
		status, body := tokenRequest(t, ts, codeForm(loc.Query().Get("code"), basic), basic)
		if status != http.StatusOK || body["token_type"] != "Bearer" {
			t.Fatalf("token = %d %v", status, body)
		}
		tok, err := jwt.ParseSigned(body["id_token"].(string), []jose.SignatureAlgorithm{jose.RS256})
		if err != nil {
			t.Fatal(err)
		}
		var claims map[string]any
		if err := tok.Claims(pubKey(t, ts), &claims); err != nil {
			t.Fatal(err)
		}
		if claims["iss"] != f.IssuerURL() || claims["sub"] != "42" || claims["aud"] != "cid" ||
			claims["nonce"] != "no" || claims["preferred_username"] != "alice" ||
			claims["email_verified"] != true || claims["email"] != "alice@example.com" {
			t.Errorf("claims = %v", claims)
		}

		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/oauth/userinfo", nil)
		req.Header.Set("Authorization", "Bearer "+body["access_token"].(string))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var ui map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&ui)
		_ = resp.Body.Close()
		if ui["sub"] != "42" || ui["preferred_username"] != "alice" || len(ui["groups"].([]any)) != 1 {
			t.Errorf("userinfo = %v", ui)
		}
	}
	if f.UserinfoCalls() != 2 {
		t.Errorf("UserinfoCalls = %d, want 2", f.UserinfoCalls())
	}
}

func TestUserinfoRejectsBadToken(t *testing.T) {
	_, ts := newFake(t)
	for _, h := range []string{"", "Bearer nope", "Basic abc"} {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/oauth/userinfo", nil)
		if h != "" {
			req.Header.Set("Authorization", h)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%q: status = %d", h, resp.StatusCode)
		}
	}
}

func TestAuthorizeValidation(t *testing.T) {
	f, ts := newFake(t)
	f.SetUser(alice)

	for name, mutate := range map[string]func(url.Values){
		"unknown client":     func(q url.Values) { q.Set("client_id", "other") },
		"redirect mismatch":  func(q url.Values) { q.Set("redirect_uri", redirectURI+"/") },
		"redirect prefix":    func(q url.Values) { q.Set("redirect_uri", "https://evil.example.com") },
		"missing client":     func(q url.Values) { q.Del("client_id") },
		"missing redirect":   func(q url.Values) { q.Del("redirect_uri") },
		"redirect different": func(q url.Values) { q.Set("redirect_uri", "https://scanopsis.example.com/other") },
	} {
		q := authQuery()
		mutate(q)
		resp := get(t, ts.URL+"/oauth/authorize?"+q.Encode())
		if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("Location") != "" {
			t.Errorf("%s: status = %d, location = %q", name, resp.StatusCode, resp.Header.Get("Location"))
		}
	}

	for name, mutate := range map[string]func(url.Values){
		"response type": func(q url.Values) { q.Set("response_type", "token") },
		"plain pkce":    func(q url.Values) { q.Set("code_challenge_method", "plain") },
		"no challenge":  func(q url.Values) { q.Del("code_challenge") },
		"no state":      func(q url.Values) { q.Del("state") },
		"no nonce":      func(q url.Values) { q.Del("nonce") },
	} {
		q := authQuery()
		mutate(q)
		loc := authorize(t, ts, q)
		if loc.Query().Get("error") != "invalid_request" || loc.Query().Get("code") != "" {
			t.Errorf("%s: redirect = %s", name, loc)
		}
	}

	q := authQuery()
	q.Set("deny", "1")
	loc := authorize(t, ts, q)
	if loc.Query().Get("error") != "access_denied" || loc.Query().Get("state") != "st" {
		t.Errorf("deny redirect = %s", loc)
	}
}

func TestLoginAsAndPicker(t *testing.T) {
	f, ts := newFake(t)
	bob := fakegitlab.User{ID: 7, Username: "bob", Name: `Bob <script>alert(1)</script>`, Email: "bob@example.com"}
	f.AddUser(alice)
	f.AddUser(bob)

	// No user selected: an HTML page with escaped content.
	resp := get(t, ts.URL+"/oauth/authorize?"+authQuery().Encode())
	page := resp.Body
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("picker status = %d", resp.StatusCode)
	}
	if strings.Contains(page, "<script>") || !strings.Contains(page, "&lt;script&gt;") ||
		!strings.Contains(page, "login_as=alice") || !strings.Contains(page, "login_as=bob") {
		t.Errorf("picker page = %s", page)
	}

	q := authQuery()
	q.Set("login_as", "bob")
	loc := authorize(t, ts, q)
	status, body := tokenRequest(t, ts, codeForm(loc.Query().Get("code"), true), true)
	if status != http.StatusOK {
		t.Fatalf("token = %d %v", status, body)
	}
	tok, _ := jwt.ParseSigned(body["id_token"].(string), []jose.SignatureAlgorithm{jose.RS256})
	var claims map[string]any
	if err := tok.UnsafeClaimsWithoutVerification(&claims); err != nil {
		t.Fatal(err)
	}
	if claims["sub"] != "7" || claims["preferred_username"] != "bob" {
		t.Errorf("claims = %v", claims)
	}

	// Unknown login_as is refused.
	q.Set("login_as", "mallory")
	if loc := authorize(t, ts, q); loc.Query().Get("error") != "access_denied" {
		t.Errorf("unknown user redirect = %s", loc)
	}

	// An empty fake says so.
	_, empty := newFake(t)
	resp = get(t, empty.URL+"/oauth/authorize?"+authQuery().Encode())
	if !strings.Contains(resp.Body, "No users are registered.") {
		t.Errorf("empty picker = %s", resp.Body)
	}
}

func TestTokenEndpointRejections(t *testing.T) {
	f, ts := newFake(t)
	f.SetUser(alice)
	fresh := func() string { return authorize(t, ts, authQuery()).Query().Get("code") }

	cases := map[string]struct {
		mutate func(url.Values)
		basic  bool
		status int
		errMsg string
	}{
		"missing verifier": {func(v url.Values) { v.Del("code_verifier") }, true, 400, "invalid_grant"},
		"wrong verifier":   {func(v url.Values) { v.Set("code_verifier", verifier+"x") }, true, 400, "invalid_grant"},
		"wrong redirect":   {func(v url.Values) { v.Set("redirect_uri", redirectURI+"x") }, true, 400, "invalid_grant"},
		"unknown code":     {func(v url.Values) { v.Set("code", "nope") }, true, 400, "invalid_grant"},
		"wrong grant":      {func(v url.Values) { v.Set("grant_type", "password") }, true, 400, "unsupported_grant_type"},
		"bad secret form": {func(v url.Values) {
			v.Set("client_id", "cid")
			v.Set("client_secret", "wrong")
		}, false, 401, "invalid_client"},
	}
	for name, c := range cases {
		form := codeForm(fresh(), c.basic)
		c.mutate(form)
		status, body := tokenRequest(t, ts, form, c.basic)
		if status != c.status || body["error"] != c.errMsg || body["id_token"] != nil {
			t.Errorf("%s: %d %v", name, status, body)
		}
	}

	// Bad basic-auth secret.
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/oauth/token", strings.NewReader(codeForm(fresh(), true).Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("cid", "wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad basic secret status = %d", resp.StatusCode)
	}
}

func TestCodeSingleUseAndExpiry(t *testing.T) {
	f, ts := newFake(t)
	f.SetUser(alice)

	code := authorize(t, ts, authQuery()).Query().Get("code")
	if status, _ := tokenRequest(t, ts, codeForm(code, true), true); status != http.StatusOK {
		t.Fatalf("first exchange status = %d", status)
	}
	if status, body := tokenRequest(t, ts, codeForm(code, true), true); status != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Errorf("replay = %d %v", status, body)
	}

	now := time.Now()
	f.SetNow(func() time.Time { return now })
	code = authorize(t, ts, authQuery()).Query().Get("code")
	now = now.Add(61 * time.Second)
	if status, body := tokenRequest(t, ts, codeForm(code, true), true); status != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Errorf("expired code = %d %v", status, body)
	}
}

func TestHooksAndSignJWT(t *testing.T) {
	f, ts := newFake(t)
	f.SetUser(alice)
	f.SetNonceOverride("wrong")
	f.SetAudienceOverride("other")
	if err := f.UseForeignKey(); err != nil {
		t.Fatal(err)
	}
	code := authorize(t, ts, authQuery()).Query().Get("code")
	_, body := tokenRequest(t, ts, codeForm(code, true), true)
	tok, err := jwt.ParseSigned(body["id_token"].(string), []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := tok.Claims(pubKey(t, ts), &claims); err == nil {
		t.Error("foreign-key token verified with the published key")
	}
	if err := tok.UnsafeClaimsWithoutVerification(&claims); err != nil {
		t.Fatal(err)
	}
	if claims["nonce"] != "wrong" || claims["aud"] != "other" {
		t.Errorf("claims = %v", claims)
	}

	signed, err := f.SignJWT(map[string]any{"iss": "x", "custom": "y"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := jwt.ParseSigned(signed, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := parsed.Claims(pubKey(t, ts), &got); err != nil || got["custom"] != "y" {
		t.Errorf("SignJWT claims = %v, err = %v", got, err)
	}
	if parsed.Headers[0].KeyID == "" {
		t.Error("SignJWT token has no kid")
	}
}

// pubKey fetches the published signing key.
func pubKey(t *testing.T, ts *httptest.Server) jose.JSONWebKey {
	t.Helper()
	var set jose.JSONWebKeySet
	if err := json.Unmarshal([]byte(get(t, ts.URL+"/oauth/discovery/keys").Body), &set); err != nil || len(set.Keys) != 1 {
		t.Fatalf("keys: %v", err)
	}
	return set.Keys[0]
}
