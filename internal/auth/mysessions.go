// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/store"
)

// maxListedSessions bounds the session list. Sessions end after the absolute
// timeout, so a user rarely has more than a handful.
const maxListedSessions = 200

// MySessionsStore is the persistence the session list needs; *store.Queries
// implements it.
type MySessionsStore interface {
	ListUserSessions(ctx context.Context, arg store.ListUserSessionsParams) ([]store.ListUserSessionsRow, error)
	DeleteUserSession(ctx context.Context, arg store.DeleteUserSessionParams) (int64, error)
	DeleteOtherUserSessions(ctx context.Context, arg store.DeleteOtherUserSessionsParams) (int64, error)
}

// MySessions serves the signed-in user's own session list: GET and DELETE
// /api/v1/me/sessions and DELETE /api/v1/me/sessions/{id}. The handlers
// expect the router to have put the principal of a complete session in the
// request context.
type MySessions struct {
	store    MySessionsStore
	sessions *Sessions
	audit    AuditRecorder
	clientIP func(*http.Request) netip.Addr
	log      *slog.Logger
}

// NewMySessions returns the session list handlers. sessions supplies the
// clock and the idle timeout, so the list shows exactly the sessions that
// still work. A nil log discards server-side failures.
func NewMySessions(s MySessionsStore, sessions *Sessions, audit AuditRecorder, clientIP func(*http.Request) netip.Addr, log *slog.Logger) *MySessions {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &MySessions{store: s, sessions: sessions, audit: audit, clientIP: clientIP, log: log}
}

type sessionJSON struct {
	ID          int64     `json:"id"`
	Current     bool      `json:"current"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	MFAVerified bool      `json:"mfa_verified"`
	ClientIP    string    `json:"client_ip"`
	UserAgent   string    `json:"user_agent"`
}

// ListHandler handles GET /api/v1/me/sessions.
func (m *MySessions) ListHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "Sign in first.")
			return
		}
		now, idleCutoff := m.sessions.window()
		rows, err := m.store.ListUserSessions(r.Context(), store.ListUserSessionsParams{
			UserID: p.UserID, Now: ts(now), IdleCutoff: ts(idleCutoff), MaxRows: maxListedSessions,
		})
		if err != nil {
			m.fail(w, r, "cannot list sessions", err)
			return
		}
		items := make([]sessionJSON, 0, len(rows))
		for _, row := range rows {
			items = append(items, sessionJSON{
				ID:          row.ID,
				Current:     bytes.Equal(row.IDHash, p.SessionID),
				CreatedAt:   row.CreatedAt.Time,
				LastSeenAt:  row.LastSeenAt.Time,
				ExpiresAt:   row.ExpiresAt.Time,
				MFAVerified: row.MfaVerified,
				ClientIP:    row.ClientIp,
				UserAgent:   row.UserAgent,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	})
}

// EndOthersHandler handles DELETE /api/v1/me/sessions: it ends every session
// of the user except the one making the request.
func (m *MySessions) EndOthersHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "Sign in first.")
			return
		}
		n, err := m.store.DeleteOtherUserSessions(r.Context(), store.DeleteOtherUserSessionsParams{
			UserID: p.UserID, KeepIDHash: p.SessionID,
		})
		if err != nil {
			m.fail(w, r, "cannot end other sessions", err)
			return
		}
		if n > 0 {
			m.audit.Record(r.Context(), actorOf(p), audit.ActionSessionRevoked, map[string]any{
				"scope": "others", "count": n, "ip": m.clientIP(r).String(),
			})
		}
		writeJSON(w, http.StatusOK, map[string]int64{"ended": n})
	})
}

// EndHandler handles DELETE /api/v1/me/sessions/{id}: it ends one other
// session of the user. The current session ends with sign-out instead.
func (m *MySessions) EndHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := PrincipalFrom(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "Sign in first.")
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			writeError(w, http.StatusBadRequest, "invalid_request", "The session id must be a positive integer.")
			return
		}
		if id == p.SessionNumber {
			writeError(w, http.StatusConflict, "current_session", "This is the session you are using. Sign out to end it.")
			return
		}
		n, err := m.store.DeleteUserSession(r.Context(), store.DeleteUserSessionParams{
			ID: id, UserID: p.UserID, KeepIDHash: p.SessionID,
		})
		if err != nil {
			m.fail(w, r, "cannot end session", err)
			return
		}
		if n == 0 {
			writeError(w, http.StatusNotFound, "not_found", "No such session. It may have ended already.")
			return
		}
		m.audit.Record(r.Context(), actorOf(p), audit.ActionSessionRevoked, map[string]any{
			"scope": "one", "session_id": id, "ip": m.clientIP(r).String(),
		})
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
}

func (m *MySessions) fail(w http.ResponseWriter, r *http.Request, msg string, err error) {
	m.log.ErrorContext(r.Context(), "sessions: "+msg, "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "Something went wrong. Try again later.")
}
