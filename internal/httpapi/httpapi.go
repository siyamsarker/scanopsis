// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Package httpapi builds the HTTP handler of the server: the JSON API, the
// probes and the embedded web interface, with the middleware they share.
package httpapi

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/store"
)

// Authenticator resolves and ends sessions. *auth.Sessions implements it.
type Authenticator interface {
	// Authenticate returns the principal of the request, or an error when
	// the request carries no valid session.
	Authenticate(r *http.Request) (*auth.Principal, error)
	// End deletes the request's session, if any, and clears the cookie.
	End(w http.ResponseWriter, r *http.Request) error
}

// UserStore is the user persistence the API needs.
type UserStore interface {
	ListUsers(ctx context.Context, arg store.ListUsersParams) ([]store.User, error)
	GetUser(ctx context.Context, id int64) (store.User, error)
	// SetUserRole wraps store.SetUserRole.
	SetUserRole(ctx context.Context, id int64, role string) (store.User, error)
}

// Pinger checks that the database is reachable. *pgxpool.Pool implements it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// BuildInfo identifies the running build.
type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Go      string `json:"go"`
}

// Deps are the collaborators of the handler.
type Deps struct {
	Log      *slog.Logger
	DB       Pinger
	Users    UserStore
	Sessions Authenticator
	Audit    auth.AuditRecorder
	ClientIP func(*http.Request) netip.Addr

	LocalLogin     http.Handler // nil when local sign-in is disabled
	GitLabLogin    http.Handler // nil when GitLab sign-in is not configured
	GitLabCallback http.Handler

	Assets fs.FS // built web interface rooted at index.html; nil when not built
	Build  BuildInfo
	Secure bool // the public URL is https
}

type route struct {
	method  string
	pattern string // path, with {name} wildcards as in the OpenAPI document
	handler http.Handler
}

type server struct {
	Deps
}

// New returns the handler serving the API, the probes and the web interface.
func New(d Deps) http.Handler {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	s := &server{Deps: d}
	return s.handler(s.routes())
}

// routes is the single list of API and probe routes. The contract test
// compares it with api/openapi.yaml.
func (s *server) routes() []route {
	return []route{
		{"GET", "/healthz", http.HandlerFunc(s.healthz)},
		{"GET", "/readyz", http.HandlerFunc(s.readyz)},
		{"GET", "/api/v1/version", http.HandlerFunc(s.version)},
		{"GET", "/api/v1/auth/methods", http.HandlerFunc(s.authMethods)},
		{"GET", "/api/v1/auth/gitlab/login", s.optional(s.GitLabLogin, "GitLab")},
		{"GET", "/api/v1/auth/gitlab/callback", s.optional(s.GitLabCallback, "GitLab")},
		{"POST", "/api/v1/auth/local/login", s.optional(s.LocalLogin, "Local")},
		{"POST", "/api/v1/auth/logout", http.HandlerFunc(s.logout)},
		{"GET", "/api/v1/me", s.requireSession(http.HandlerFunc(s.me))},
		{"GET", "/api/v1/users", s.requireAdmin(http.HandlerFunc(s.listUsers))},
		{"PATCH", "/api/v1/users/{id}", s.requireAdmin(http.HandlerFunc(s.updateUser))},
	}
}

func (s *server) handler(routes []route) http.Handler {
	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.Handle(rt.method+" "+rt.pattern, rt.handler)
	}
	mux.Handle("/api/", apiFallback(routes))
	mux.Handle("/", s.static())

	csrf := http.NewCrossOriginProtection()
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusForbidden, "cross_origin", "Cross-origin request rejected.")
	}))

	var h http.Handler = mux
	h = limitBody(h)
	h = csrf.Handler(h)
	h = s.securityHeaders(h)
	h = s.logRequests(h)
	return s.recoverPanics(h)
}
