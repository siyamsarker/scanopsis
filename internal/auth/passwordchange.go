// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/store"
)

// PasswordStore is the persistence a password change needs; *store.Queries
// implements it.
type PasswordStore interface {
	GetUser(ctx context.Context, id int64) (store.User, error)
	SetPassword(ctx context.Context, arg store.SetPasswordParams) error
	DeleteOtherUserSessions(ctx context.Context, arg store.DeleteOtherUserSessionsParams) (int64, error)
}

type passwordChange struct {
	store   PasswordStore
	audit   AuditRecorder
	limiter *RateLimiter
	log     *slog.Logger
}

// NewPasswordChange returns the handler for POST /api/v1/me/password. The
// router must put the signed-in Principal in the request context and ensure
// its second factor is verified; the principal may still have to change its
// password. Callers should use NewRateLimiter(12*time.Second, 10, nil) as
// limiter, which limits attempts per user. Server-side failures are logged
// to log (nil discards them).
func NewPasswordChange(s PasswordStore, audit AuditRecorder, limiter *RateLimiter, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &passwordChange{store: s, audit: audit, limiter: limiter, log: log}
}

type passwordChangeRequest struct {
	Current *string `json:"current_password"`
	New     *string `json:"new_password"`
}

func (h *passwordChange) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST to change the password.")
		return
	}
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "Sign in first.")
		return
	}
	if p.Source != SourceLocal {
		writeError(w, http.StatusForbidden, "not_local", "Only local accounts have a password here. Change it where you sign in.")
		return
	}
	if ok, wait := h.limiter.Allow(strconv.FormatInt(p.UserID, 10)); !ok {
		secs := retryAfterSeconds(wait)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts",
			"Too many attempts. Wait "+strconv.Itoa(secs)+" seconds and try again.")
		return
	}

	var req passwordChangeRequest
	if !decodeBody(w, r, &req, "Send a JSON object with only \"current_password\" and \"new_password\" (at most 8 KiB).") {
		return
	}
	if req.Current == nil || req.New == nil || *req.Current == "" || *req.New == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Both \"current_password\" and \"new_password\" are required.")
		return
	}
	current, next := *req.Current, *req.New
	if len(current) > maxPasswordBytes {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "The current password is wrong.")
		return
	}

	user, err := h.store.GetUser(r.Context(), p.UserID)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "Sign in first.")
		return
	}
	if err != nil {
		h.fail(w, r, "cannot load user", err)
		return
	}
	if user.Source != SourceLocal {
		writeError(w, http.StatusForbidden, "not_local", "Only local accounts have a password here. Change it where you sign in.")
		return
	}

	match := false
	if user.PasswordHash.Valid {
		var verr error
		match, verr = VerifyPassword(r.Context(), user.PasswordHash.String, current)
		if isCancel(verr) {
			writeError(w, http.StatusServiceUnavailable, "busy", "The server is busy. Try again in a moment.")
			return
		}
		if verr != nil {
			match = false
		}
	}
	if !match {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "The current password is wrong.")
		return
	}
	if err := CheckPassword(next, user.Username); err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}
	if next == current {
		writeError(w, http.StatusBadRequest, "weak_password", "The new password must be different from the current one.")
		return
	}

	hash, err := HashPassword(r.Context(), next)
	if err != nil {
		if isCancel(err) {
			writeError(w, http.StatusServiceUnavailable, "busy", "The server is busy. Try again in a moment.")
			return
		}
		h.fail(w, r, "cannot hash password", err)
		return
	}
	if err := h.store.SetPassword(r.Context(), store.SetPasswordParams{
		ID:           user.ID,
		PasswordHash: pgtype.Text{String: hash, Valid: true},
		MustChange:   false,
	}); err != nil {
		h.fail(w, r, "cannot save password", err)
		return
	}
	// The password is changed; signing out the other sessions is what makes
	// the change useful after a suspected compromise, so failing here is an error.
	if _, err := h.store.DeleteOtherUserSessions(r.Context(), store.DeleteOtherUserSessionsParams{
		UserID: user.ID, KeepIDHash: p.SessionID,
	}); err != nil {
		h.fail(w, r, "cannot sign out other sessions", err)
		return
	}
	h.audit.Record(r.Context(), "local:"+user.Username, audit.ActionPasswordChanged,
		map[string]any{"username": user.Username})
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h *passwordChange) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.log.ErrorContext(r.Context(), "password change: "+what, "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error",
		"The password could not be changed because of a server problem. Try again, and check the server logs if it persists.")
}

func isCancel(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
