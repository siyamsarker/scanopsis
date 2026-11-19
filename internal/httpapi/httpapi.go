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
	// ResetMFA wraps store.ResetMFA; it returns store.ErrNotFound for an
	// unknown user.
	ResetMFA(ctx context.Context, id int64) error
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
	// Methods, when set, reports which sign-in methods are currently
	// enabled; it overrides the nil checks above for GET
	// /api/v1/auth/methods, so settings changed at run time are reflected.
	Methods func() (gitlab, local bool)

	// Handlers provided by other packages. All but Setup may be nil when the
	// feature is unavailable; the routes then answer 404 not_enabled. They
	// receive the principal in the request context where the route needs a
	// session.
	Setup                http.Handler // GET and POST /api/v1/setup, public
	MFAStartEnrollment   http.Handler
	MFAConfirmEnrollment http.Handler
	MFAVerify            http.Handler
	PasswordChange       http.Handler
	SettingsList         http.Handler
	SettingsUpdate       http.Handler

	// Features maps OpenAPI operation IDs (for example "ingestTrivy",
	// "getPortfolio") to the handlers of the scan and SonarQube endpoints.
	// The router applies the session and role checks; a missing entry
	// answers 404 not_enabled.
	Features map[string]http.Handler

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

// feature returns the handler registered for an operation ID, or a 404
// not_enabled handler when there is none.
func (s *server) feature(operationID string) http.Handler {
	return s.optional(s.Features[operationID], "This feature")
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
		{"GET", "/api/v1/me", s.requireFirstFactor(http.HandlerFunc(s.me))},
		{"POST", "/api/v1/me/password", s.requireMFA(s.optional(s.PasswordChange, "Password change"))},
		{"GET", "/api/v1/setup", s.optional(s.Setup, "Setup")},
		{"POST", "/api/v1/setup", s.optional(s.Setup, "Setup")},
		{"POST", "/api/v1/auth/mfa/verify", s.requireFirstFactor(s.optional(s.MFAVerify, "Two-factor authentication"))},
		{"POST", "/api/v1/auth/mfa/enrollment", s.requireFirstFactor(s.optional(s.MFAStartEnrollment, "Two-factor authentication"))},
		{"POST", "/api/v1/auth/mfa/enrollment/confirm", s.requireFirstFactor(s.optional(s.MFAConfirmEnrollment, "Two-factor authentication"))},
		{"GET", "/api/v1/settings", s.requireAdmin(s.optional(s.SettingsList, "Settings"))},
		{"PATCH", "/api/v1/settings", s.requireAdmin(s.optional(s.SettingsUpdate, "Settings"))},

		// Uploads and webhooks authenticate themselves (CI ID token, HMAC).
		{"POST", "/api/v1/ingest/trivy", s.feature("ingestTrivy")},
		{"POST", "/api/v1/hooks/sonarqube", s.feature("sonarWebhook")},

		{"GET", "/api/v1/portfolio", s.requireComplete(s.feature("getPortfolio"))},
		{"GET", "/api/v1/services", s.requireComplete(s.feature("listServices"))},
		{"POST", "/api/v1/services", s.requireAdmin(s.feature("createService"))},
		{"GET", "/api/v1/services/{id}", s.requireComplete(s.feature("getService"))},
		{"PATCH", "/api/v1/services/{id}", s.requireAdmin(s.feature("updateService"))},
		{"DELETE", "/api/v1/services/{id}", s.requireAdmin(s.feature("deleteService"))},
		{"GET", "/api/v1/services/{id}/overview", s.requireComplete(s.feature("getServiceOverview"))},
		{"GET", "/api/v1/services/{id}/scans", s.requireComplete(s.feature("listServiceScans"))},
		{"GET", "/api/v1/scans/{id}", s.requireComplete(s.feature("getScan"))},
		{"GET", "/api/v1/scans/{id}/diff", s.requireComplete(s.feature("getScanDiff"))},
		{"GET", "/api/v1/services/{id}/vulnerabilities", s.requireComplete(s.feature("listServiceVulnerabilities"))},
		{"GET", "/api/v1/services/{id}/secrets", s.requireComplete(s.feature("listServiceSecrets"))},
		{"GET", "/api/v1/services/{id}/trends", s.requireComplete(s.feature("getServiceTrends"))},
		{"GET", "/api/v1/services/{id}/sonar", s.requireComplete(s.feature("getServiceSonar"))},
		{"GET", "/api/v1/search/vulnerabilities", s.requireComplete(s.feature("searchVulnerabilities"))},
		{"GET", "/api/v1/admin/audit", s.requireAdmin(s.feature("listAuditEvents"))},
		{"GET", "/api/v1/admin/integrations/trivy", s.requireAdmin(s.feature("getTrivyIntegration"))},
		{"GET", "/api/v1/admin/integrations/sonarqube", s.requireAdmin(s.feature("getSonarIntegration"))},
		{"DELETE", "/api/v1/users/{id}/mfa", s.requireAdmin(http.HandlerFunc(s.resetMFA))},
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
