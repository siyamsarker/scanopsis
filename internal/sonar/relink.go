// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package sonar

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/store"
)

// waitingLinkedDetail is the detail of a delivery that waited for the service
// of its GitLab project and was linked once the service existed.
const waitingLinkedDetail = "Linked by the sonar.analysis.gitlabProjectId scanner property once the service of the GitLab project existed; the newest analysis is read from SonarQube."

var (
	// errKeyTaken: another service, maybe an archived one, has the key.
	errKeyTaken = errors.New("SonarQube key already linked to another service")
	// errServiceTaken: the service was linked or archived in the meantime.
	errServiceTaken = errors.New("service already linked")
)

// LinkWaiting links a service to the SonarQube project of analyses that
// arrived before it existed. A pipeline that runs its analysis before its
// first Trivy scan delivers the analysis while no service has the project's
// GitLab ID, so the delivery stays unlinked with that ID recorded. Once the
// service with gitlabProjectID exists (it was created, or received the ID),
// the newest such delivery whose project key no service has yet links it, by
// the same rules and with the same audit record as a webhook: only a service
// without a SonarQube key is linked, and a key is never linked to a second
// service. The deliveries of that key are then marked linked and the project
// is synced in the background by Run's workers. Without waiting deliveries,
// or when the rules forbid the link, it does nothing.
func (s *Sonar) LinkWaiting(ctx context.Context, serviceID, gitlabProjectID int64) error {
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
	waiting, err := s.q.WaitingSonarDeliveries(ctx, gid)
	if err != nil {
		return err
	}
	tried := map[string]bool{}
	for _, d := range waiting {
		if tried[d.ProjectKey] {
			continue
		}
		tried[d.ProjectKey] = true
		switch err := s.pinWaiting(ctx, serviceID, gitlabProjectID, d); {
		case errors.Is(err, errKeyTaken):
			continue // an older delivery may name a free key
		case errors.Is(err, errServiceTaken):
			return nil
		case err != nil:
			return err
		}
		l := store.SonarLinkedServicesRow{ID: serviceID, SonarProjectKey: d.ProjectKey}
		select {
		case s.linked <- l:
		default:
			s.log.InfoContext(ctx, "SonarQube project linked; the next sync reads its analysis", "project", d.ProjectKey, "service_id", serviceID)
		}
		return nil
	}
	return nil
}

// pinWaiting links the service to the project of d and marks the waiting
// deliveries of that project linked, with the audit record, in one
// transaction.
func (s *Sonar) pinWaiting(ctx context.Context, serviceID, gitlabProjectID int64, d store.WaitingSonarDeliveriesRow) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := s.q.WithTx(tx)
	path, err := q.PinSonarKey(ctx, store.PinSonarKeyParams{ID: serviceID, SonarProjectKey: pgtype.Text{String: d.ProjectKey, Valid: true}})
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return errServiceTaken
	case errors.As(err, &pgErr) && pgErr.Code == "23505": // unique_violation
		return errKeyTaken
	case err != nil:
		return err
	}
	claimed := strconv.FormatInt(gitlabProjectID, 10)
	if err := auditPin(ctx, q, serviceID, path, d.ProjectKey, d.TaskID, claimed, nil); err != nil {
		return err
	}
	if _, err := q.LinkWaitingSonarDeliveries(ctx, store.LinkWaitingSonarDeliveriesParams{
		ServiceID: pgtype.Int8{Int64: serviceID, Valid: true}, Detail: waitingLinkedDetail,
		ClaimedProjectID: pgtype.Int8{Int64: gitlabProjectID, Valid: true}, ProjectKey: d.ProjectKey,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// linkLate links a delivery that has just been marked as waiting for the
// service of gitlabProjectID, if that service exists by now. The first upload
// of the project may create the service while the delivery is processed: it
// then looks for waiting deliveries before this one is marked as waiting, and
// this check finds the service instead. A failure is logged.
func (s *Sonar) linkLate(ctx context.Context, gitlabProjectID int64) {
	svc, err := s.q.SonarServiceByGitlabProject(ctx, pgtype.Int8{Int64: gitlabProjectID, Valid: true})
	if err == nil && !svc.SonarProjectKey.Valid {
		err = s.LinkWaiting(ctx, svc.ID, gitlabProjectID)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
		s.log.WarnContext(ctx, "cannot link a waiting SonarQube delivery", "gitlab_project_id", gitlabProjectID, "error", err)
	}
}

// syncLinked reads the newest analysis of a project LinkWaiting has just
// linked. A failure is logged; the next sync reads the project again.
func (s *Sonar) syncLinked(ctx context.Context, l store.SonarLinkedServicesRow) {
	snap := s.cfg.Current()
	if !snap.SonarEnabled() {
		return
	}
	if err := s.syncProject(ctx, s.client(snap), l); err != nil && ctx.Err() == nil {
		s.warn(ctx, "reading the analysis of a newly linked SonarQube project failed; the next sync retries", l.SonarProjectKey, err)
	}
}
