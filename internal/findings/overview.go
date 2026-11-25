// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/siyamsarker/scanopsis/internal/domain"
	"github.com/siyamsarker/scanopsis/internal/httpapi"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const topPackages = 10

type packageImpactJSON struct {
	PkgName           string     `json:"pkg_name"`
	InstalledVersion  string     `json:"installed_version"`
	FixVersion        string     `json:"fix_version"`
	Findings          int        `json:"findings"`
	BySeverity        countsJSON `json:"by_severity"`
	Fixable           int        `json:"fixable"`
	FixableBySeverity countsJSON `json:"fixable_by_severity"`
	Risk              int        `json:"risk"`
	RiskCleared       int        `json:"risk_cleared"`
}

type overviewJSON struct {
	Service            serviceJSON           `json:"service"`
	Ref                *string               `json:"ref"`
	RefSource          string                `json:"ref_source"`
	Refs               []string              `json:"refs"`
	Artifacts          []scanSummaryJSON     `json:"artifacts"`
	Counts             countsJSON            `json:"counts"`
	Fixable            int                   `json:"fixable"`
	Secrets            int                   `json:"secrets"`
	Suppressed         int                   `json:"suppressed"`
	NewSincePrevious   int                   `json:"new_since_previous"`
	FixedSincePrevious int                   `json:"fixed_since_previous"`
	ByClass            map[string]countsJSON `json:"by_class"`
	TopPackages        []packageImpactJSON   `json:"top_packages"`
	DataAt             *time.Time            `json:"data_at"`
}

// overview validates the request, reads everything from one read-only
// REPEATABLE READ transaction so the artifacts, the counts per class, the
// package groups and the changes come from the same snapshot even when a scan
// is ingested meanwhile, ends the transaction, and only then writes the
// response: a slow client never holds a connection or an open transaction.
func (f *Findings) overview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		notFound(w, "service")
		return
	}
	requested, ok := refParam(w, r)
	if !ok {
		return
	}
	ov, err := f.loadOverview(r.Context(), id, requested)
	var stage *stageError
	switch {
	case errors.Is(err, errNoService):
		notFound(w, "service")
	case errors.As(err, &stage):
		f.internalError(w, r, stage.msg, stage.err)
	case err != nil:
		f.internalError(w, r, "cannot read overview", err)
	default:
		httpapi.WriteJSON(w, http.StatusOK, ov)
	}
}

var errNoService = errors.New("no such service")

// stageError names the step of a read that failed, for the log.
type stageError struct {
	msg string
	err error
}

func (e *stageError) Error() string { return e.msg + ": " + e.err.Error() }
func (e *stageError) Unwrap() error { return e.err }

func stage(msg string, err error) error { return &stageError{msg, err} }

// loadOverview builds the overview of a service inside one snapshot. The
// transaction is rolled back (it has nothing to commit) before it returns.
func (f *Findings) loadOverview(ctx context.Context, serviceID int64, requested string) (*overviewJSON, error) {
	tx, err := f.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, stage("cannot begin read transaction", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	snap := *f
	snap.q = f.q.WithTx(tx)
	return snap.overviewIn(ctx, serviceID, requested)
}

// overviewIn builds the overview with f.q bound to one snapshot.
func (f *Findings) overviewIn(ctx context.Context, serviceID int64, requested string) (*overviewJSON, error) {
	svc, err := f.q.FindingsGetService(ctx, serviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNoService
	}
	if err != nil {
		return nil, stage("cannot load service", err)
	}
	refs, err := f.refs(ctx, svc.ID)
	if err != nil {
		return nil, stage("cannot list refs", err)
	}
	ref, source := pickRef(requested, svc.DefaultRef, refs)
	ov := &overviewJSON{
		Service: newServiceJSON(svc.ID, svc.Name, svc.GitlabProjectID, svc.GitlabProjectPath,
			svc.SonarProjectKey, svc.CreatedVia, svc.Archived, svc.CreatedAt, svc.DefaultRef),
		RefSource:   source,
		Refs:        refs,
		Artifacts:   []scanSummaryJSON{},
		ByClass:     map[string]countsJSON{},
		TopPackages: []packageImpactJSON{},
	}
	rows, ids, err := f.currentScans(ctx, svc.ID, ref)
	if err != nil {
		return nil, stage("cannot load current scans", err)
	}
	if ref != "" {
		ov.Ref = &ref
	}
	if len(rows) == 0 {
		return ov, nil
	}
	var newest time.Time
	for _, row := range rows {
		s := newScanSummary(row)
		ov.Artifacts = append(ov.Artifacts, s)
		ov.Counts = ov.Counts.add(s.Counts)
		ov.Fixable += s.Fixable
		ov.Secrets += s.Secrets
		ov.Suppressed += s.Suppressed
		if s.ScannedAt.After(newest) {
			newest = s.ScannedAt
		}
	}
	ov.DataAt = &newest

	classes, err := f.q.FindingsClassCounts(ctx, ids)
	if err != nil {
		return nil, stage("cannot count classes", err)
	}
	byClass := map[string]*domain.SeverityCounts{}
	for _, c := range classes {
		sc := byClass[c.Class]
		if sc == nil {
			sc = &domain.SeverityCounts{}
			byClass[c.Class] = sc
		}
		sc.Add(c.Severity, int(c.Findings))
	}
	for class, sc := range byClass {
		ov.ByClass[class] = fromDomain(*sc)
	}

	groups, err := f.q.FindingsPackageGroups(ctx, ids)
	if err != nil {
		return nil, stage("cannot group packages", err)
	}
	ov.TopPackages = topPackageJSON(groups)

	diff, err := f.q.FindingsDiffCounts(ctx, ids)
	if err != nil {
		return nil, stage("cannot count changes", err)
	}
	ov.NewSincePrevious, ov.FixedSincePrevious = int(diff.NewCount), int(diff.FixedCount)
	return ov, nil
}

func topPackageJSON(rows []store.FindingsPackageGroupsRow) []packageImpactJSON {
	groups := make([]domain.PackageGroup, len(rows))
	for i, g := range rows {
		groups[i] = domain.PackageGroup{
			PkgName: g.PkgName, InstalledVersion: g.InstalledVersion, Severity: g.Severity,
			Count: int(g.Findings), Fixable: int(g.Fixable), FixedVersions: g.FixedVersions,
		}
	}
	top := domain.TopPackages(groups, topPackages)
	out := make([]packageImpactJSON, len(top))
	for i, p := range top {
		out[i] = packageImpactJSON{
			PkgName: p.PkgName, InstalledVersion: p.InstalledVersion, FixVersion: p.FixVersion,
			Findings: p.Findings, BySeverity: fromDomain(p.BySeverity),
			Fixable: p.Fixable, FixableBySeverity: fromDomain(p.FixableBySeverity),
			Risk: p.Risk, RiskCleared: p.RiskCleared,
		}
	}
	return out
}
