// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

// Package fakegitlab is a fake GitLab OpenID Connect provider for tests and
// end-to-end runs. It implements only what Scanopsis uses: discovery, the
// JSON Web Key Set, the authorization code flow with PKCE, the token
// endpoint and the userinfo endpoint. It must never be used outside tests.
package fakegitlab

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const (
	codeLife        = 60 * time.Second
	accessTokenLife = 2 * time.Hour
	idTokenLife     = 10 * time.Minute
)

// User is a GitLab account known to the fake.
type User struct {
	ID       int64
	Username string
	Name     string
	Email    string
	// Groups are full paths of the groups the user belongs to, such as
	// "platform" or "security/appsec".
	Groups []string
}

type client struct{ secret, redirectURI string }

type authCode struct {
	clientID, redirectURI, challenge, nonce string
	user                                    User
	expires                                 time.Time
}

type accessToken struct {
	user    User
	expires time.Time
}

// Server is a fake GitLab. Create it with New.
type Server struct {
	issuer string
	kid    string
	signer jose.Signer
	jwk    jose.JSONWebKey

	mu       sync.Mutex
	clients  map[string]client
	users    map[string]User
	current  *User
	codes    map[string]authCode
	tokens   map[string]accessToken
	now      func() time.Time
	nonce    *string
	audience *string
	foreign  jose.Signer
	userinfo int
	testAPI  bool
}

// New returns a Server whose issuer is issuerURL (no trailing slash). It
// generates a fresh RSA-2048 signing key.
func New(issuerURL string) (*Server, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	kid, err := randomString(8)
	if err != nil {
		return nil, err
	}
	signer, err := newSigner(key, kid)
	if err != nil {
		return nil, err
	}
	return &Server{
		issuer: strings.TrimSuffix(issuerURL, "/"),
		kid:    kid,
		signer: signer,
		jwk:    jose.JSONWebKey{Key: &key.PublicKey, KeyID: kid, Algorithm: string(jose.RS256), Use: "sig"},

		clients: map[string]client{},
		users:   map[string]User{},
		codes:   map[string]authCode{},
		tokens:  map[string]accessToken{},
		now:     time.Now,
	}, nil
}

func newSigner(key *rsa.PrivateKey, kid string) (jose.Signer, error) {
	return jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: kid}},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
}

// IssuerURL returns the issuer the fake was created with.
func (s *Server) IssuerURL() string { return s.issuer }

// AddClient registers an OAuth client with one exact redirect URI.
func (s *Server) AddClient(id, secret, redirectURI string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[id] = client{secret: secret, redirectURI: redirectURI}
}

// AddUser registers a user who can be selected with login_as.
func (s *Server) AddUser(u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[u.Username] = u
}

// SetUser registers u and makes it the user the next authorization request
// signs in.
func (s *Server) SetUser(u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[u.Username] = u
	s.current = &u
}

// SignJWT signs claims with the server key (RS256, with its kid).
func (s *Server) SignJWT(claims map[string]any) (string, error) {
	return sign(s.signer, claims)
}

func sign(signer jose.Signer, claims map[string]any) (string, error) {
	return jwt.Signed(signer).Claims(claims).Serialize()
}

// Test hooks. They make the fake misbehave in controlled ways.

// SetNow replaces the clock used for token lifetimes.
func (s *Server) SetNow(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// SetNonceOverride makes ID tokens carry nonce instead of the requested one.
func (s *Server) SetNonceOverride(nonce string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nonce = &nonce
}

// SetAudienceOverride makes ID tokens carry aud instead of the client ID.
func (s *Server) SetAudienceOverride(aud string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audience = &aud
}

// UseForeignKey makes ID tokens be signed with a different RSA key that
// carries the published kid, as an attacker's token would.
func (s *Server) UseForeignKey() error {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	signer, err := newSigner(key, s.kid)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.foreign = signer
	return nil
}

// UserinfoCalls returns how many successful userinfo requests were served.
func (s *Server) UserinfoCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.userinfo
}

// EnableTestEndpoints turns on POST /test/ci-token, which signs the posted
// claims as a CI ID token. It is off by default so that the fake never hands
// out tokens unless the test run asks for it.
func (s *Server) EnableTestEndpoints() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.testAPI = true
}

// Handler serves the provider's endpoints.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", s.discovery)
	mux.HandleFunc("GET /oauth/discovery/keys", s.keys)
	mux.HandleFunc("GET /oauth/authorize", s.authorize)
	mux.HandleFunc("POST /oauth/token", s.token)
	mux.HandleFunc("GET /oauth/userinfo", s.userInfo)
	mux.HandleFunc("POST /oauth/userinfo", s.userInfo)
	mux.HandleFunc("POST /test/ci-token", s.ciToken)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func (s *Server) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                s.issuer,
		"authorization_endpoint":                s.issuer + "/oauth/authorize",
		"token_endpoint":                        s.issuer + "/oauth/token",
		"userinfo_endpoint":                     s.issuer + "/oauth/userinfo",
		"jwks_uri":                              s.issuer + "/oauth/discovery/keys",
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"S256"},
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"scopes_supported":                      []string{"openid", "profile", "email"},
	})
}

func (s *Server) keys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{s.jwk}})
}

func randomString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func redirectWith(w http.ResponseWriter, r *http.Request, base string, params url.Values) {
	u, err := url.Parse(base)
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	q := u.Query()
	for k, v := range params {
		q[k] = v
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

var pickerPage = template.Must(template.New("picker").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Fake GitLab</title></head>
<body><h1>Fake GitLab sign-in</h1>
<ul>{{range .}}<li><a href="{{.Href}}">{{.Username}}</a> ({{.Name}}, {{.Email}})</li>
{{else}}<li>No users are registered.</li>{{end}}</ul></body></html>`))

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	c, ok := s.clients[q.Get("client_id")]
	s.mu.Unlock()
	// Without a valid client and redirect URI the request must not redirect.
	if !ok || q.Get("redirect_uri") != c.redirectURI {
		http.Error(w, "invalid client_id or redirect_uri", http.StatusBadRequest)
		return
	}
	state := q.Get("state")
	fail := func(code string) {
		redirectWith(w, r, c.redirectURI, url.Values{"error": {code}, "state": {state}})
	}
	if q.Get("deny") == "1" {
		fail("access_denied")
		return
	}
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" ||
		q.Get("code_challenge") == "" || state == "" || q.Get("nonce") == "" {
		fail("invalid_request")
		return
	}

	s.mu.Lock()
	var user *User
	loginAs := q.Get("login_as")
	if loginAs != "" {
		if u, ok := s.users[loginAs]; ok {
			user = &u
		}
	} else {
		user = s.current
	}
	if user == nil && loginAs == "" {
		// Nobody selected: list the registered users.
		type link struct{ Href, Username, Name, Email string }
		names := make([]string, 0, len(s.users))
		for n := range s.users {
			names = append(names, n)
		}
		sort.Strings(names)
		links := make([]link, 0, len(names))
		for _, n := range names {
			u := s.users[n]
			v := url.Values{}
			for k, vals := range q {
				v[k] = vals
			}
			v.Set("login_as", n)
			links = append(links, link{"/oauth/authorize?" + v.Encode(), u.Username, u.Name, u.Email})
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pickerPage.Execute(w, links)
		return
	}
	if user == nil {
		s.mu.Unlock()
		fail("access_denied")
		return
	}
	code, err := randomString(16)
	if err != nil {
		s.mu.Unlock()
		fail("server_error")
		return
	}
	s.codes[code] = authCode{
		clientID: q.Get("client_id"), redirectURI: c.redirectURI,
		challenge: q.Get("code_challenge"), nonce: q.Get("nonce"),
		user: *user, expires: s.now().Add(codeLife),
	}
	s.mu.Unlock()
	redirectWith(w, r, c.redirectURI, url.Values{"code": {code}, "state": {state}})
}

func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func clientCredentials(r *http.Request) (id, secret string) {
	if u, p, ok := r.BasicAuth(); ok {
		id, _ = url.QueryUnescape(u)
		secret, _ = url.QueryUnescape(p)
		return id, secret
	}
	return r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	id, secret := clientCredentials(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.clients[id]
	if !ok || !equal(c.secret, secret) {
		oauthError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	if r.PostForm.Get("grant_type") != "authorization_code" {
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	code := r.PostForm.Get("code")
	ac, ok := s.codes[code]
	delete(s.codes, code) // single use, even when the rest fails
	now := s.now()
	if !ok || ac.clientID != id || now.After(ac.expires) || r.PostForm.Get("redirect_uri") != ac.redirectURI {
		oauthError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	verifier := r.PostForm.Get("code_verifier")
	sum := sha256.Sum256([]byte(verifier))
	if verifier == "" || !equal(base64.RawURLEncoding.EncodeToString(sum[:]), ac.challenge) {
		oauthError(w, http.StatusBadRequest, "invalid_grant")
		return
	}

	u := ac.user
	nonce, aud := ac.nonce, id
	if s.nonce != nil {
		nonce = *s.nonce
	}
	if s.audience != nil {
		aud = *s.audience
	}
	signer := s.signer
	if s.foreign != nil {
		signer = s.foreign
	}
	idToken, err := sign(signer, map[string]any{
		"iss": s.issuer, "sub": strconv.FormatInt(u.ID, 10), "aud": aud,
		"exp": now.Add(idTokenLife).Unix(), "iat": now.Unix(), "nonce": nonce,
		"name": u.Name, "nickname": u.Username, "preferred_username": u.Username,
		"email": u.Email, "email_verified": true, "groups_direct": groupsOrEmpty(u.Groups),
	})
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error")
		return
	}
	at, err := randomString(16)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error")
		return
	}
	s.tokens[at] = accessToken{user: u, expires: now.Add(accessTokenLife)}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": at, "token_type": "Bearer",
		"expires_in": int(accessTokenLife.Seconds()), "id_token": idToken,
	})
}

func groupsOrEmpty(g []string) []string {
	if g == nil {
		return []string{}
	}
	return g
}

var errNoToken = errors.New("no bearer token")

func bearer(r *http.Request) (string, error) {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return h[7:], nil
	}
	return "", errNoToken
}

func (s *Server) userInfo(w http.ResponseWriter, r *http.Request) {
	tok, err := bearer(r)
	s.mu.Lock()
	t, ok := s.tokens[tok]
	valid := err == nil && ok && !s.now().After(t.expires)
	if valid {
		s.userinfo++
	}
	s.mu.Unlock()
	if !valid {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		oauthError(w, http.StatusUnauthorized, "invalid_token")
		return
	}
	u := t.user
	writeJSON(w, http.StatusOK, map[string]any{
		"sub": strconv.FormatInt(u.ID, 10), "name": u.Name, "nickname": u.Username,
		"preferred_username": u.Username, "email": u.Email, "groups": groupsOrEmpty(u.Groups),
	})
}

// ciToken signs the posted JSON claims. iss defaults to the issuer, iat to
// now and exp to ten minutes from now; every other claim is taken as posted.
func (s *Server) ciToken(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	enabled, now := s.testAPI, s.now()
	s.mu.Unlock()
	if !enabled {
		http.NotFound(w, r)
		return
	}
	var claims map[string]any
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := dec.Decode(&claims); err != nil || claims == nil {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if _, ok := claims["iss"]; !ok {
		claims["iss"] = s.issuer
	}
	if _, ok := claims["iat"]; !ok {
		claims["iat"] = now.Unix()
	}
	if _, ok := claims["exp"]; !ok {
		claims["exp"] = now.Add(idTokenLife).Unix()
	}
	tok, err := s.SignJWT(claims)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": tok})
}
