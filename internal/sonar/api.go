// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package sonar

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/httpapi"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const (
	defaultHistory = 30
	maxHistory     = 200
)

type analysisJSON struct {
	ID                     int64             `json:"id"`
	AnalysedAt             time.Time         `json:"analysed_at"`
	Revision               string            `json:"revision"`
	QualityGateStatus      string            `json:"quality_gate_status"`
	Conditions             []Condition       `json:"conditions"`
	ReliabilityRating      *string           `json:"reliability_rating"`
	SecurityRating         *string           `json:"security_rating"`
	MaintainabilityRating  *string           `json:"maintainability_rating"`
	SecurityReviewRating   *string           `json:"security_review_rating"`
	Coverage               *float64          `json:"coverage"`
	DuplicatedLinesDensity *float64          `json:"duplicated_lines_density"`
	Measures               map[string]string `json:"measures"`
	Mode                   string            `json:"mode"`
	GitlabPipelineID       *int64            `json:"gitlab_pipeline_id"`
	ProjectURL             *string           `json:"project_url"`
}

type serviceSonarJSON struct {
	SonarProjectKey *string        `json:"sonar_project_key"`
	Latest          *analysisJSON  `json:"latest"`
	History         []analysisJSON `json:"history"`
}

type deliveryJSON struct {
	TaskID     string    `json:"task_id"`
	ProjectKey string    `json:"project_key"`
	ReceivedAt time.Time `json:"received_at"`
	Outcome    string    `json:"outcome"`
	ServiceID   *int64    `json:"service_id"`
	ServiceName *string   `json:"service_name"`
	Detail      string    `json:"detail"`
}

type integrationJSON struct {
	Configured       bool           `json:"configured"`
	ServerVersion    *string        `json:"server_version"`
	LastWebhookAt    *time.Time     `json:"last_webhook_at"`
	LastSyncAt       *time.Time     `json:"last_sync_at"`
	LastSyncError    *string        `json:"last_sync_error"`
	RecentDeliveries []deliveryJSON `json:"recent_deliveries"`
}

func ratingPtr(r string) *string {
	if r == "" {
		return nil
	}
	return &r
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func floatPtr(f pgtype.Float8) *float64 {
	if !f.Valid {
		return nil
	}
	return &f.Float64
}

func toAnalysis(a store.SonarAnalysis, projectURL *string) analysisJSON {
	out := analysisJSON{
		ID: a.ID, AnalysedAt: a.AnalysedAt.Time.UTC(), Revision: a.Revision, QualityGateStatus: a.QualityGateStatus,
		Conditions: []Condition{}, Measures: map[string]string{}, Mode: a.Mode,
		ReliabilityRating: ratingPtr(a.ReliabilityRating), SecurityRating: ratingPtr(a.SecurityRating),
		MaintainabilityRating: ratingPtr(a.MaintainabilityRating), SecurityReviewRating: ratingPtr(a.SecurityReviewRating),
		Coverage: floatPtr(a.Coverage), DuplicatedLinesDensity: floatPtr(a.DuplicatedLinesDensity),
		ProjectURL: projectURL,
	}
	// The columns are written by this package; a malformed value is shown as empty.
	_ = json.Unmarshal(a.GateConditions, &out.Conditions)
	_ = json.Unmarshal(a.Measures, &out.Measures)
	if out.Conditions == nil {
		out.Conditions = []Condition{}
	}
	if out.Measures == nil {
		out.Measures = map[string]string{}
	}
	if a.GitlabPipelineID.Valid {
		id := a.GitlabPipelineID.Int64
		out.GitlabPipelineID = &id
	}
	return out
}

func (s *Sonar) internalError(w http.ResponseWriter, r *http.Request, msg string, err error) {
	s.log.ErrorContext(r.Context(), msg, "error", err)
	httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "Something went wrong. Try again later.")
}

func (s *Sonar) getServiceSonar(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	notFound := func() { httpapi.WriteError(w, http.StatusNotFound, "not_found", "No such service.") }
	if err != nil {
		notFound()
		return
	}
	limit := defaultHistory
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n >= 1 {
		limit = min(n, maxHistory)
	}
	key, err := s.q.SonarServiceKey(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		notFound()
		return
	}
	if err != nil {
		s.internalError(w, r, "read service", err)
		return
	}
	rows, err := s.q.ListSonarAnalyses(r.Context(), store.ListSonarAnalysesParams{ServiceID: id, Limit: int32(limit)})
	if err != nil {
		s.internalError(w, r, "read SonarQube analyses", err)
		return
	}
	var projectURL *string
	if base := s.cfg.Current().SonarURL; base != "" && key.Valid {
		u := strings.TrimRight(base, "/") + "/dashboard?id=" + url.QueryEscape(key.String)
		projectURL = &u
	}
	out := serviceSonarJSON{History: make([]analysisJSON, 0, len(rows))}
	if key.Valid {
		out.SonarProjectKey = &key.String
	}
	for _, a := range rows {
		out.History = append(out.History, toAnalysis(a, projectURL))
	}
	if len(out.History) > 0 {
		out.Latest = &out.History[0]
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

func (s *Sonar) getSonarIntegration(w http.ResponseWriter, r *http.Request) {
	rows, err := s.q.ListSonarDeliveries(r.Context())
	if err != nil {
		s.internalError(w, r, "read SonarQube deliveries", err)
		return
	}
	last, err := s.q.LastSonarDeliveryAt(r.Context())
	if err != nil {
		s.internalError(w, r, "read last SonarQube delivery", err)
		return
	}
	out := integrationJSON{Configured: s.cfg.Current().SonarEnabled(), RecentDeliveries: make([]deliveryJSON, 0, len(rows))}
	if last.Valid {
		t := last.Time.UTC()
		out.LastWebhookAt = &t
	}
	s.mu.Lock()
	out.ServerVersion = nonEmpty(s.version)
	out.LastSyncError = nonEmpty(s.lastErr)
	if !s.lastSyncAt.IsZero() {
		t := s.lastSyncAt.UTC()
		out.LastSyncAt = &t
	}
	s.mu.Unlock()
	for _, d := range rows {
		dj := deliveryJSON{TaskID: d.TaskID, ProjectKey: d.ProjectKey, ReceivedAt: d.ReceivedAt.Time.UTC(), Outcome: d.Outcome, Detail: d.Detail}
		if d.ServiceID.Valid {
			id := d.ServiceID.Int64
			dj.ServiceID = &id
			if d.ServiceName.Valid {
				name := d.ServiceName.String
				dj.ServiceName = &name
			}
		}
		out.RecentDeliveries = append(out.RecentDeliveries, dj)
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}
