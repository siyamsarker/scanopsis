// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/siyamsarker/scanopsis/internal/testutil/fakegitlab"
)

const testAudience = "https://scanopsis.example.com"

// gitlab is a fake GitLab served over HTTP.
type gitlab struct {
	ts   *httptest.Server
	fake *fakegitlab.Server
	url  string
}

func newGitLab(t testing.TB) *gitlab {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	url := "http://" + ts.Listener.Addr().String()
	fake, err := fakegitlab.New(url)
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = fake.Handler()
	ts.Start()
	t.Cleanup(ts.Close)
	return &gitlab{ts: ts, fake: fake, url: url}
}

// claimsFor returns the claims of a valid token, which a test then changes.
func (g *gitlab) claims() map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": g.url, "aud": testAudience, "sub": "project_path:platform/payments-api:ref_type:branch:ref:main",
		"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nbf": now.Add(-time.Minute).Unix(),
		"project_id": "42", "project_path": "platform/payments-api", "namespace_path": "platform",
		"pipeline_id": "1001", "job_id": "2002", "ref": "main", "ref_type": "branch", "ref_protected": "true",
		"sha": "0123456789abcdef0123456789abcdef01234567", "user_login": "dev", "pipeline_source": "push",
	}
}

func (g *gitlab) token(t testing.TB, mutate func(map[string]any)) string {
	t.Helper()
	c := g.claims()
	if mutate != nil {
		mutate(c)
	}
	tok, err := g.fake.SignJWT(c)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func newVerifier() *verifier {
	return &verifier{audience: testAudience, client: http.DefaultClient, now: time.Now}
}

func TestVerifyValidToken(t *testing.T) {
	g := newGitLab(t)
	c, te := newVerifier().verify(context.Background(), g.url, "Bearer "+g.token(t, nil))
	if te != nil {
		t.Fatal(te)
	}
	want := Claims{
		ProjectID: 42, ProjectPath: "platform/payments-api", NamespacePath: "platform", PipelineID: 1001, JobID: 2002,
		Ref: "main", RefProtected: true, SHA: "0123456789abcdef0123456789abcdef01234567",
	}
	if *c != want {
		t.Fatalf("claims = %+v, want %+v", *c, want)
	}
}

func TestVerifyFailures(t *testing.T) {
	g := newGitLab(t)
	other := newGitLab(t) // another key, another issuer
	valid := g.token(t, nil)
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"missing header", "", "Authorization header is missing"},
		{"blank header", "   ", "Authorization header is missing"},
		{"wrong scheme", "Basic abc", "must be: Bearer"},
		{"no token", "Bearer", "must be: Bearer"},
		{"two tokens", "Bearer a b", "must be: Bearer"},
		{"not a jwt", "Bearer not-a-jwt", "malformed"},
		{"two parts", "Bearer aaa.bbb", "malformed"},
		{"wrong audience", "Bearer " + g.token(t, func(c map[string]any) { c["aud"] = "https://other.example.com" }), "audience (aud) does not contain " + testAudience},
		{"audience array without ours", "Bearer " + g.token(t, func(c map[string]any) { c["aud"] = []string{"a", "b"} }), "audience (aud)"},
		{"no audience", "Bearer " + g.token(t, func(c map[string]any) { delete(c, "aud") }), "audience (aud)"},
		{"wrong issuer", "Bearer " + g.token(t, func(c map[string]any) { c["iss"] = "https://gitlab.evil.example.com" }), "issuer (iss) is not the GitLab URL configured"},
		{"expired", "Bearer " + g.token(t, func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }), "has expired"},
		{"no exp", "Bearer " + g.token(t, func(c map[string]any) { delete(c, "exp") }), "has expired"},
		{"not yet valid", "Bearer " + g.token(t, func(c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() }), "not valid yet"},
		{"unknown key", "Bearer " + other.token(t, func(c map[string]any) { c["iss"] = g.url }), "signature is not valid"},
		{"tampered payload", "Bearer " + tamperPayload(t, valid), "signature is not valid"},
		{"tampered signature", "Bearer " + valid[:len(valid)-6] + "AAAAAA", "signature is not valid"},
		{"missing project_id", "Bearer " + g.token(t, func(c map[string]any) { delete(c, "project_id") }), "no project_id claim"},
		{"missing project_path", "Bearer " + g.token(t, func(c map[string]any) { delete(c, "project_path") }), "no project_path claim"},
		{"missing namespace_path", "Bearer " + g.token(t, func(c map[string]any) { delete(c, "namespace_path") }), "no namespace_path claim"},
		{"missing pipeline_id", "Bearer " + g.token(t, func(c map[string]any) { delete(c, "pipeline_id") }), "no pipeline_id claim"},
		{"missing job_id", "Bearer " + g.token(t, func(c map[string]any) { delete(c, "job_id") }), "no job_id claim"},
		{"missing ref", "Bearer " + g.token(t, func(c map[string]any) { delete(c, "ref") }), "no ref claim"},
		{"missing sha", "Bearer " + g.token(t, func(c map[string]any) { delete(c, "sha") }), "no sha claim"},
		{"missing ref_protected", "Bearer " + g.token(t, func(c map[string]any) { delete(c, "ref_protected") }), "no ref_protected claim"},
		{"project_id not a number", "Bearer " + g.token(t, func(c map[string]any) { c["project_id"] = "abc" }), "project_id must be a positive integer"},
		{"job_id zero", "Bearer " + g.token(t, func(c map[string]any) { c["job_id"] = "0" }), "job_id must be a positive integer"},
		{"pipeline_id negative", "Bearer " + g.token(t, func(c map[string]any) { c["pipeline_id"] = -5 }), "pipeline_id must be a positive integer"},
		{"sha not hex", "Bearer " + g.token(t, func(c map[string]any) { c["sha"] = "zz" + strings.Repeat("a", 38) }), "sha must be"},
		{"sha short", "Bearer " + g.token(t, func(c map[string]any) { c["sha"] = "abc123" }), "sha must be"},
		{"ref_protected junk", "Bearer " + g.token(t, func(c map[string]any) { c["ref_protected"] = "yes" }), `ref_protected must be "true" or "false"`},
		{"claim of wrong type", "Bearer " + g.token(t, func(c map[string]any) { c["ref"] = []string{"a"} }), "claims are not"},
	}
	v := newVerifier()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, te := v.verify(context.Background(), g.url, tc.header)
			if te == nil || c != nil {
				t.Fatalf("verified: %+v", c)
			}
			if !strings.Contains(te.msg, tc.want) {
				t.Errorf("message %q does not contain %q", te.msg, tc.want)
			}
			if te.unavailable {
				t.Error("marked unavailable")
			}
			if strings.Contains(te.msg, "http://") || strings.Contains(te.msg, g.url) {
				t.Errorf("message names the GitLab URL: %q", te.msg)
			}
			if strings.Contains(te.msg, tc.header) && len(tc.header) > 20 {
				t.Error("message echoes the header")
			}
		})
	}
}

func tamperPayload(t testing.TB, tok string) string {
	t.Helper()
	parts := strings.Split(tok, ".")
	// A valid base64 payload with another project_id.
	parts[1] = "eyJwcm9qZWN0X2lkIjoiOTkifQ"
	return strings.Join(parts, ".")
}

func TestVerifyAudience(t *testing.T) {
	g := newGitLab(t)
	v := newVerifier()
	// An audience array that contains ours is accepted.
	_, te := v.verify(context.Background(), g.url, "Bearer "+g.token(t, func(c map[string]any) {
		c["aud"] = []string{"https://other.example.com", testAudience}
	}))
	if te != nil {
		t.Fatalf("array containing the audience: %v", te)
	}
}

func TestVerifyRefProtected(t *testing.T) {
	g := newGitLab(t)
	v := newVerifier()
	tests := []struct {
		in   any
		want bool
	}{{"true", true}, {"false", false}, {true, true}, {false, false}}
	for _, tc := range tests {
		c, te := v.verify(context.Background(), g.url, "Bearer "+g.token(t, func(m map[string]any) { m["ref_protected"] = tc.in }))
		if te != nil || c.RefProtected != tc.want {
			t.Errorf("ref_protected %v: %+v %v", tc.in, c, te)
		}
	}
}

func TestVerifyNumericClaims(t *testing.T) {
	g := newGitLab(t)
	c, te := newVerifier().verify(context.Background(), g.url, "bearer "+g.token(t, func(m map[string]any) {
		m["project_id"], m["pipeline_id"], m["job_id"] = 7, 8, 9
	}))
	if te != nil || c.ProjectID != 7 || c.PipelineID != 8 || c.JobID != 9 {
		t.Fatalf("%+v %v", c, te)
	}
}

// testClock is a clock a test can move.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock { return &testClock{t: time.Now()} }

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func (c *testClock) verifier() *verifier {
	return &verifier{audience: testAudience, client: http.DefaultClient, now: c.now}
}

// nbf and iat in the future are refused beyond a small leeway. go-oidc alone
// accepts an nbf five minutes ahead and ignores iat.
func TestVerifyNotBeforeAndIssuedAt(t *testing.T) {
	g := newGitLab(t)
	v := newVerifier()
	tests := []struct {
		name  string
		claim string
		ahead time.Duration
		want  string // empty: accepted
	}{
		{"nbf in the past", "nbf", -time.Minute, ""},
		{"nbf now", "nbf", 0, ""},
		{"nbf a little ahead", "nbf", 5 * time.Second, ""},
		{"nbf 30 s ahead", "nbf", 30 * time.Second, "not valid yet"},
		{"nbf one minute ahead", "nbf", time.Minute, "not valid yet"},
		{"nbf five minutes ahead", "nbf", 5 * time.Minute, "not valid yet"},
		{"nbf an hour ahead", "nbf", time.Hour, "not valid yet"},
		{"iat in the past", "iat", -time.Minute, ""},
		{"iat a little ahead", "iat", 5 * time.Second, ""},
		{"iat 30 s ahead", "iat", 30 * time.Second, "issued in the future"},
		{"iat five minutes ahead", "iat", 5 * time.Minute, "issued in the future"},
		{"iat an hour ahead", "iat", time.Hour, "issued in the future"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tok := g.token(t, func(c map[string]any) { c[tc.claim] = time.Now().Add(tc.ahead).Unix() })
			_, te := v.verify(context.Background(), g.url, "Bearer "+tok)
			switch {
			case tc.want == "" && te != nil:
				t.Fatalf("refused: %v", te)
			case tc.want != "" && (te == nil || !strings.Contains(te.msg, tc.want) || te.unavailable):
				t.Fatalf("got %v, want a message with %q", te, tc.want)
			}
		})
	}
	// Tokens without nbf or iat are not refused for it.
	if _, te := v.verify(context.Background(), g.url, "Bearer "+g.token(t, func(c map[string]any) { delete(c, "nbf"); delete(c, "iat") })); te != nil {
		t.Fatalf("no nbf or iat: %v", te)
	}
	// The verifier's clock decides, not the host's.
	clock := newTestClock()
	clock.advance(2 * time.Hour)
	if _, te := clock.verifier().verify(context.Background(), g.url, "Bearer "+g.token(t, nil)); te == nil || !strings.Contains(te.msg, "expired") {
		t.Fatalf("injected clock ignored: %v", te)
	}
	clock = newTestClock()
	clock.advance(-time.Hour)
	if _, te := clock.verifier().verify(context.Background(), g.url, "Bearer "+g.token(t, nil)); te == nil || !strings.Contains(te.msg, "not valid yet") {
		t.Fatalf("a clock behind the token: %v", te)
	}
}

// rotatingGitLab serves a JWKS that a test can replace.
type rotatingGitLab struct {
	ts   *httptest.Server
	mu   sync.Mutex
	keys []jose.JSONWebKey
	hits int
	fail bool
}

func newRotating(t testing.TB) *rotatingGitLab {
	t.Helper()
	r := &rotatingGitLab{}
	r.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.hits++
		if r.fail {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		b, _ := json.Marshal(jose.JSONWebKeySet{Keys: r.keys})
		_, _ = w.Write(b)
	}))
	t.Cleanup(r.ts.Close)
	return r
}

func (r *rotatingGitLab) newKey(t testing.TB, kid string) jose.Signer {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: k, KeyID: kid}}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.keys = append(r.keys, jose.JSONWebKey{Key: &k.PublicKey, KeyID: kid, Algorithm: "RS256", Use: "sig"})
	r.mu.Unlock()
	return s
}

func (r *rotatingGitLab) publish(kids ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var keep []jose.JSONWebKey
	for _, k := range r.keys {
		for _, kid := range kids {
			if k.KeyID == kid {
				keep = append(keep, k)
			}
		}
	}
	r.keys = keep
}

func (r *rotatingGitLab) sign(t testing.TB, s jose.Signer, c map[string]any) string {
	t.Helper()
	tok, err := jwt.Signed(s).Claims(c).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestVerifyKeyRotation(t *testing.T) {
	r := newRotating(t)
	g := &gitlab{url: r.ts.URL}
	s1 := r.newKey(t, "key-1")
	clock := newTestClock()
	v := clock.verifier()
	ctx := context.Background()

	t1 := r.sign(t, s1, g.claims())
	if _, te := v.verify(ctx, g.url, "Bearer "+t1); te != nil {
		t.Fatal(te)
	}
	clock.advance(keyRefetchInterval + time.Second)

	// GitLab rotates: a token with an unknown kid is checked against a fresh
	// copy of the key set, and then verifies.
	s2 := r.newKey(t, "key-2")
	r.publish("key-2")
	before := r.hits
	if _, te := v.verify(ctx, g.url, "Bearer "+r.sign(t, s2, g.claims())); te != nil {
		t.Fatalf("after rotation: %v", te)
	}
	if r.hits != before+1 {
		t.Errorf("key set fetched %d times for the unknown kid, want 1", r.hits-before)
	}
	// The retired key no longer verifies.
	if _, te := v.verify(ctx, g.url, "Bearer "+t1); te == nil || !strings.Contains(te.msg, "signature is not valid") {
		t.Errorf("retired key accepted: %v", te)
	}
}

func TestVerifyUnknownKidBeforePublication(t *testing.T) {
	r := newRotating(t)
	g := &gitlab{url: r.ts.URL}
	r.newKey(t, "key-1")
	s2 := r.newKey(t, "key-2")
	k2 := r.keys[1]
	r.publish("key-1") // key-2 is not published yet
	clock := newTestClock()
	v := clock.verifier()
	tok := r.sign(t, s2, g.claims())
	if _, te := v.verify(context.Background(), g.url, "Bearer "+tok); te == nil || !strings.Contains(te.msg, "signature is not valid") {
		t.Fatalf("unpublished key accepted: %v", te)
	}
	// Once GitLab publishes the key, the same token verifies, as soon as the
	// keys may be fetched again.
	r.mu.Lock()
	r.keys = append(r.keys, k2)
	r.mu.Unlock()
	if _, te := v.verify(context.Background(), g.url, "Bearer "+tok); te == nil {
		t.Fatal("the keys were fetched again inside the interval")
	}
	clock.advance(keyRefetchInterval)
	if _, te := v.verify(context.Background(), g.url, "Bearer "+tok); te != nil {
		t.Fatalf("after publication: %v", te)
	}
}

func TestVerifyKeysUnavailable(t *testing.T) {
	g := newGitLab(t)
	tok := g.token(t, nil)
	g.ts.Close()
	_, te := newVerifier().verify(context.Background(), g.url, "Bearer "+tok)
	if te == nil || !te.unavailable || !strings.Contains(te.msg, "could not fetch the signing keys") {
		t.Fatalf("%v", te)
	}
	if strings.Contains(te.msg, g.url) || strings.Contains(te.msg, "discovery") {
		t.Errorf("message names the GitLab URL: %q", te.msg)
	}
	if !strings.Contains(te.detail, g.url+"/oauth/discovery/keys") {
		t.Errorf("log detail %q lacks the keys URL", te.detail)
	}
}

func TestVerifierFollowsGitLabURL(t *testing.T) {
	a, b := newGitLab(t), newGitLab(t)
	v := newVerifier()
	if _, te := v.verify(context.Background(), a.url, "Bearer "+a.token(t, nil)); te != nil {
		t.Fatal(te)
	}
	// After the URL setting changes, tokens of the old GitLab are refused and
	// tokens of the new one accepted.
	if _, te := v.verify(context.Background(), b.url, "Bearer "+a.token(t, nil)); te == nil {
		t.Fatal("token of the old GitLab accepted")
	}
	if _, te := v.verify(context.Background(), b.url, "Bearer "+b.token(t, nil)); te != nil {
		t.Fatal(te)
	}
}

func TestNamespaceAllowed(t *testing.T) {
	tests := []struct {
		allowed []string
		ns      string
		want    bool
	}{
		{[]string{"platform"}, "platform", true},
		{[]string{"platform"}, "platform/payments", true},
		{[]string{"platform"}, "platform/payments/deep", true},
		{[]string{"platform"}, "platformx", false},
		{[]string{"platform"}, "other/platform", false},
		{[]string{"platform/payments"}, "platform", false},
		{[]string{"platform/payments"}, "platform/payments", true},
		{[]string{"a", "platform"}, "platform/x", true},
		{[]string{"Platform/"}, "platform/x", true},
		{[]string{" /platform "}, "PLATFORM", true},
		{[]string{""}, "platform", false},
		{nil, "platform", false},
	}
	for _, tc := range tests {
		if got := namespaceAllowed(tc.allowed, tc.ns); got != tc.want {
			t.Errorf("namespaceAllowed(%q, %q) = %v, want %v", tc.allowed, tc.ns, got, tc.want)
		}
	}
}

// forged returns a token that claims a key ID, signed with a key GitLab does
// not publish.
func forged(t testing.TB, kid string, claims map[string]any) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: k, KeyID: kid}}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := jwt.Signed(s).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// Forged tokens do not make Scanopsis fetch GitLab's keys again and again.
func TestVerifyKeyFetchesAreLimited(t *testing.T) {
	r := newRotating(t)
	g := &gitlab{url: r.ts.URL}
	s1 := r.newKey(t, "key-1")
	clock := newTestClock()
	v := clock.verifier()
	ctx := context.Background()
	good := r.sign(t, s1, g.claims())
	if _, te := v.verify(ctx, g.url, "Bearer "+good); te != nil {
		t.Fatal(te)
	}
	if r.hits != 1 {
		t.Fatalf("first verification fetched %d times", r.hits)
	}

	// An unknown key ID, many times, inside the interval: no fetch.
	for i := range 30 {
		tok := forged(t, "unknown-"+strconv.Itoa(i), g.claims())
		if _, te := v.verify(ctx, g.url, "Bearer "+tok); te == nil || !strings.Contains(te.msg, "signature is not valid") || te.unavailable {
			t.Fatalf("forged token: %v", te)
		}
	}
	if r.hits != 1 {
		t.Errorf("%d fetches for forged tokens inside the interval, want 1 in all", r.hits)
	}

	// After the interval one unknown key ID makes one fetch, and the next ones
	// do not.
	clock.advance(keyRefetchInterval)
	for i := range 30 {
		if _, te := v.verify(ctx, g.url, "Bearer "+forged(t, "other-"+strconv.Itoa(i), g.claims())); te == nil {
			t.Fatal("forged token accepted")
		}
	}
	if r.hits != 2 {
		t.Errorf("%d fetches after the interval, want 2 in all", r.hits)
	}

	// A bad signature on a key ID that is in the cached set never fetches,
	// even long after the last fetch: the token is wrong, not the cache.
	clock.advance(30 * time.Minute)
	damaged := good[:len(good)-6] + "AAAAAA"
	for range 30 {
		if _, te := v.verify(ctx, g.url, "Bearer "+damaged); te == nil || !strings.Contains(te.msg, "signature is not valid") {
			t.Fatalf("damaged token: %v", te)
		}
		if _, te := v.verify(ctx, g.url, "Bearer "+forged(t, "key-1", g.claims())); te == nil {
			t.Fatal("token signed with another key under a known key ID accepted")
		}
	}
	if r.hits != 2 {
		t.Errorf("%d fetches for bad signatures of a known key, want 2 in all", r.hits)
	}
	// Valid tokens are still verified from the cache.
	if _, te := v.verify(ctx, g.url, "Bearer "+good); te != nil || r.hits != 2 {
		t.Errorf("valid token: %v, %d fetches", te, r.hits)
	}
}

// A GitLab that cannot be reached is asked once in the interval: a token with
// an unknown key ID in that time is answered "unavailable" again, without
// another request, and the first request after it asks again.
func TestVerifyKeyFetchFailuresAreLimited(t *testing.T) {
	r := newRotating(t)
	g := &gitlab{url: r.ts.URL}
	s1 := r.newKey(t, "key-1")
	r.fail = true
	clock := newTestClock()
	v := clock.verifier()
	ctx := context.Background()
	tok := r.sign(t, s1, g.claims())
	for i := range 5 {
		_, te := v.verify(ctx, g.url, "Bearer "+tok)
		if te == nil || !te.unavailable || !strings.Contains(te.msg, "could not fetch the signing keys") {
			t.Fatalf("attempt %d: %v", i, te)
		}
	}
	if r.hits != 1 {
		t.Errorf("%d requests to a failing GitLab inside the interval, want 1", r.hits)
	}
	clock.advance(keyRefetchInterval)
	if _, te := v.verify(ctx, g.url, "Bearer "+tok); te == nil || !te.unavailable {
		t.Fatalf("%v", te)
	}
	if r.hits != 2 {
		t.Errorf("%d requests after the interval, want 2", r.hits)
	}
	// GitLab comes back: the next attempt after the interval works.
	r.mu.Lock()
	r.fail = false
	r.mu.Unlock()
	clock.advance(keyRefetchInterval)
	if _, te := v.verify(ctx, g.url, "Bearer "+tok); te != nil {
		t.Fatalf("after recovery: %v", te)
	}
}

// Keys that were fetched stay in use when a later fetch fails.
func TestVerifyKeepsKeysWhenARefreshFails(t *testing.T) {
	r := newRotating(t)
	g := &gitlab{url: r.ts.URL}
	s1 := r.newKey(t, "key-1")
	clock := newTestClock()
	v := clock.verifier()
	ctx := context.Background()
	tok := r.sign(t, s1, g.claims())
	if _, te := v.verify(ctx, g.url, "Bearer "+tok); te != nil {
		t.Fatal(te)
	}
	clock.advance(30 * time.Minute)
	r.mu.Lock()
	r.fail = true
	r.mu.Unlock()
	// A token of a key that is cached needs no fetch.
	if _, te := v.verify(ctx, g.url, "Bearer "+tok); te != nil {
		t.Fatalf("cached key not used: %v", te)
	}
	// A token with an unknown key ID tries a fetch, which fails.
	if _, te := v.verify(ctx, g.url, "Bearer "+forged(t, "new", g.claims())); te == nil || !te.unavailable {
		t.Fatalf("%v", te)
	}
	if _, te := v.verify(ctx, g.url, "Bearer "+tok); te != nil {
		t.Fatalf("cached keys lost after a failed refresh: %v", te)
	}
}
