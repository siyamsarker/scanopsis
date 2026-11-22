// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Package catalog serves the services catalog, the portfolio overview and
// the health of scan ingestion.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/httpapi"
	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const (
	defaultLimit = 100
	maxLimit     = 500
	maxBody      = 16 << 10
)

// SettingsSource provides the current settings snapshot.
type SettingsSource interface{ Current() settings.Snapshot }

// Catalog implements the service and portfolio endpoints.
type Catalog struct {
	pool  *pgxpool.Pool
	q     *store.Queries
	cfg   SettingsSource
	audit auth.AuditRecorder
	log   *slog.Logger
	// history caches the portfolio history (see historyCache).
	history *historyCache
}

// New returns a Catalog.
func New(pool *pgxpool.Pool, cfg SettingsSource, audit auth.AuditRecorder, log *slog.Logger) *Catalog {
	c := &Catalog{pool: pool, q: store.New(pool), cfg: cfg, audit: audit, log: log}
	c.history = newHistoryCache(c)
	return c
}

// Handlers returns the HTTP handlers by OpenAPI operation ID.
func (c *Catalog) Handlers() map[string]http.Handler {
	return map[string]http.Handler{
		"getPortfolio":        http.HandlerFunc(c.getPortfolio),
		"getPortfolioHistory": http.HandlerFunc(c.getPortfolioHistory),
		"listServices":        http.HandlerFunc(c.listServices),
		"createService":       http.HandlerFunc(c.createService),
		"getService":          http.HandlerFunc(c.getService),
		"updateService":       http.HandlerFunc(c.updateService),
		"deleteService":       http.HandlerFunc(c.deleteService),
		"getTrivyIntegration": http.HandlerFunc(c.getTrivyIntegration),
	}
}

type serviceJSON struct {
	ID                int64     `json:"id"`
	Name              string    `json:"name"`
	GitLabProjectID   *int64    `json:"gitlab_project_id"`
	GitLabProjectPath string    `json:"gitlab_project_path"`
	SonarProjectKey   *string   `json:"sonar_project_key"`
	CreatedVia        string    `json:"created_via"`
	Archived          bool      `json:"archived"`
	CreatedAt         time.Time `json:"created_at"`
	DefaultRef        *string   `json:"default_ref"`
}

func toService(s store.Service) serviceJSON {
	out := serviceJSON{
		ID: s.ID, Name: s.Name, GitLabProjectPath: s.GitlabProjectPath, CreatedVia: s.CreatedVia,
		Archived: s.Archived, CreatedAt: s.CreatedAt.Time,
	}
	if s.GitlabProjectID.Valid {
		out.GitLabProjectID = &s.GitlabProjectID.Int64
	}
	if s.SonarProjectKey.Valid {
		out.SonarProjectKey = &s.SonarProjectKey.String
	}
	if s.DefaultRef.Valid {
		out.DefaultRef = &s.DefaultRef.String
	}
	return out
}

// textOrNil is the JSON form of a nullable text column.
func textOrNil(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	return &t.String
}

func (c *Catalog) internalError(w http.ResponseWriter, r *http.Request, msg string, err error) {
	c.log.ErrorContext(r.Context(), msg, "error", err)
	httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "Something went wrong. Try again later.")
}

func (c *Catalog) actor(ctx context.Context) string {
	if p, ok := auth.PrincipalFrom(ctx); ok {
		return p.Source + ":" + p.Username
	}
	return ""
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func notFound(w http.ResponseWriter) {
	httpapi.WriteError(w, http.StatusNotFound, "not_found", "Service not found.")
}

func badRequest(w http.ResponseWriter, msg string) {
	httpapi.WriteError(w, http.StatusBadRequest, "invalid_request", msg)
}

// conflictMessage names the value that is already taken, if err is a unique
// violation on the services table.
func conflictMessage(err error) (string, bool) {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) || pe.Code != "23505" {
		return "", false
	}
	switch pe.ConstraintName {
	case "services_gitlab_project_path_key":
		return "A service with this GitLab project path already exists.", true
	case "services_sonar_project_key_key":
		return "This SonarQube project key is already linked to another service.", true
	}
	return "A service with this value already exists.", true
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil || dec.More() {
		badRequest(w, "The request body is not valid.")
		return false
	}
	return true
}

func (c *Catalog) listServices(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	arg := store.ListServicesParams{Q: qs.Get("q")}
	if len(arg.Q) > 200 {
		badRequest(w, "q is too long.")
		return
	}
	if v := qs.Get("archived"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			badRequest(w, "archived must be true or false.")
			return
		}
		arg.Archived = b
	}
	if v := qs.Get("after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			badRequest(w, "after must be a non-negative integer.")
			return
		}
		arg.After = n
	}
	limit := defaultLimit
	if v := qs.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxLimit {
			badRequest(w, "limit must be between 1 and 500.")
			return
		}
		limit = n
	}
	arg.MaxRows = int32(limit + 1) //nolint:gosec // limit <= maxLimit
	rows, err := c.q.ListServices(r.Context(), arg)
	if err != nil {
		c.internalError(w, r, "cannot list services", err)
		return
	}
	var next *int64
	if len(rows) > limit {
		rows = rows[:limit]
		next = &rows[limit-1].ID
	}
	items := make([]serviceJSON, len(rows))
	for i, s := range rows {
		items[i] = toService(s)
	}
	httpapi.WriteJSON(w, http.StatusOK, struct {
		Items     []serviceJSON `json:"items"`
		NextAfter *int64        `json:"next_after"`
	}{items, next})
}

func (c *Catalog) getService(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	s, err := c.q.GetService(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		c.internalError(w, r, "cannot read service", err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, toService(s))
}

func (c *Catalog) createService(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string  `json:"gitlab_project_path"`
		Name *string `json:"name"`
		Key  *string `json:"sonar_project_key"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	name, key, err := validateCreate(in.Path, in.Name, in.Key)
	if err != nil {
		badRequest(w, err.Error())
		return
	}
	arg := store.CreateServiceParams{Name: name, GitlabProjectPath: in.Path}
	if key != nil {
		arg.SonarProjectKey.String, arg.SonarProjectKey.Valid = *key, true
	}
	s, err := c.q.CreateService(r.Context(), arg)
	if msg, ok := conflictMessage(err); ok {
		httpapi.WriteError(w, http.StatusConflict, "conflict", msg)
		return
	}
	if err != nil {
		c.internalError(w, r, "cannot create service", err)
		return
	}
	c.audit.Record(r.Context(), c.actor(r.Context()), audit.ActionServiceCreated, map[string]any{
		"id": s.ID, "gitlab_project_path": s.GitlabProjectPath,
	})
	httpapi.WriteJSON(w, http.StatusCreated, toService(s))
}

// optString records whether a JSON field was present, and whether null.
type optString struct {
	set bool
	val *string
}

func (o *optString) UnmarshalJSON(b []byte) error {
	o.set = true
	return json.Unmarshal(b, &o.val)
}

func (c *Catalog) updateService(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	var in struct {
		Name     *string   `json:"name"`
		Key      optString `json:"sonar_project_key"`
		Archived *bool     `json:"archived"`
		Ref      optString `json:"default_ref"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if err := validateUpdate(in.Name, in.Key.set, in.Key.val, in.Archived, in.Ref.set, in.Ref.val); err != nil {
		badRequest(w, err.Error())
		return
	}
	arg := store.UpdateServiceParams{ID: id, SetSonarKey: in.Key.set, SetDefaultRef: in.Ref.set}
	changed := []string{}
	if in.Name != nil {
		arg.Name.String, arg.Name.Valid = *in.Name, true
		changed = append(changed, "name")
	}
	if in.Key.set {
		if in.Key.val != nil {
			arg.SonarProjectKey.String, arg.SonarProjectKey.Valid = *in.Key.val, true
		}
		changed = append(changed, "sonar_project_key")
	}
	if in.Archived != nil {
		arg.Archived.Bool, arg.Archived.Valid = *in.Archived, true
		changed = append(changed, "archived")
	}
	if in.Ref.set {
		if in.Ref.val != nil {
			ref, _ := validateRef(*in.Ref.val) // checked above
			arg.DefaultRef.String, arg.DefaultRef.Valid = ref, true
		}
		changed = append(changed, "default_ref")
	}
	row, err := c.q.UpdateService(r.Context(), arg)
	if errors.Is(err, pgx.ErrNoRows) {
		notFound(w)
		return
	}
	if msg, ok := conflictMessage(err); ok {
		httpapi.WriteError(w, http.StatusConflict, "conflict", msg)
		return
	}
	if err != nil {
		c.internalError(w, r, "cannot update service", err)
		return
	}
	s := row.Service
	detail := map[string]any{"id": s.ID, "gitlab_project_path": s.GitlabProjectPath, "fields": changed}
	if in.Ref.set {
		detail["default_ref"] = map[string]any{
			"from": textOrNil(row.PreviousDefaultRef), "to": textOrNil(s.DefaultRef),
		}
	}
	c.audit.Record(r.Context(), c.actor(r.Context()), audit.ActionServiceUpdated, detail)
	httpapi.WriteJSON(w, http.StatusOK, toService(s))
}

func (c *Catalog) deleteService(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		notFound(w)
		return
	}
	path, err := c.q.DeleteService(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		c.internalError(w, r, "cannot delete service", err)
		return
	}
	c.audit.Record(r.Context(), c.actor(r.Context()), audit.ActionServiceDeleted, map[string]any{
		"id": id, "gitlab_project_path": path,
	})
	w.WriteHeader(http.StatusNoContent)
}

type rejectionJSON struct {
	At          time.Time `json:"at"`
	Reason      string    `json:"reason"`
	ProjectPath *string   `json:"project_path"`
}

func (c *Catalog) getTrivyIntegration(w http.ResponseWriter, r *http.Request) {
	st, err := c.q.TrivyIntegrationStats(r.Context())
	if err != nil {
		c.internalError(w, r, "cannot read ingestion stats", err)
		return
	}
	rows, err := c.q.RecentIngestRejections(r.Context())
	if err != nil {
		c.internalError(w, r, "cannot read ingestion rejections", err)
		return
	}
	rej := make([]rejectionJSON, len(rows))
	for i, row := range rows {
		var d struct {
			Reason      string  `json:"reason"`
			ProjectPath *string `json:"project_path"`
		}
		_ = json.Unmarshal(row.Detail, &d) // a malformed detail shows as an empty reason
		rej[i] = rejectionJSON{At: row.At.Time, Reason: d.Reason, ProjectPath: d.ProjectPath}
	}
	var last *time.Time
	if st.LastUploadAt.Valid {
		last = &st.LastUploadAt.Time
	}
	httpapi.WriteJSON(w, http.StatusOK, struct {
		Configured       bool            `json:"configured"`
		LastUploadAt     *time.Time      `json:"last_upload_at"`
		Uploads24h       int64           `json:"uploads_24h"`
		Rejections24h    int64           `json:"rejections_24h"`
		RecentRejections []rejectionJSON `json:"recent_rejections"`
	}{c.cfg.Current().GitLabURL != "", last, st.Uploads24h, st.Rejections24h, rej})
}
