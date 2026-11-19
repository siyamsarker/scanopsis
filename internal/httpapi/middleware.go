// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/siyamsarker/scanopsis/internal/auth"
)

const (
	maxAPIBody = 1 << 20

	contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
		"font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; " +
		"form-action 'self'; frame-ancestors 'none'"
)

// statusRecorder remembers the status code written to the response.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does.
				panic(v)
			}
			s.Log.ErrorContext(r.Context(), "panic while handling request",
				"method", r.Method, "path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
			writeError(w, http.StatusInternalServerError, "internal_error", "Something went wrong. Try again later.")
		}()
		next.ServeHTTP(w, r)
	})
}

// logRequests logs one line per request. It records the path without the
// query string and never headers or cookies.
func (s *server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		defer func() {
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			ip := ""
			if s.ClientIP != nil {
				if a := s.ClientIP(r); a.IsValid() {
					ip = a.String()
				}
			}
			s.Log.InfoContext(r.Context(), "request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", status,
				"duration_ms", time.Since(start).Milliseconds(),
				"client_ip", ip)
		}()
		next.ServeHTTP(rec, r)
	})
}

func (s *server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if s.Secure {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// limitBody caps request bodies below /api. Handlers that accept less apply
// their own, smaller limit.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxAPIBody)
		}
		next.ServeHTTP(w, r)
	})
}

// requireFirstFactor rejects requests without a valid session and otherwise
// passes the principal to next through the request context. The session may
// still lack the second factor or have a password change pending.
func (s *server) requireFirstFactor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := s.Sessions.Authenticate(r)
		if err != nil && !errors.Is(err, auth.ErrNoSession) {
			// A failing session store is a server problem, not a signed-out user.
			s.internalError(w, r, "cannot check session", err)
			return
		}
		if err != nil || p == nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "Sign in to continue.")
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
	})
}

// requireMFA is requireFirstFactor plus a verified second factor. A pending
// password change does not matter: it is how the user resolves it.
func (s *server) requireMFA(next http.Handler) http.Handler {
	return s.requireFirstFactor(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, _ := auth.PrincipalFrom(r.Context()); !p.MFAVerified {
			writeIncomplete(w, p)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// requireComplete is requireFirstFactor plus a complete session: second
// factor verified and no password change pending.
func (s *server) requireComplete(next http.Handler) http.Handler {
	return s.requireFirstFactor(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, _ := auth.PrincipalFrom(r.Context()); !p.Complete() {
			writeIncomplete(w, p)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// requireAdmin is requireComplete plus the admin role.
func (s *server) requireAdmin(next http.Handler) http.Handler {
	return s.requireComplete(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, _ := auth.PrincipalFrom(r.Context()); !p.IsAdmin() {
			writeError(w, http.StatusForbidden, "forbidden", "This action requires the admin role.")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// writeIncomplete answers 403 for a session that cannot yet use the
// application, naming the next step.
func writeIncomplete(w http.ResponseWriter, p *auth.Principal) {
	switch {
	case !p.MFAEnrolled:
		writeError(w, http.StatusForbidden, "mfa_enrollment_required", "Set up two-factor authentication to continue.")
	case !p.MFAVerified:
		writeError(w, http.StatusForbidden, "mfa_required", "Enter your two-factor authentication code to continue.")
	default:
		writeError(w, http.StatusForbidden, "password_change_required", "Change your password to continue.")
	}
}
