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
}

type userPage struct {
	Items     []userJSON `json:"items"`
	NextAfter *int64     `json:"next_after"`
}

func toUser(u store.User) userJSON {
	out := userJSON{
		ID: u.ID, Source: u.Source, Username: u.Username, Name: u.Name, Email: u.Email,
		Role: u.Role, CreatedAt: u.CreatedAt.Time,
	}
	if u.LastLoginAt.Valid {
		t := u.LastLoginAt.Time
		out.LastLoginAt = &t
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiError{apiErrorBody{Code: code, Message: message}})
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
	if err := s.DB.Ping(ctx); err != nil {
		s.Log.ErrorContext(r.Context(), "readiness check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, statusBody{Status: "not_ready", Reason: "database unreachable"})
		return
	}
	writeJSON(w, http.StatusOK, statusBody{Status: "ready"})
}

func (s *server) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Build)
}

func (s *server) authMethods(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"gitlab": s.GitLabLogin != nil,
		"local":  s.LocalLogin != nil,
	})
}

// optional delegates to h, or answers 404 not_enabled when h is nil.
func (s *server) optional(h http.Handler, what string) http.Handler {
	if h != nil {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_enabled", what+" sign-in is not enabled on this server.")
	})
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	actor := ""
	if p, err := s.Sessions.Authenticate(r); err == nil && p != nil {
		actor = p.Username
	}
	if err := s.Sessions.End(w, r); err != nil {
		s.internalError(w, r, "cannot end session", err)
		return
	}
	s.Audit.Record(r.Context(), actor, audit.ActionLogout, nil)
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
		writeJSON(w, http.StatusOK, toUser(u))
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

func (s *server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request", "The user id must be a positive integer.")
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPatchBody))
	dec.DisallowUnknownFields()
	err = dec.Decode(&body)
	if err == nil {
		// Reject anything after the object.
		if extra := dec.Decode(&struct{}{}); !errors.Is(extra, io.EOF) {
			err = errors.New("trailing data")
		}
	}
	if err != nil || (body.Role != store.RoleAdmin && body.Role != store.RoleViewer) {
		writeError(w, http.StatusBadRequest, "invalid_request", `The body must be a JSON object {"role":"admin"} or {"role":"viewer"}.`)
		return
	}

	before, err := s.Users.GetUser(r.Context(), id)
	if err != nil {
		s.userError(w, r, err)
		return
	}
	updated, err := s.Users.SetUserRole(r.Context(), id, body.Role)
	if err != nil {
		s.userError(w, r, err)
		return
	}
	actor, _ := auth.PrincipalFrom(r.Context())
	s.Audit.Record(r.Context(), actor.Username, audit.ActionRoleChanged, map[string]any{
		"user_id": updated.ID, "username": updated.Username, "from": before.Role, "to": updated.Role,
	})
	writeJSON(w, http.StatusOK, toUser(updated))
}

func (s *server) userError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such user.")
	case errors.Is(err, store.ErrLastAdmin):
		writeError(w, http.StatusConflict, "last_admin", "At least one administrator must remain. Promote another user first.")
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
