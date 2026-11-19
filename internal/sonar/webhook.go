// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package sonar

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/httpapi"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const (
	maxWebhookBody  = 1 << 20
	signatureHeader = "X-Sonar-Webhook-HMAC-SHA256"
)

// Delivery outcomes stored in sonar_deliveries.
const (
	outcomeLinked   = "linked"
	outcomeUnlinked = "unlinked"
	outcomeFailed   = "failed"
)

// webhookPayload is the part of the SonarQube webhook body that is used.
type webhookPayload struct {
	TaskID     string `json:"taskId"`
	Status     string `json:"status"`
	AnalysedAt string `json:"analysedAt"`
	Revision   string `json:"revision"`
	Project    struct {
		Key string `json:"key"`
	} `json:"project"`
	QualityGate *struct {
		Status     string `json:"status"`
		Conditions []struct {
			Metric         string  `json:"metric"`
			Operator       string  `json:"operator"`
			Value          *string `json:"value"`
			Status         string  `json:"status"`
			ErrorThreshold string  `json:"errorThreshold"`
		} `json:"conditions"`
	} `json:"qualityGate"`
	Properties map[string]string `json:"properties"`
}

// delivery is a verified, recorded webhook waiting to be processed.
type delivery struct {
	payload webhookPayload
}

// validSignature reports whether sig, the hex HMAC-SHA256 of body under
// secret, is correct. SonarQube sends lowercase hex; either case is read as
// hex and the bytes are compared in constant time.
func validSignature(secret string, body []byte, sig string) bool {
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), got)
}

func (s *Sonar) webhook(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Current()
	if !snap.SonarEnabled() || snap.SonarWebhookSecret == "" {
		httpapi.WriteError(w, http.StatusServiceUnavailable, "sonar_not_configured",
			"The SonarQube integration is not configured. Set the SonarQube URL, token and webhook secret in the settings.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httpapi.WriteError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "The webhook payload is larger than 1 MiB.")
			return
		}
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_payload", "The request body could not be read.")
		return
	}
	if !validSignature(snap.SonarWebhookSecret, body, r.Header.Get(signatureHeader)) {
		httpapi.WriteError(w, http.StatusUnauthorized, "invalid_signature",
			"The "+signatureHeader+" header is missing or does not match the webhook secret.")
		return
	}
	var p webhookPayload
	if err := json.Unmarshal(body, &p); err != nil || p.TaskID == "" || p.Project.Key == "" {
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_payload", "The payload is not a SonarQube webhook (taskId and project.key are required).")
		return
	}

	n, err := s.q.InsertSonarDelivery(r.Context(), store.InsertSonarDeliveryParams{
		TaskID: p.TaskID, ProjectKey: p.Project.Key,
		ReceivedAt: pgtype.Timestamptz{Time: s.now(), Valid: true},
	})
	if err != nil {
		s.log.ErrorContext(r.Context(), "record SonarQube delivery", "error", err)
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "Something went wrong. Try again later.")
		return
	}
	if n == 1 {
		select {
		case s.queue <- delivery{payload: p}:
		default:
			s.log.WarnContext(r.Context(), "SonarQube delivery queue is full", "task_id", p.TaskID)
			s.finish(r.Context(), p.TaskID, outcomeFailed, nil, "The processing queue was full; the next sync will read the analysis.")
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Sonar) finish(ctx context.Context, taskID, outcome string, serviceID *int64, detail string) {
	arg := store.FinishSonarDeliveryParams{TaskID: taskID, Outcome: outcome, Detail: detail}
	if serviceID != nil {
		arg.ServiceID = pgtype.Int8{Int64: *serviceID, Valid: true}
	}
	if err := s.q.FinishSonarDelivery(ctx, arg); err != nil {
		s.log.ErrorContext(ctx, "update SonarQube delivery", "task_id", taskID, "error", err)
	}
}

// linkService finds the service of a project: by the GitLab project ID the
// pipeline passed as a scanner property, else by the SonarQube project key.
func (s *Sonar) linkService(ctx context.Context, p webhookPayload) (int64, bool, error) {
	if raw := p.Properties["sonar.analysis.gitlabProjectId"]; raw != "" {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
			sid, err := s.q.SonarServiceByGitlabProject(ctx, pgtype.Int8{Int64: id, Valid: true})
			if err == nil {
				return sid, true, nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return 0, false, err
			}
		}
	}
	sid, err := s.q.SonarServiceByKey(ctx, pgtype.Text{String: p.Project.Key, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return sid, err == nil, err
}

// process turns a recorded delivery into a stored analysis. Every path ends
// by setting the delivery's outcome.
func (s *Sonar) process(ctx context.Context, d delivery) {
	p := d.payload
	fail := func(sid *int64, format string, args ...any) {
		detail := fmt.Sprintf(format, args...)
		s.log.WarnContext(ctx, "SonarQube delivery failed", "task_id", p.TaskID, "project", p.Project.Key, "detail", detail)
		s.finish(ctx, p.TaskID, outcomeFailed, sid, detail)
	}

	sid, linked, err := s.linkService(ctx, p)
	if err != nil {
		fail(nil, "Looking up the service failed: %v", err)
		return
	}
	if !linked {
		s.finish(ctx, p.TaskID, outcomeUnlinked, nil, fmt.Sprintf(
			"No service is linked to SonarQube project %q. Pass -Dsonar.analysis.gitlabProjectId=$CI_PROJECT_ID to the scanner, or set the SonarQube key on the service.",
			p.Project.Key))
		return
	}
	if p.Status != "SUCCESS" {
		fail(&sid, "The analysis task ended with status %q; nothing was stored.", p.Status)
		return
	}
	analysedAt, err := parseTime(p.AnalysedAt)
	if err != nil {
		fail(&sid, "The webhook has no readable analysedAt (%q).", p.AnalysedAt)
		return
	}
	snap := s.cfg.Current()
	if !snap.SonarEnabled() {
		fail(&sid, "The SonarQube URL and token are not configured, so the analysis cannot be read.")
		return
	}
	cl := s.client(snap)
	measures, err := cl.measures(ctx, p.Project.Key)
	if err != nil {
		fail(&sid, "Reading measures failed: %v. The next sync will retry.", err)
		return
	}
	latest, err := cl.latestAnalysis(ctx, p.Project.Key)
	if err != nil {
		fail(&sid, "Reading the latest analysis failed: %v. The next sync will retry.", err)
		return
	}

	a := snapshot{
		ServiceID: sid, TaskID: p.TaskID, AnalysedAt: analysedAt, Revision: p.Revision,
		Measures: measures, Conditions: []Condition{},
	}
	if p.QualityGate != nil {
		a.Gate = p.QualityGate.Status
		for _, c := range p.QualityGate.Conditions {
			a.Conditions = append(a.Conditions, Condition{
				Metric: c.Metric, Operator: c.Operator, Threshold: c.ErrorThreshold, Value: c.Value, Status: c.Status,
			})
		}
	}
	if raw := p.Properties["sonar.analysis.pipelineId"]; raw != "" {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
			a.Pipeline = &id
		}
	}
	detail := ""
	switch {
	case latest == nil:
		detail = "SonarQube reports no analysis for the project; the measures may not belong to this analysis."
	case latest.Date.Equal(analysedAt):
		a.AnalysisKey = latest.Key
		if a.Revision == "" {
			a.Revision = latest.Revision
		}
	case latest.Date.After(analysedAt):
		detail = fmt.Sprintf("A newer analysis (%s) already exists in SonarQube; the measures are from it. The next sync will add it.",
			latest.Date.UTC().Format(time.RFC3339))
	default:
		detail = "The latest analysis in SonarQube is older than the one in the webhook; the measures may not belong to it."
	}

	if a.AnalysisKey != "" {
		known, err := s.q.SonarAnalysisKnown(ctx, store.SonarAnalysisKnownParams{ServiceID: sid, AnalysisKey: a.AnalysisKey})
		if err != nil {
			fail(&sid, "Checking stored analyses failed: %v", err)
			return
		}
		if known {
			s.finish(ctx, p.TaskID, outcomeLinked, &sid, "The analysis was already stored by a sync.")
			return
		}
	}
	if err := s.save(ctx, a); err != nil {
		fail(&sid, "Storing the analysis failed: %v", err)
		return
	}
	s.finish(ctx, p.TaskID, outcomeLinked, &sid, detail)
}

// snapshot is an analysis ready to be stored.
type snapshot struct {
	ServiceID   int64
	TaskID      string
	AnalysisKey string
	AnalysedAt  time.Time
	Revision    string
	Gate        string
	Conditions  []Condition
	Measures    map[string]string
	Pipeline    *int64
}

func (s *Sonar) save(ctx context.Context, a snapshot) error {
	conds, err := json.Marshal(a.Conditions)
	if err != nil {
		return err
	}
	measures, err := json.Marshal(a.Measures)
	if err != nil {
		return err
	}
	n := normalise(a.Measures)
	arg := store.InsertSonarAnalysisParams{
		ServiceID: a.ServiceID, TaskID: a.TaskID, AnalysisKey: a.AnalysisKey,
		AnalysedAt: pgtype.Timestamptz{Time: a.AnalysedAt, Valid: true}, Revision: a.Revision,
		QualityGateStatus: a.Gate, GateConditions: conds, Measures: measures, Mode: n.Mode,
		ReliabilityRating: n.Reliability, SecurityRating: n.Security,
		MaintainabilityRating: n.Maintainability, SecurityReviewRating: n.SecurityReview,
		ReceivedAt: pgtype.Timestamptz{Time: s.now(), Valid: true},
	}
	if n.Coverage != nil {
		arg.Coverage = pgtype.Float8{Float64: *n.Coverage, Valid: true}
	}
	if n.Duplication != nil {
		arg.DuplicatedLinesDensity = pgtype.Float8{Float64: *n.Duplication, Valid: true}
	}
	if a.Pipeline != nil {
		arg.GitlabPipelineID = pgtype.Int8{Int64: *a.Pipeline, Valid: true}
	}
	return s.q.InsertSonarAnalysis(ctx, arg)
}
