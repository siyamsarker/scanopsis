// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/oauth2"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const (
	gitlabCallbackPath = "/api/v1/auth/gitlab/callback"
	oidcCookieMaxAge   = 600
	// discoveryRetryInterval is the shortest time between two discovery
	// attempts after a failure.
	discoveryRetryInterval = 10 * time.Second
	discoveryTimeout       = 10 * time.Second
	maxReturnTo            = 512
)

// Error codes the sign-in page understands (see the OpenAPI document).
const (
	loginAccessDenied    = "access_denied"
	loginExpired         = "login_expired"
	loginGroupNotAllowed = "group_not_allowed"
	loginFailed          = "login_failed"
)

// GitLabUserStore stores signed-in GitLab users. *store.Queries satisfies it.
type GitLabUserStore interface {
	UpsertGitLabUser(ctx context.Context, arg store.UpsertGitLabUserParams) (store.User, error)
}

// GitLabOptions configures GitLab sign-in.
type GitLabOptions struct {
	// IssuerURL is the GitLab base URL, which is also the OpenID Connect issuer.
	IssuerURL    string
	ClientID     string
	ClientSecret string
	// PublicURL is where Scanopsis is reached; the redirect URI is
	// PublicURL + "/api/v1/auth/gitlab/callback".
	PublicURL string
	// Secure sets the Secure attribute of the sign-in cookie.
	Secure bool
	// AllowedGroups restricts sign-in to members of these GitLab groups (full
	// paths, compared case-sensitively; members of a subgroup also match). Empty
	// allows any GitLab user.
	AllowedGroups []string
	// HTTPClient is used for discovery, keys, token and userinfo requests. Nil
	// means http.DefaultClient.
	HTTPClient *http.Client
	Now        func() time.Time
	// Logger receives warnings about failed sign-ins. Nil discards them.
	Logger *slog.Logger
}

// GitLab implements sign-in with GitLab as OpenID Connect provider.
type GitLab struct {
	opts     GitLabOptions
	users    GitLabUserStore
	sessions SessionStarter
	audit    AuditRecorder
	clientIP func(*http.Request) netip.Addr
	log      *slog.Logger
	now      func() time.Time
	client   *http.Client

	mu          sync.Mutex
	provider    *oidc.Provider
	lastAttempt time.Time
}

// NewGitLab returns a GitLab sign-in. It does not contact GitLab: discovery
// happens on the first sign-in, so the local administrator keeps working
// while GitLab is down.
func NewGitLab(opts GitLabOptions, users GitLabUserStore, sessions SessionStarter, audit AuditRecorder, clientIP func(*http.Request) netip.Addr) *GitLab {
	g := &GitLab{
		opts: opts, users: users, sessions: sessions, audit: audit, clientIP: clientIP,
		log: opts.Logger, now: opts.Now, client: opts.HTTPClient,
	}
	if g.log == nil {
		g.log = slog.New(slog.DiscardHandler)
	}
	if g.now == nil {
		g.now = time.Now
	}
	if g.client == nil {
		g.client = http.DefaultClient
	}
	return g
}

var errDiscoveryThrottled = errors.New("GitLab discovery failed recently")

// discover returns the OpenID Connect provider, discovering it on first use.
// After a failure it does not try again for discoveryRetryInterval.
func (g *GitLab) discover(ctx context.Context) (*oidc.Provider, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.provider != nil {
		return g.provider, nil
	}
	if now := g.now(); !g.lastAttempt.IsZero() && now.Sub(g.lastAttempt) < discoveryRetryInterval {
		return nil, errDiscoveryThrottled
	}
	g.lastAttempt = g.now()
	ctx, cancel := context.WithTimeout(oidc.ClientContext(ctx, g.client), discoveryTimeout)
	defer cancel()
	p, err := oidc.NewProvider(ctx, g.opts.IssuerURL)
	if err != nil {
		return nil, err
	}
	g.provider = p
	return p, nil
}

func (g *GitLab) oauthConfig(p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     g.opts.ClientID,
		ClientSecret: g.opts.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  strings.TrimSuffix(g.opts.PublicURL, "/") + gitlabCallbackPath,
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
}

// oidcCookie is the state kept in the browser between login and callback.
type oidcCookie struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	ReturnTo string `json:"r"`
}

func (g *GitLab) cookieName() string {
	if g.opts.Secure {
		return "__Host-scanopsis_oidc"
	}
	return "scanopsis_oidc"
}

func (g *GitLab) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // Secure follows the public URL scheme; false only for plain-HTTP evaluation setups
		Name: g.cookieName(), Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: g.opts.Secure, SameSite: http.SameSiteLaxMode,
	}
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// sanitizeReturnTo returns the local path and query that p names, or "/" if
// p is not a plain local path. The path is cleaned first (as http.Redirect
// would clean it) and validated afterwards, so that dot segments cannot turn
// an accepted value into a different one: "/./\host" would otherwise become
// "/\host", which browsers read as "//host" on another site. A backslash or
// a control character anywhere, raw or percent-encoded, rejects the value.
// The result is rebuilt from the parsed path and query only.
func sanitizeReturnTo(p string) string {
	if !isLocalPath(p) || len(p) > maxReturnTo {
		return "/"
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Opaque != "" ||
		!strings.HasPrefix(u.Path, "/") || strings.ContainsFunc(u.Path, unsafeInPath) {
		return "/"
	}
	clean := path.Clean(u.Path)
	if strings.HasSuffix(u.Path, "/") && clean != "/" {
		clean += "/"
	}
	out := (&url.URL{Path: clean, RawQuery: u.RawQuery}).String()
	if !isLocalPath(out) {
		return "/"
	}
	return out
}

// unsafeInPath reports characters that never appear in an accepted return
// path: browsers treat a backslash as a slash and drop tabs and newlines.
func unsafeInPath(r rune) bool { return r == '\\' || r < 0x20 || r == 0x7f }

// isLocalPath reports whether s starts with exactly one slash.
func isLocalPath(s string) bool {
	return strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") && !strings.ContainsFunc(s, unsafeInPath)
}

// LoginHandler starts sign-in: GET /api/v1/auth/gitlab/login.
func (g *GitLab) LoginHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := g.discover(r.Context())
		if err != nil {
			g.log.WarnContext(r.Context(), "GitLab discovery failed", "error", err)
			writeError(w, http.StatusServiceUnavailable, "gitlab_unavailable",
				"GitLab could not be reached. Try again in a few seconds, or sign in as the local administrator.")
			return
		}
		state, err1 := randomToken()
		nonce, err2 := randomToken()
		if err := errors.Join(err1, err2); err != nil {
			g.log.ErrorContext(r.Context(), "cannot generate sign-in secrets", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "Sign-in failed. Try again.")
			return
		}
		verifier := oauth2.GenerateVerifier()
		raw, err := json.Marshal(oidcCookie{State: state, Nonce: nonce, Verifier: verifier, ReturnTo: sanitizeReturnTo(r.URL.Query().Get("return_to"))})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Sign-in failed. Try again.")
			return
		}
		http.SetCookie(w, g.cookie(base64.RawURLEncoding.EncodeToString(raw), oidcCookieMaxAge))
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, g.oauthConfig(p).AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
	})
}

// failure describes why a callback did not sign the user in.
type failure struct {
	code     string
	err      error
	username string
	gitlabID int64
	// verified is true once the ID token was verified; only then is the
	// failure worth an audit event (before that, anyone can forge requests).
	verified bool
}

func (f *failure) Error() string { return f.code + ": " + f.err.Error() }

// CallbackHandler completes sign-in: GET /api/v1/auth/gitlab/callback.
func (g *GitLab) CallbackHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		http.SetCookie(w, g.cookie("", -1))
		w.Header().Set("Cache-Control", "no-store")

		returnTo, f := g.complete(w, r)
		if f != nil {
			g.log.WarnContext(ctx, "GitLab sign-in failed", "reason", f.code, "error", f.err)
			if f.verified {
				g.audit.Record(ctx, "gitlab:"+f.username, audit.ActionLoginFailed, map[string]any{
					"method": "gitlab", "reason": f.code, "ip": g.clientIP(r).String(),
					"username": f.username, "gitlab_user_id": f.gitlabID,
				})
			}
			http.Redirect(w, r, "/login?error="+url.QueryEscape(f.code), http.StatusFound)
			return
		}
		// The cookie is not signed: check its value again just before use.
		http.Redirect(w, r, sanitizeReturnTo(returnTo), http.StatusFound) //nolint:gosec // returnTo passed sanitizeReturnTo
	})
}

// gitlabClaims are the ID token claims Scanopsis uses.
type gitlabClaims struct {
	PreferredUsername string `json:"preferred_username"`
	Nickname          string `json:"nickname"`
	Name              string `json:"name"`
	Email             string `json:"email"`
}

func (g *GitLab) complete(w http.ResponseWriter, r *http.Request) (string, *failure) {
	q := r.URL.Query()
	if q.Get("error") == loginAccessDenied {
		return "", &failure{code: loginAccessDenied, err: errors.New("GitLab reported access_denied")}
	}

	c, err := readOIDCCookie(r, g.cookieName())
	if err != nil {
		return "", &failure{code: loginExpired, err: err}
	}
	state := q.Get("state")
	if state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(c.State)) != 1 {
		return "", &failure{code: loginExpired, err: errors.New("state does not match")}
	}
	if e := q.Get("error"); e != "" {
		return "", &failure{code: loginFailed, err: fmt.Errorf("GitLab returned error %q", e)}
	}
	code := q.Get("code")
	if code == "" {
		return "", &failure{code: loginFailed, err: errors.New("no authorization code")}
	}

	p, err := g.discover(r.Context())
	if err != nil {
		return "", &failure{code: loginFailed, err: err}
	}
	ctx := oidc.ClientContext(r.Context(), g.client)
	conf := g.oauthConfig(p)
	tok, err := conf.Exchange(ctx, code, oauth2.VerifierOption(c.Verifier))
	if err != nil {
		return "", &failure{code: loginFailed, err: fmt.Errorf("token exchange: %w", err)}
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return "", &failure{code: loginFailed, err: errors.New("token response has no id_token")}
	}
	idToken, err := p.Verifier(&oidc.Config{ClientID: g.opts.ClientID, Now: g.now}).Verify(ctx, rawID)
	if err != nil {
		return "", &failure{code: loginFailed, err: fmt.Errorf("ID token: %w", err)}
	}
	if idToken.Nonce == "" || subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(c.Nonce)) != 1 {
		return "", &failure{code: loginFailed, err: errors.New("ID token nonce does not match")}
	}
	var claims gitlabClaims
	if err := idToken.Claims(&claims); err != nil {
		return "", &failure{code: loginFailed, err: fmt.Errorf("ID token claims: %w", err)}
	}
	username := claims.PreferredUsername
	if username == "" {
		username = claims.Nickname
	}
	id, err := strconv.ParseInt(idToken.Subject, 10, 64)
	if err != nil || id <= 0 || username == "" {
		return "", &failure{code: loginFailed, err: errors.New("ID token has no valid subject or username")}
	}
	// From here on the identity is authentic: failures are audited.
	fail := func(code string, err error) (string, *failure) {
		return "", &failure{code: code, err: err, username: username, gitlabID: id, verified: true}
	}

	if len(g.opts.AllowedGroups) > 0 {
		groups, err := g.userGroups(ctx, p, tok, idToken.Subject)
		if err != nil {
			return fail(loginFailed, err)
		}
		if !groupAllowed(groups, g.opts.AllowedGroups) {
			return fail(loginGroupNotAllowed, errors.New("user is in none of the allowed groups"))
		}
	}

	user, err := g.users.UpsertGitLabUser(ctx, store.UpsertGitLabUserParams{
		GitlabUserID: pgtype.Int8{Int64: id, Valid: true},
		Username:     username,
		Name:         claims.Name,
		Email:        claims.Email,
	})
	if err != nil {
		return fail(loginFailed, fmt.Errorf("store user: %w", err))
	}
	if err := g.sessions.Start(w, r, user.ID); err != nil {
		return fail(loginFailed, fmt.Errorf("start session: %w", err))
	}
	g.audit.Record(r.Context(), "gitlab:"+username, audit.ActionLoginSucceeded,
		map[string]any{"method": "gitlab", "username": username, "gitlab_user_id": id, "ip": g.clientIP(r).String()})
	return c.ReturnTo, nil
}

// userGroups returns the full paths of the groups the user belongs to, from
// the userinfo endpoint. It refuses a response about a different subject.
func (g *GitLab) userGroups(ctx context.Context, p *oidc.Provider, tok *oauth2.Token, subject string) ([]string, error) {
	info, err := p.UserInfo(ctx, oauth2.StaticTokenSource(tok))
	if err != nil {
		return nil, fmt.Errorf("userinfo: %w", err)
	}
	if info.Subject != subject {
		return nil, errors.New("userinfo subject does not match the ID token")
	}
	var c struct {
		Groups []string `json:"groups"`
	}
	if err := info.Claims(&c); err != nil {
		return nil, fmt.Errorf("userinfo claims: %w", err)
	}
	return c.Groups, nil
}

// groupAllowed reports whether any group equals an allowed path or lies below
// one. Comparison is case-sensitive: GitLab paths are case-insensitive but
// are reported in their canonical case, which administrators must copy.
func groupAllowed(groups, allowed []string) bool {
	for _, g := range groups {
		for _, a := range allowed {
			if g == a || strings.HasPrefix(g, a+"/") {
				return true
			}
		}
	}
	return false
}

func readOIDCCookie(r *http.Request, name string) (oidcCookie, error) {
	var c oidcCookie
	ck, err := r.Cookie(name)
	if err != nil {
		return c, errors.New("sign-in cookie missing")
	}
	raw, err := base64.RawURLEncoding.DecodeString(ck.Value)
	if err != nil || json.Unmarshal(raw, &c) != nil || c.State == "" || c.Nonce == "" || c.Verifier == "" {
		return oidcCookie{}, errors.New("sign-in cookie malformed")
	}
	c.ReturnTo = sanitizeReturnTo(c.ReturnTo)
	return c, nil
}
