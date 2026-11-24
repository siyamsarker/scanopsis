// Copyright 2026 Siyam Sarker
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
	"net/netip"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/audit"
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
	outcomePending  = "pending"
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
			s.reject(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "The webhook payload is larger than 1 MiB.")
			return
		}
		httpapi.WriteError(w, http.StatusBadRequest, "invalid_payload", "The request body could not be read.")
		return
	}
	if !validSignature(snap.SonarWebhookSecret, body, r.Header.Get(signatureHeader)) {
		s.reject(w, r, http.StatusUnauthorized, "invalid_signature",
			"The "+signatureHeader+" header is missing or does not match the webhook secret.")
		return
	}
	var p webhookPayload
	if err := json.Unmarshal(body, &p); err != nil || p.TaskID == "" || p.Project.Key == "" {
		s.reject(w, r, http.StatusBadRequest, "invalid_payload", "The payload is not a SonarQube webhook (taskId and project.key are required).")
		return
	}

	arg := store.InsertSonarDeliveryParams{
		TaskID: p.TaskID, ProjectKey: p.Project.Key,
		ReceivedAt: pgtype.Timestamptz{Time: s.now(), Valid: true},
	}
	// Kept so that the delivery can be linked when the first upload of the
	// same pipeline creates the service of the project (see LinkWaiting).
	if id, ok := claimedProject(p); ok {
		arg.ClaimedProjectID = pgtype.Int8{Int64: id, Valid: true}
	}
	if id, ok := claimedPipeline(p); ok {
		arg.ClaimedPipelineID = pgtype.Int8{Int64: id, Valid: true}
	}
	n, err := s.q.InsertSonarDelivery(r.Context(), arg)
	if err != nil {
		s.log.ErrorContext(r.Context(), "record SonarQube delivery", "error", err)
		httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "Something went wrong. Try again later.")
		return
	}
	if n == 1 {
		select {
		case s.queue <- delivery{payload: p}:
		default:
			// The delivery stays pending; the next sync reads the analysis of
			// a project linked by key and marks it linked.
			s.warnQueueFull(r.Context())
			s.finish(r.Context(), p.TaskID, outcomePending, nil, queuedDetail)
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

// warnQueueFull logs that the queue is full, at most once a minute, so a
// burst does not write a line per delivery.
func (s *Sonar) warnQueueFull(ctx context.Context) {
	now := s.now().UnixNano()
	last := s.queueWarned.Load()
	if now-last < int64(time.Minute) || !s.queueWarned.CompareAndSwap(last, now) {
		return
	}
	s.log.WarnContext(ctx, "SonarQube delivery queue is full; deliveries wait for the next sync", "queue_size", cap(s.queue))
}

// reject answers a refused webhook, records webhook.rejected with the reason
// and the client address, and logs it. Anyone who can reach the endpoint can
// cause a rejection, so records are bounded per client address: beyond the
// burst, a rejection is logged at debug level only. The response is the same
// either way.
func (s *Sonar) reject(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	httpapi.WriteError(w, status, code, message)
	var ip netip.Addr
	if s.clientIP != nil {
		ip = s.clientIP(r).Unmap()
	}
	addr := ""
	if ip.IsValid() {
		addr = ip.String()
	}
	attrs := []any{"status", status, "reason", code, "ip", addr}
	if ok, _ := s.auditLimit.Allow(limitKey(ip)); !ok {
		s.log.DebugContext(r.Context(), "webhook rejected; not recorded: too many rejections from this address", attrs...)
		return
	}
	s.log.WarnContext(r.Context(), "webhook rejected", attrs...)
	if s.audit == nil {
		return
	}
	// The client may be gone, so the record does not depend on the request,
	// but it cannot hold the request for long either.
	actx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), auditTimeout)
	defer cancel()
	s.audit.Record(actx, "sonarqube:unknown", audit.ActionWebhookRejected, map[string]any{"reason": code, "ip": addr})
}

// limitKey is the rate-limit key of a client address: an IPv4 address, an
// IPv6 /64, or "unknown".
func limitKey(a netip.Addr) string {
	switch {
	case !a.IsValid():
		return "unknown"
	case a.Is4():
		return a.String()
	}
	return netip.PrefixFrom(a, 64).Masked().String()
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

// link is the result of resolving a delivery to a service.
type link struct {
	ServiceID int64
	// Pin is set when the service has no SonarQube key yet and must be linked
	// to the project when its first analysis is stored.
	Pin bool
	// Unlinked explains why no service was found; empty when one was.
	Unlinked string
	// Waiting is set when the delivery names a GitLab project that has no
	// service yet, and the pipeline whose first upload may create it.
	Waiting                  bool
	WaitingFor, WaitPipeline int64
}

// linkService finds the service of a project. The signature only proves the
// delivery came from SonarQube, not which service it belongs to, so the
// project key decides first. The gitlabProjectId scanner property, which any
// pipeline can set, is used only for a service that is not linked to a
// SonarQube project yet, and then pins that link; it can never move a
// service to another project.
func (s *Sonar) linkService(ctx context.Context, p webhookPayload) (link, error) {
	sid, err := s.q.SonarServiceByKey(ctx, pgtype.Text{String: p.Project.Key, Valid: true})
	if err == nil {
		return link{ServiceID: sid}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return link{}, err
	}
	id, ok := claimedProject(p)
	if !ok {
		return link{Unlinked: fmt.Sprintf(
			`No service is linked to SonarQube project %q. Pass -Dsonar.analysis.gitlabProjectId="$CI_PROJECT_ID" and -Dsonar.analysis.pipelineId="$CI_PIPELINE_ID" to the scanner, or set the SonarQube key on the service.`,
			p.Project.Key)}, nil
	}
	svc, err := s.q.SonarServiceByGitlabProject(ctx, pgtype.Int8{Int64: id, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return s.waitingLink(ctx, p, id)
	}
	if err != nil {
		return link{}, err
	}
	if !svc.SonarProjectKey.Valid {
		return link{ServiceID: svc.ID, Pin: true}, nil
	}
	return link{Unlinked: fmt.Sprintf(
		"The service named by sonar.analysis.gitlabProjectId is linked to a different SonarQube project, so project %q was not linked to it. Change the SonarQube key on the service if the project was renamed.",
		p.Project.Key)}, nil
}

// waitingLink explains a delivery that names GitLab project id when no
// active service has it. Typically the analysis ran before the first scan of
// the project; with a pipeline ID, LinkWaiting links it when the upload of
// that pipeline creates the service. An archived service is never linked.
func (s *Sonar) waitingLink(ctx context.Context, p webhookPayload, id int64) (link, error) {
	archived, err := s.q.SonarArchivedProject(ctx, pgtype.Int8{Int64: id, Valid: true})
	if err != nil {
		return link{}, err
	}
	if archived {
		return link{Unlinked: fmt.Sprintf(
			"The service of GitLab project %d is archived, so SonarQube project %q was not linked. An administrator must set the SonarQube key on the service after unarchiving it.",
			id, p.Project.Key)}, nil
	}
	pipeline, ok := claimedPipeline(p)
	if !ok {
		return link{Unlinked: fmt.Sprintf(
			`No service is linked to SonarQube project %q, and no service has GitLab project %d yet. Pass -Dsonar.analysis.pipelineId="$CI_PIPELINE_ID" to the scanner as well, so that the analysis is linked when the scan of the same pipeline arrives, or set the SonarQube key on the service.`,
			p.Project.Key, id)}, nil
	}
	return link{Waiting: true, WaitingFor: id, WaitPipeline: pipeline, Unlinked: fmt.Sprintf(
		"No service is linked to SonarQube project %q yet. The analysis is linked when the scan of the same pipeline (%d) reaches Scanopsis and creates the service of GitLab project %d; you can also set the SonarQube key on the service.",
		p.Project.Key, pipeline, id)}, nil
}

// claimedPipeline returns the GitLab pipeline ID in the
// sonar.analysis.pipelineId scanner property, if it is a number.
func claimedPipeline(p webhookPayload) (int64, bool) {
	id, err := strconv.ParseInt(p.Properties["sonar.analysis.pipelineId"], 10, 64)
	return id, err == nil
}

// claimedProject returns the GitLab project ID in the
// sonar.analysis.gitlabProjectId scanner property, if it is a number.
func claimedProject(p webhookPayload) (int64, bool) {
	id, err := strconv.ParseInt(p.Properties["sonar.analysis.gitlabProjectId"], 10, 64)
	return id, err == nil
}

// briefError describes a failed Web API call without internals.
func briefError(err error) string {
	var ae *apiError
	if errors.As(err, &ae) {
		return fmt.Sprintf("SonarQube answered %d on %s", ae.Status, ae.Endpoint)
	}
	return "the request failed or the answer was unreadable; see the server log"
}

// process turns a recorded delivery into a stored analysis. Every path ends
// by setting the delivery's outcome.
func (s *Sonar) process(ctx context.Context, d delivery) {
	p := d.payload
	// fail records a short message for administrators and logs the cause.
	fail := func(sid *int64, detail string, cause error) {
		s.log.WarnContext(ctx, "SonarQube delivery failed", "task_id", p.TaskID, "project", p.Project.Key, "detail", detail, "error", cause)
		s.finish(ctx, p.TaskID, outcomeFailed, sid, detail)
	}

	l, err := s.linkService(ctx, p)
	if err != nil {
		fail(nil, "Looking up the service failed; see the server log.", err)
		return
	}
	if l.Unlinked != "" {
		s.finish(ctx, p.TaskID, outcomeUnlinked, nil, l.Unlinked)
		if l.Waiting {
			s.linkLate(ctx, l.WaitingFor, l.WaitPipeline)
		}
		return
	}
	sid := l.ServiceID
	if p.Status != "SUCCESS" {
		fail(&sid, fmt.Sprintf("The analysis task ended with status %q; nothing was stored.", p.Status), nil)
		return
	}
	analysedAt, err := parseTime(p.AnalysedAt)
	if err != nil {
		fail(&sid, "The webhook has no readable analysedAt.", err)
		return
	}
	snap := s.cfg.Current()
	if !snap.SonarEnabled() {
		fail(&sid, "The SonarQube URL and token are not configured, so the analysis cannot be read.", nil)
		return
	}
	cl := s.client(snap)
	measures, err := cl.measures(ctx, p.Project.Key)
	if err != nil {
		fail(&sid, "Reading measures failed: "+briefError(err)+". The next sync will retry.", err)
		return
	}
	latest, err := cl.latestAnalysis(ctx, p.Project.Key)
	if err != nil {
		fail(&sid, "Reading the latest analysis failed: "+briefError(err)+". The next sync will retry.", err)
		return
	}

	a := snapshot{
		ServiceID: sid, TaskID: p.TaskID, AnalysedAt: analysedAt, Revision: p.Revision,
		Measures: measures, Conditions: []Condition{},
		ClaimedProjectID: p.Properties["sonar.analysis.gitlabProjectId"],
	}
	if p.QualityGate != nil {
		a.Gate = p.QualityGate.Status
		for _, c := range p.QualityGate.Conditions {
			a.Conditions = append(a.Conditions, Condition{
				Metric: c.Metric, Operator: c.Operator, Threshold: c.ErrorThreshold, Value: c.Value, Status: c.Status,
			})
		}
	}
	if id, ok := claimedPipeline(p); ok {
		a.Pipeline = &id
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
			fail(&sid, "Checking stored analyses failed; see the server log.", err)
			return
		}
		if known {
			s.finish(ctx, p.TaskID, outcomeLinked, &sid, "The analysis was already stored by a sync.")
			return
		}
	}
	pin := ""
	if l.Pin {
		pin = p.Project.Key
	}
	switch err := s.save(ctx, a, pin); {
	case errors.Is(err, errPinConflict):
		s.finish(ctx, p.TaskID, outcomeUnlinked, nil, fmt.Sprintf(
			"The service named by sonar.analysis.gitlabProjectId cannot be linked to SonarQube project %q because the service or the key is already linked.", p.Project.Key))
		return
	case err != nil:
		fail(&sid, "Storing the analysis failed; see the server log.", err)
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
	// ClaimedProjectID is the gitlabProjectId scanner property, recorded in
	// the audit event when the analysis links a service.
	ClaimedProjectID string
}

// errPinConflict is returned when a service cannot be linked to a project.
var errPinConflict = errors.New("service or SonarQube key already linked")

// save stores an analysis. With a pin key it first links the service to that
// SonarQube project, in the same transaction.
func (s *Sonar) save(ctx context.Context, a snapshot, pinKey string) error {
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
	if pinKey == "" {
		return s.q.InsertSonarAnalysis(ctx, arg)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := s.q.WithTx(tx)
	path, err := q.PinSonarKey(ctx, store.PinSonarKeyParams{ID: a.ServiceID, SonarProjectKey: pgtype.Text{String: pinKey, Valid: true}})
	var pgErr *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgErr) && pgErr.Code == "23505") { // unique_violation
		return errPinConflict
	}
	if err != nil {
		return err
	}
	if err := auditPin(ctx, q, a.ServiceID, path, pinKey, a.TaskID, a.ClaimedProjectID, a.Pipeline); err != nil {
		return err
	}
	if err := q.InsertSonarAnalysis(ctx, arg); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// auditPin records, in the transaction of q, that a service was linked to a
// SonarQube project by the gitlabProjectId scanner property of the delivery
// taskID.
func auditPin(ctx context.Context, q *store.Queries, serviceID int64, path, key, taskID, claimed string, pipeline *int64) error {
	detail, err := json.Marshal(map[string]any{
		"id": serviceID, "gitlab_project_path": path, "fields": []string{"sonar_project_key"},
		"sonar_project_key": key, "reason": "linked by the sonar.analysis.gitlabProjectId scanner property",
		"task_id": taskID, "claimed_gitlab_project_id": claimed, "pipeline_id": pipeline,
	})
	if err != nil {
		return err
	}
	return q.InsertAudit(ctx, store.InsertAuditParams{Actor: "sonarqube", Action: audit.ActionServiceUpdated, Detail: detail})
}
