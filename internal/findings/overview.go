// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"net/http"
	"time"

	"github.com/siyamsarker/scanopsis/internal/domain"
	"github.com/siyamsarker/scanopsis/internal/httpapi"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const topPackages = 10

type packageImpactJSON struct {
	PkgName          string     `json:"pkg_name"`
	InstalledVersion string     `json:"installed_version"`
	FixVersion       string     `json:"fix_version"`
	Findings         int        `json:"findings"`
	BySeverity       countsJSON `json:"by_severity"`
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

func (f *Findings) overview(w http.ResponseWriter, r *http.Request) {
	svc, ok := f.service(w, r)
	if !ok {
		return
	}
	requested, ok := refParam(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	refs, err := f.refs(ctx, svc.ID)
	if err != nil {
		f.internalError(w, r, "cannot list refs", err)
		return
	}
	ref, source := pickRef(requested, svc.DefaultRef, refs)
	ov := overviewJSON{
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
		f.internalError(w, r, "cannot load current scans", err)
		return
	}
	if ref != "" {
		ov.Ref = &ref
	}
	if len(rows) == 0 {
		httpapi.WriteJSON(w, http.StatusOK, ov)
		return
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
		f.internalError(w, r, "cannot count classes", err)
		return
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
		f.internalError(w, r, "cannot group packages", err)
		return
	}
	ov.TopPackages = topPackageJSON(groups)

	diff, err := f.q.FindingsDiffCounts(ctx, ids)
	if err != nil {
		f.internalError(w, r, "cannot count changes", err)
		return
	}
	ov.NewSincePrevious, ov.FixedSincePrevious = int(diff.NewCount), int(diff.FixedCount)
	httpapi.WriteJSON(w, http.StatusOK, ov)
}

func topPackageJSON(rows []store.FindingsPackageGroupsRow) []packageImpactJSON {
	groups := make([]domain.PackageGroup, len(rows))
	for i, g := range rows {
		groups[i] = domain.PackageGroup{
			PkgName: g.PkgName, InstalledVersion: g.InstalledVersion, Severity: g.Severity,
			Count: int(g.Findings), FixedVersions: g.FixedVersions,
		}
	}
	top := domain.TopPackages(groups, topPackages)
	out := make([]packageImpactJSON, len(top))
	for i, p := range top {
		out[i] = packageImpactJSON{
			PkgName: p.PkgName, InstalledVersion: p.InstalledVersion, FixVersion: p.FixVersion,
			Findings: p.Findings, BySeverity: fromDomain(p.BySeverity),
		}
	}
	return out
}
