// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const (
	defaultLimit = 50
	maxLimit     = 200
	maxPatchBody = 8 << 10
	readyTimeout = 2 * time.Second
)

type apiError struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type statusBody struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type userJSON struct {
	ID          int64      `json:"id"`
	Source      string     `json:"source"`
	Username    string     `json:"username"`
	Name        string     `json:"name"`
	Email       string     `json:"email"`
	Role        string     `json:"role"`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at"`
	DisabledAt  *time.Time `json:"disabled_at"`
	// MFAEnrolled reports a confirmed TOTP enrolment.
	MFAEnrolled bool `json:"mfa_enrolled"`
}

type meJSON struct {
	userJSON
	MFAVerified        bool `json:"mfa_verified"`
	MustChangePassword bool `json:"must_change_password"`
}

type userPage struct {
	Items     []userJSON `json:"items"`
	NextAfter *int64     `json:"next_after"`
}

func toUser(u store.User) userJSON {
	out := userJSON{
		ID: u.ID, Source: u.Source, Username: u.Username, Name: u.Name, Email: u.Email,
		Role: u.Role, CreatedAt: u.CreatedAt.Time, MFAEnrolled: u.TotpEnabledAt.Valid,
	}
	if u.LastLoginAt.Valid {
		t := u.LastLoginAt.Time
		out.LastLoginAt = &t
	}
	if u.DisabledAt.Valid {
		t := u.DisabledAt.Time
		out.DisabledAt = &t
	}
	return out
}

// writeJSON writes v as JSON. API responses can carry user data and depend
// on the session, so no browser or proxy may store them.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiError{apiErrorBody{Code: code, Message: message}})
}

// WriteJSON writes v as a JSON response with Cache-Control: no-store. Feature
// packages use it so that every API response has the same form.
func WriteJSON(w http.ResponseWriter, status int, v any) { writeJSON(w, status, v) }

// WriteError writes the API's standard error body {"error":{"code","message"}}.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	writeError(w, status, code, message)
}

func (s *server) internalError(w http.ResponseWriter, r *http.Request, msg string, err error) {
	s.Log.ErrorContext(r.Context(), msg, "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "Something went wrong. Try again later.")
}

func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}

func (s *server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
	defer cancel()
	err := s.ready.check(ctx, func(ctx context.Context) error {
		err := s.DB.Ping(ctx)
		if err != nil {
			s.Log.ErrorContext(ctx, "readiness check failed", "error", err)
		}
		return err
	})
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, statusBody{Status: "not_ready", Reason: "database unreachable"})
		return
	}
	writeJSON(w, http.StatusOK, statusBody{Status: "ready"})
}

func (s *server) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Build)
}

func (s *server) authMethods(w http.ResponseWriter, _ *http.Request) {
	gitlab, local := s.GitLabLogin != nil, s.LocalLogin != nil
	if s.Methods != nil {
		gitlab, local = s.Methods()
	}
	writeJSON(w, http.StatusOK, map[string]bool{"gitlab": gitlab, "local": local})
}

// NotEnabled answers 404 not_enabled, naming the feature. Handlers that can
// be switched off at run time use it when they are off.
func NotEnabled(what string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_enabled", what+" is not enabled on this server.")
	})
}

// optional delegates to h, or answers 404 not_enabled when h is nil.
func (s *server) optional(h http.Handler, what string) http.Handler {
	if h != nil {
		return h
	}
	return NotEnabled(what)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	// Logging out must work even if the session cannot be read, so errors
	// other than "no session" are logged and otherwise ignored.
	actor := ""
	p, err := s.Sessions.Authenticate(r)
	switch {
	case err == nil && p != nil:
		actor = p.Source + ":" + p.Username
	case err != nil && !errors.Is(err, auth.ErrNoSession):
		s.Log.ErrorContext(r.Context(), "cannot check session on logout", "error", err)
	}
	if err := s.Sessions.End(w, r); err != nil {
		s.internalError(w, r, "cannot end session", err)
		return
	}
	if actor != "" {
		s.Audit.Record(r.Context(), actor, audit.ActionLogout, nil)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) me(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	u, err := s.Users.GetUser(r.Context(), p.UserID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusUnauthorized, "unauthenticated", "Sign in to continue.")
	case err != nil:
		s.internalError(w, r, "cannot load user", err)
	default:
		user := toUser(u)
		// The session's view of the enrolment decides the next step, as everywhere else.
		user.MFAEnrolled = p.MFAEnrolled
		writeJSON(w, http.StatusOK, meJSON{
			userJSON: user, MFAVerified: p.MFAVerified, MustChangePassword: p.MustChangePassword,
		})
	}
}

func (s *server) listUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	after, limit := int64(0), int64(defaultLimit)
	var err error
	if v := q.Get("after"); v != "" {
		if after, err = strconv.ParseInt(v, 10, 64); err != nil || after < 0 {
			writeError(w, http.StatusBadRequest, "invalid_parameter", "after must be an integer of 0 or more.")
			return
		}
	}
	if v := q.Get("limit"); v != "" {
		if limit, err = strconv.ParseInt(v, 10, 64); err != nil || limit < 1 || limit > maxLimit {
			writeError(w, http.StatusBadRequest, "invalid_parameter", fmt.Sprintf("limit must be an integer from 1 to %d.", maxLimit))
			return
		}
	}
	rows, err := s.Users.ListUsers(r.Context(), store.ListUsersParams{AfterID: after, MaxRows: int32(limit) + 1}) //nolint:gosec // limit <= maxLimit.
	if err != nil {
		s.internalError(w, r, "cannot list users", err)
		return
	}
	page := userPage{Items: []userJSON{}}
	if int64(len(rows)) > limit {
		rows = rows[:limit]
		next := rows[len(rows)-1].ID
		page.NextAfter = &next
	}
	for _, u := range rows {
		page.Items = append(page.Items, toUser(u))
	}
	writeJSON(w, http.StatusOK, page)
}

// userID reads the {id} path value, answering 400 itself when it is not a
// positive integer.
func userID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request", "The user id must be a positive integer.")
		return 0, false
	}
	return id, true
}

func actorName(r *http.Request) (*auth.Principal, string) {
	p, _ := auth.PrincipalFrom(r.Context())
	return p, p.Source + ":" + p.Username
}

func (s *server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(w, r)
	if !ok {
		return
	}
	var body struct {
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPatchBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(&body)
	if err == nil {
		// Reject anything after the object.
		if extra := dec.Decode(&struct{}{}); !errors.Is(extra, io.EOF) {
			err = errors.New("trailing data")
		}
	}
	if err != nil || (body.Role == nil && body.Disabled == nil) ||
		(body.Role != nil && *body.Role != store.RoleAdmin && *body.Role != store.RoleViewer) {
		writeError(w, http.StatusBadRequest, "invalid_request",
			`The body must be a JSON object with "role" ("admin" or "viewer"), "disabled" (true or false), or both.`)
		return
	}
	actor, name := actorName(r)
	if body.Disabled != nil && *body.Disabled && actor.UserID == id {
		writeError(w, http.StatusConflict, "own_account", "You cannot disable your own account. Ask another administrator.")
		return
	}

	before, err := s.Users.GetUser(r.Context(), id)
	if err != nil {
		s.userError(w, r, err)
		return
	}
	updated := before
	if body.Role != nil {
		if updated, err = s.Users.SetUserRole(r.Context(), id, *body.Role); err != nil {
			s.userError(w, r, err)
			return
		}
		s.Audit.Record(r.Context(), name, audit.ActionRoleChanged, map[string]any{
			"user_id": updated.ID, "username": updated.Username, "from": before.Role, "to": updated.Role,
		})
	}
	if body.Disabled != nil {
		if updated, err = s.Users.SetUserDisabled(r.Context(), id, *body.Disabled); err != nil {
			s.userError(w, r, err)
			return
		}
		action := audit.ActionUserEnabled
		if *body.Disabled {
			action = audit.ActionUserDisabled
		}
		s.Audit.Record(r.Context(), name, action, map[string]any{
			"user_id": updated.ID, "username": updated.Username, "source": updated.Source,
		})
	}
	writeJSON(w, http.StatusOK, toUser(updated))
}

func (s *server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(w, r)
	if !ok {
		return
	}
	actor, name := actorName(r)
	if actor.UserID == id {
		writeError(w, http.StatusConflict, "own_account", "You cannot delete your own account. Ask another administrator.")
		return
	}
	deleted, err := s.Users.DeleteUser(r.Context(), id)
	if err != nil {
		s.userError(w, r, err)
		return
	}
	s.Audit.Record(r.Context(), name, audit.ActionUserDeleted, map[string]any{
		"user_id": deleted.ID, "username": deleted.Username, "source": deleted.Source,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) resetMFA(w http.ResponseWriter, r *http.Request) {
	id, ok := userID(w, r)
	if !ok {
		return
	}
	u, err := s.Users.GetUser(r.Context(), id)
	if err != nil {
		s.userError(w, r, err)
		return
	}
	if err := s.Users.ResetMFA(r.Context(), id); err != nil {
		s.userError(w, r, err)
		return
	}
	actor, _ := auth.PrincipalFrom(r.Context())
	s.Audit.Record(r.Context(), actor.Source+":"+actor.Username, audit.ActionMFAReset, map[string]any{
		"user_id": u.ID, "username": u.Username,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) userError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such user.")
	case errors.Is(err, store.ErrLastAdmin):
		writeError(w, http.StatusConflict, "last_admin", "At least one enabled administrator must remain. Promote or enable another administrator first.")
	default:
		s.internalError(w, r, "cannot update user", err)
	}
}

// apiFallback answers /api/ paths that match no route: 405 with an Allow
// header when the path exists for other methods, 404 otherwise.
func apiFallback(routes []route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var allowed []string
		for _, rt := range routes {
			if matchPattern(rt.pattern, r.URL.Path) {
				allowed = append(allowed, rt.method)
			}
		}
		if len(allowed) == 0 {
			writeError(w, http.StatusNotFound, "not_found", "No such endpoint.")
			return
		}
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "This method is not supported on this endpoint.")
	})
}

// matchPattern reports whether path matches pattern, where {name} matches
// any single non-empty segment.
func matchPattern(pattern, path string) bool {
	ps, as := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(ps) != len(as) {
		return false
	}
	for i, p := range ps {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			if as[i] == "" {
				return false
			}
			continue
		}
		if p != as[i] {
			return false
		}
	}
	return true
}
