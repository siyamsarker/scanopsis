// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

// Package auditlog serves the audit log to administrators, newest first and
// one page at a time.
package auditlog

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/httpapi"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const (
	defaultLimit = 50
	maxLimit     = 200
	maxAction    = 100
	maxActor     = 200
)

// AuditLog implements the audit log endpoint.
type AuditLog struct {
	q   *store.Queries
	log *slog.Logger
}

// New returns an AuditLog that reads from pool.
func New(pool *pgxpool.Pool, log *slog.Logger) *AuditLog {
	return &AuditLog{q: store.New(pool), log: log}
}

// Handlers returns the HTTP handlers by OpenAPI operation ID.
func (a *AuditLog) Handlers() map[string]http.Handler {
	return map[string]http.Handler{
		"listAuditEvents": http.HandlerFunc(a.listAuditEvents),
	}
}

// filter is a validated request. Nil fields are not applied.
type filter struct {
	action *string
	actor  *string
	before *int64
	limit  int
}

// parseFilter validates the query string. It returns a plain message naming
// the first invalid parameter, or the empty string when the request is valid.
// An empty value is treated as absent, as elsewhere in the API.
func parseFilter(qs url.Values) (filter, string) {
	f := filter{limit: defaultLimit}
	if v := qs.Get("action"); v != "" {
		if len(v) > maxAction {
			return filter{}, "action must be at most 100 characters."
		}
		f.action = &v
	}
	if v := qs.Get("actor"); v != "" {
		if len(v) > maxActor {
			return filter{}, "actor must be at most 200 characters."
		}
		f.actor = &v
	}
	if v := qs.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return filter{}, "before must be an integer of at least 1."
		}
		f.before = &n
	}
	if v := qs.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxLimit {
			return filter{}, "limit must be an integer between 1 and 200."
		}
		f.limit = n
	}
	return f, ""
}

type eventJSON struct {
	ID     int64           `json:"id"`
	At     time.Time       `json:"at"`
	Actor  string          `json:"actor"`
	Action string          `json:"action"`
	Detail json.RawMessage `json:"detail"`
}

type pageJSON struct {
	Items      []eventJSON `json:"items"`
	NextBefore *int64      `json:"next_before"`
}

func (a *AuditLog) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	f, msg := parseFilter(r.URL.Query())
	if msg != "" {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_parameter", msg)
		return
	}
	arg := store.ListAuditEventsParams{
		ActionName: optionalText(f.action),
		ActorPart:  optionalText(f.actor),
		BeforeID:   optionalInt(f.before),
		MaxRows:    int32(f.limit + 1), //nolint:gosec // limit is at most maxLimit
	}
	rows, err := a.q.ListAuditEvents(r.Context(), arg)
	if err != nil {
		a.log.ErrorContext(r.Context(), "cannot list audit events", "error", err)
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "Something went wrong. Try again later.")
		return
	}
	var next *int64
	if len(rows) > f.limit {
		rows = rows[:f.limit]
		id := rows[f.limit-1].ID
		next = &id
	}
	items := make([]eventJSON, len(rows))
	for i, e := range rows {
		items[i] = eventJSON{
			ID: e.ID, At: e.At.Time.UTC(), Actor: e.Actor, Action: e.Action,
			Detail: json.RawMessage(e.Detail),
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, pageJSON{Items: items, NextBefore: next})
}

func optionalText(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func optionalInt(n *int64) pgtype.Int8 {
	if n == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *n, Valid: true}
}
