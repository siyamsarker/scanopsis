// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package sonar

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/store"
)

// waitingWindow is how long an analysis waits for the first scan of its
// pipeline. A pipeline that takes longer is not expected.
const waitingWindow = 7 * 24 * time.Hour

// waitingLinkedDetail is the detail of a delivery that waited for the service
// of its GitLab project and was linked by the first scan of its pipeline.
const waitingLinkedDetail = "Linked when the first scan of the same pipeline created the service of the GitLab project; the newest analysis is read from SonarQube."

var (
	// errKeyTaken: another service, maybe an archived one, has the key.
	errKeyTaken = errors.New("SonarQube key already linked to another service")
	// errServiceTaken: the service was linked or archived in the meantime.
	errServiceTaken = errors.New("service already linked")
)

// LinkWaiting links a service to the SonarQube project of an analysis that
// arrived before the service existed. A pipeline that runs its analysis
// before its first Trivy scan delivers it while no service has the project's
// GitLab ID, so the delivery stays unlinked with the GitLab project and
// pipeline it names. Anyone who can run an analysis can name any project, so
// the claim alone is not trusted: when the upload of pipelineID, verified by
// its CI token, creates the service of gitlabProjectID or gives it that ID,
// only the analyses of that same pipeline from the last 7 days are linked.
//
// The rules are those of a webhook link: only a service without a SonarQube
// key is linked, a key is never linked to a second service, and the link has
// the same audit record. When analyses of more than one SonarQube project
// match, none is linked and an administrator must set the key. After a link
// the project is synced in the background by Run's workers, and every other
// unlinked delivery naming the GitLab project says why it was not linked.
func (s *Sonar) LinkWaiting(ctx context.Context, serviceID, gitlabProjectID, pipelineID int64) error {
	gid := pgtype.Int8{Int64: gitlabProjectID, Valid: true}
	svc, err := s.q.SonarServiceByGitlabProject(ctx, gid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if svc.ID != serviceID || svc.SonarProjectKey.Valid {
		return nil
	}
	w := waiting{
		serviceID: serviceID, project: gid, pipeline: pgtype.Int8{Int64: pipelineID, Valid: true},
		since: pgtype.Timestamptz{Time: s.now().Add(-waitingWindow), Valid: true},
	}
	keys, err := s.q.WaitingSonarKeys(ctx, store.WaitingSonarKeysParams{
		Since: w.since, ClaimedProjectID: w.project, ClaimedPipelineID: w.pipeline,
	})
	switch {
	case err != nil:
		return err
	case len(keys) == 0:
		return nil
	case len(keys) > 1:
		return s.explainWaiting(ctx, w, fmt.Sprintf(
			"More than one SonarQube project names GitLab project %d in pipeline %d, so none was linked automatically. An administrator must set the SonarQube key on the service.",
			gitlabProjectID, pipelineID))
	}
	k := keys[0]
	switch err := s.pinWaiting(ctx, w, k); {
	case errors.Is(err, errKeyTaken):
		return s.explainWaiting(ctx, w, fmt.Sprintf(
			"SonarQube project %q is already linked to another service, so it was not linked to the service of GitLab project %d.",
			k.ProjectKey, gitlabProjectID))
	case errors.Is(err, errServiceTaken):
		return nil
	case err != nil:
		return err
	}
	l := linkedProject{
		SonarLinkedServicesRow: store.SonarLinkedServicesRow{ID: serviceID, SonarProjectKey: k.ProjectKey},
		from:                   &delivered{pipeline: pipelineID, at: k.ReceivedAt.Time},
	}
	select {
	case s.linked <- l:
	default:
		s.log.InfoContext(ctx, "SonarQube project linked; the next sync reads its analysis", "project", k.ProjectKey, "service_id", serviceID)
	}
	return nil
}

// linkedProject is a project that has just been linked to a service, for a
// worker to read its newest analysis at once.
type linkedProject struct {
	store.SonarLinkedServicesRow
	// from is the delivery that linked it, nil when there is none.
	from *delivered
}

// delivered is a webhook delivery that named the pipeline of an analysis.
// The analysis is read back from SonarQube, whose Web API does not return
// the scanner's properties, so the pipeline comes from the delivery.
type delivered struct {
	pipeline int64
	// at is when the delivery arrived. An analysis that started later is a
	// newer one, not the analysis of the pipeline.
	at time.Time
}

// pipelineOf returns the pipeline of the delivery for an analysis that
// started at date, or nil when the analysis is not the delivered one.
func (d *delivered) pipelineOf(date time.Time) *int64 {
	if d == nil || d.pipeline == 0 || d.at.IsZero() || date.After(d.at) {
		return nil
	}
	p := d.pipeline
	return &p
}

// waiting names the deliveries LinkWaiting may link.
type waiting struct {
	serviceID         int64
	project, pipeline pgtype.Int8
	since             pgtype.Timestamptz
}

// explainWaiting sets the detail of the waiting deliveries of the pipeline
// that were not linked.
func (s *Sonar) explainWaiting(ctx context.Context, w waiting, detail string) error {
	_, err := s.q.ExplainWaitingSonarDeliveries(ctx, store.ExplainWaitingSonarDeliveriesParams{
		Detail: detail, Since: w.since, ClaimedProjectID: w.project, ClaimedPipelineID: w.pipeline,
	})
	return err
}

// pinWaiting links the service to the project of k, marks the waiting
// deliveries of that project linked and records the audit event, in one
// transaction. Every other unlinked delivery naming the GitLab project then
// says that another analysis was linked.
func (s *Sonar) pinWaiting(ctx context.Context, w waiting, k store.WaitingSonarKeysRow) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := s.q.WithTx(tx)
	path, err := q.PinSonarKey(ctx, store.PinSonarKeyParams{ID: w.serviceID, SonarProjectKey: pgtype.Text{String: k.ProjectKey, Valid: true}})
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return errServiceTaken
	case errors.As(err, &pgErr) && pgErr.Code == "23505": // unique_violation
		return errKeyTaken
	case err != nil:
		return err
	}
	claimed := strconv.FormatInt(w.project.Int64, 10)
	if err := auditPin(ctx, q, w.serviceID, path, k.ProjectKey, k.TaskID, claimed, &w.pipeline.Int64); err != nil {
		return err
	}
	if _, err := q.LinkWaitingSonarDeliveries(ctx, store.LinkWaitingSonarDeliveriesParams{
		ServiceID: pgtype.Int8{Int64: w.serviceID, Valid: true}, Detail: waitingLinkedDetail,
		Since: w.since, ClaimedProjectID: w.project, ClaimedPipelineID: w.pipeline, ProjectKey: k.ProjectKey,
	}); err != nil {
		return err
	}
	if _, err := q.ExplainUnlinkedSonarClaims(ctx, store.ExplainUnlinkedSonarClaimsParams{
		ClaimedProjectID: w.project,
		Detail: fmt.Sprintf(
			"Not linked: the service of GitLab project %d was linked to SonarQube project %q by the analysis of the pipeline of its first scan. An administrator can change the SonarQube key on the service.",
			w.project.Int64, k.ProjectKey),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// linkLate links a delivery that has just been marked as waiting for the
// service of gitlabProjectID, if that service exists by now. The first upload
// of the pipeline may create the service while the delivery is processed: it
// then looks for waiting deliveries before this one is marked as waiting, and
// this check finds the service instead. It follows the rule of LinkWaiting:
// the delivery's pipelineID must be the pipeline of the service's first
// scan, the upload that created the service or gave it its GitLab project
// ID. A failure is logged.
func (s *Sonar) linkLate(ctx context.Context, gitlabProjectID, pipelineID int64) {
	svc, err := s.q.SonarServiceByGitlabProject(ctx, pgtype.Int8{Int64: gitlabProjectID, Valid: true})
	if err == nil && !svc.SonarProjectKey.Valid {
		var first int64
		if first, err = s.q.FirstScanPipeline(ctx, svc.ID); err == nil && first == pipelineID {
			err = s.LinkWaiting(ctx, svc.ID, gitlabProjectID, pipelineID)
		}
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
		s.log.WarnContext(ctx, "cannot link a waiting SonarQube delivery", "gitlab_project_id", gitlabProjectID, "error", err)
	}
}

// syncLinked reads the newest analysis of a project LinkWaiting has just
// linked, with the pipeline of the delivery that linked it. A failure is
// logged; the next sync reads the project again.
func (s *Sonar) syncLinked(ctx context.Context, l linkedProject) {
	snap := s.cfg.Current()
	if !snap.SonarEnabled() {
		return
	}
	if err := s.syncProject(ctx, s.client(snap), l.SonarLinkedServicesRow, l.from); err != nil && ctx.Err() == nil {
		s.warn(ctx, "reading the analysis of a newly linked SonarQube project failed; the next sync retries", l.SonarProjectKey, err)
	}
}
