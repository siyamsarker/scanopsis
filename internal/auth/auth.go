// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Package auth implements sign-in (GitLab OpenID Connect and the local
// administrator), server-side sessions and the signed-in principal.
package auth

import (
	"context"
	"net/http"
)

// Role is a user's authorisation level.
type Role string

// Roles. Admins can manage users and services; viewers can read everything.
const (
	RoleAdmin  Role = "admin"
	RoleViewer Role = "viewer"
)

// User sources.
const (
	SourceGitLab = "gitlab"
	SourceLocal  = "local"
)

// Principal is the signed-in user of a request.
type Principal struct {
	UserID   int64
	Source   string
	Username string
	Name     string
	Email    string
	Role     Role

	// SessionID is the SHA-256 of the session token; it identifies the
	// session server-side.
	SessionID []byte
	// MFAEnrolled reports a confirmed TOTP enrolment; MFAVerified that this
	// session has passed the second factor.
	MFAEnrolled        bool
	MFAVerified        bool
	MustChangePassword bool
}

// Complete reports whether the session may use the application: the second
// factor is verified and no password change is pending.
func (p *Principal) Complete() bool {
	return p != nil && p.MFAVerified && !p.MustChangePassword
}

// IsAdmin reports whether the principal has the admin role and a complete
// session.
func (p *Principal) IsAdmin() bool { return p.Complete() && p.Role == RoleAdmin }

type principalKey struct{}

// WithPrincipal returns a context carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the principal stored in ctx, if any.
func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(*Principal)
	return p, ok && p != nil
}

// SessionStarter starts a session for a user who has just signed in. It
// replaces any session the request already carries (preventing session
// fixation) and sets the session cookie on w.
type SessionStarter interface {
	Start(w http.ResponseWriter, r *http.Request, userID int64) error
}

// AuditRecorder records security-relevant events. Implementations never
// fail the caller; errors are logged.
type AuditRecorder interface {
	Record(ctx context.Context, actor, action string, detail map[string]any)
}
