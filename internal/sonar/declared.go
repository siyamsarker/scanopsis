// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package sonar

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/store"
)

// declaredLinkedDetail is the detail of a delivery linked because a service
// now has its project key, set by its pipeline's upload.
const declaredLinkedDetail = "Linked when an upload of the service's pipeline declared this SonarQube project key; the newest analysis is read from SonarQube."

// keyLinkedDetail is the detail of a delivery linked because a service was
// given its project key while it was processed.
const keyLinkedDetail = "Linked when a service was given this SonarQube project key while the delivery was processed; the newest analysis is read from SonarQube."

// LinkDeclared follows an upload that set key as the SonarQube key of its
// service (the sonar_key upload parameter), because a delivery of key named
// the service's GitLab project and so confirmed the declaration. The key
// was set in the upload's transaction, before any other key could be linked
// by an analysis that merely names the project. The unlinked deliveries of
// key from the last 7 days that name the service's GitLab project are
// marked linked, every other unlinked delivery naming gitlabProjectID says
// why it was not linked, and the project is synced in the background by
// Run's workers. Nothing is done unless the service has key now.
func (s *Sonar) LinkDeclared(ctx context.Context, serviceID, gitlabProjectID int64, key string) error {
	cur, err := s.q.SonarServiceKey(ctx, serviceID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (!cur.Valid || cur.String != key) {
		return nil
	}
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := s.q.WithTx(tx)
	rows, err := s.linkKeyDeliveries(ctx, q, serviceID, key, declaredLinkedDetail)
	if err != nil {
		return err
	}
	if _, err := q.ExplainUnlinkedSonarClaims(ctx, store.ExplainUnlinkedSonarClaimsParams{
		ClaimedProjectID: pgtype.Int8{Int64: gitlabProjectID, Valid: true},
		Detail:           declaredClaimDetail(gitlabProjectID, key),
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.queueLinked(ctx, serviceID, key, rows)
	return nil
}

// declaredClaimDetail says why a delivery naming gitlabProjectID was not
// linked once the project's service is linked to key, the key its pipeline
// declared.
func declaredClaimDetail(gitlabProjectID int64, key string) string {
	return fmt.Sprintf(
		"Not linked: the service of GitLab project %d is linked to SonarQube project %q, the key its pipeline declared in its upload. An administrator can change the SonarQube key on the service.",
		gitlabProjectID, key)
}

// linkKeyLate links a delivery that has just finished unlinked, if a service
// has its project key by now and the delivery names the service's GitLab
// project. An upload may set the key while the delivery is processed: it
// then links the deliveries of the key before this one is marked unlinked,
// and this check finds the service instead. A failure is logged.
func (s *Sonar) linkKeyLate(ctx context.Context, key string) {
	sid, err := s.q.SonarServiceByKey(ctx, pgtype.Text{String: key, Valid: true})
	var rows []store.LinkSonarDeliveriesOfKeyRow
	if err == nil {
		if rows, err = s.linkKeyDeliveries(ctx, s.q, sid, key, keyLinkedDetail); err == nil && len(rows) > 0 {
			s.queueLinked(ctx, sid, key, rows)
		}
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
		s.log.WarnContext(ctx, "cannot link an unlinked SonarQube delivery", "project", key, "error", err)
	}
}

// linkKeyDeliveries marks the unlinked deliveries of key from the last 7
// days that name the service's GitLab project linked to it, with detail.
func (s *Sonar) linkKeyDeliveries(ctx context.Context, q *store.Queries, serviceID int64, key, detail string) ([]store.LinkSonarDeliveriesOfKeyRow, error) {
	return q.LinkSonarDeliveriesOfKey(ctx, store.LinkSonarDeliveriesOfKeyParams{
		ServiceID: pgtype.Int8{Int64: serviceID, Valid: true}, Detail: detail,
		Since: pgtype.Timestamptz{Time: s.now().Add(-waitingWindow), Valid: true}, ProjectKey: key,
	})
}

// queueLinked has a worker read the newest analysis of a project that has
// just been linked by the deliveries rows. The analysis gets the pipeline of
// the delivery it is provably from (see deliveries.pipelineOf).
func (s *Sonar) queueLinked(ctx context.Context, serviceID int64, key string, rows []store.LinkSonarDeliveriesOfKeyRow) {
	l := linkedProject{SonarLinkedServicesRow: store.SonarLinkedServicesRow{ID: serviceID, SonarProjectKey: key}}
	for _, r := range rows {
		if r.ClaimedPipelineID.Valid && r.AnalysedAt.Valid {
			l.from = append(l.from, delivered{pipeline: r.ClaimedPipelineID.Int64, analysedAt: r.AnalysedAt.Time})
		}
	}
	s.queueSync(ctx, l)
}
