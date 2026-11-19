// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/siyamsarker/scanopsis/internal/domain"
	"github.com/siyamsarker/scanopsis/internal/httpapi"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const (
	defaultScanLimit = 50
	maxScanLimit     = 200
	defaultTrendDays = 90
	maxTrendDays     = 365
)

type scanPageJSON struct {
	Items      []scanSummaryJSON `json:"items"`
	NextBefore *int64            `json:"next_before"`
}

// listScans lists the scans of one ref, newest first. Without a ref it uses
// the ref the dashboards show: the chosen dashboard branch, else the ref with
// the most recent scan.
func (f *Findings) listScans(w http.ResponseWriter, r *http.Request) {
	svc, ok := f.service(w, r)
	if !ok {
		return
	}
	requested, ok := refParam(w, r)
	if !ok {
		return
	}
	before, err := intParam(r, "before", 0, 0, 1<<62)
	if err != nil {
		invalid(w, err.Error())
		return
	}
	limit, err := intParam(r, "limit", defaultScanLimit, 1, maxScanLimit)
	if err != nil {
		invalid(w, err.Error())
		return
	}
	ref, _, err := f.resolveRef(r.Context(), svc, requested)
	if err != nil {
		f.internalError(w, r, "cannot pick a ref", err)
		return
	}
	page := scanPageJSON{Items: []scanSummaryJSON{}}
	if ref != "" {
		rows, err := f.q.FindingsScansPage(r.Context(), store.FindingsScansPageParams{
			ServiceID: svc.ID, Ref: ref, Before: before, MaxRows: int32(limit) + 1, //nolint:gosec // limit <= 200
		})
		if err != nil {
			f.internalError(w, r, "cannot list scans", err)
			return
		}
		more := len(rows) > int(limit)
		if more {
			rows = rows[:limit]
		}
		for _, row := range rows {
			page.Items = append(page.Items, newScanSummary(scanRow(row)))
		}
		if more {
			page.NextBefore = &rows[len(rows)-1].ID
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, page)
}

type scanJSON struct {
	scanSummaryJSON
	GitlabProjectPath string  `json:"gitlab_project_path"`
	PipelineURL       *string `json:"pipeline_url"`
	JobURL            *string `json:"job_url"`
	CommitURL         *string `json:"commit_url"`
}

// loadScan reads the scan named by the path, writing the response and
// returning false when it is unknown or the lookup fails.
func (f *Findings) loadScan(w http.ResponseWriter, r *http.Request) (scanRow, bool) {
	id, ok := pathID(r)
	if !ok {
		notFound(w, "scan")
		return scanRow{}, false
	}
	row, err := f.q.FindingsGetScan(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		notFound(w, "scan")
		return scanRow{}, false
	}
	if err != nil {
		f.internalError(w, r, "cannot load scan", err)
		return scanRow{}, false
	}
	return scanRow(row), true
}

func (f *Findings) getScan(w http.ResponseWriter, r *http.Request) {
	scan, ok := f.loadScan(w, r)
	if !ok {
		return
	}
	svc, err := f.q.FindingsGetService(r.Context(), scan.ServiceID)
	if err != nil {
		f.internalError(w, r, "cannot load service of scan", err)
		return
	}
	base := f.cfg.Current().GitLabURL
	path := svc.GitlabProjectPath
	httpapi.WriteJSON(w, http.StatusOK, scanJSON{
		scanSummaryJSON:   newScanSummary(scan),
		GitlabProjectPath: path,
		PipelineURL:       gitlabLink(base, path, "pipelines", strconv.FormatInt(scan.GitlabPipelineID, 10)),
		JobURL:            gitlabLink(base, path, "jobs", strconv.FormatInt(scan.GitlabJobID, 10)),
		CommitURL:         gitlabLink(base, path, "commit", commitID(scan.CommitSha)),
	})
}

type scanDiffJSON struct {
	ScanID         int64               `json:"scan_id"`
	PreviousScanID *int64              `json:"previous_scan_id"`
	New            []vulnerabilityJSON `json:"new"`
	Fixed          []vulnerabilityJSON `json:"fixed"`
	Unchanged      int                 `json:"unchanged"`
}

// scanDiff compares the unsuppressed findings of a scan with those of the
// previous scan of the same service, ref, artifact and kind.
func (f *Findings) scanDiff(w http.ResponseWriter, r *http.Request) {
	scan, ok := f.loadScan(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	out := scanDiffJSON{ScanID: scan.ID, New: []vulnerabilityJSON{}, Fixed: []vulnerabilityJSON{}}
	ids := []int64{scan.ID}
	prev, err := f.q.FindingsPreviousScan(ctx, scan.ID)
	switch {
	case err == nil:
		out.PreviousScanID = &prev
		ids = append(ids, prev)
	case !errors.Is(err, pgx.ErrNoRows):
		f.internalError(w, r, "cannot find previous scan", err)
		return
	}
	rows, err := f.q.FindingsDiffRows(ctx, ids)
	if err != nil {
		f.internalError(w, r, "cannot load findings", err)
		return
	}
	var prevRows, curRows []store.FindingsDiffRowsRow
	for _, row := range rows {
		if row.ScanID == scan.ID {
			curRows = append(curRows, row)
		} else {
			prevRows = append(prevRows, row)
		}
	}
	added, removed, unchanged := domain.Diff(prevRows, curRows, func(v store.FindingsDiffRowsRow) domain.Key {
		return domain.Key{VulnID: v.VulnID, PkgName: v.PkgName, Target: v.Target}
	})
	out.Unchanged = unchanged

	rowIDs := make([]int64, 0, len(added)+len(removed))
	for _, v := range added {
		rowIDs = append(rowIDs, v.ID)
	}
	for _, v := range removed {
		rowIDs = append(rowIDs, v.ID)
	}
	seen, err := f.firstSeen(ctx, rowIDs)
	if err != nil {
		f.internalError(w, r, "cannot find first seen times", err)
		return
	}
	for _, v := range added {
		out.New = append(out.New, newVulnerability(store.FindingsVulnerabilityPageRow(v), seen))
	}
	for _, v := range removed {
		out.Fixed = append(out.Fixed, newVulnerability(store.FindingsVulnerabilityPageRow(v), seen))
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

type trendPointJSON struct {
	ScanID           int64      `json:"scan_id"`
	Kind             string     `json:"kind"`
	ScannedAt        time.Time  `json:"scanned_at"`
	GitlabPipelineID int64      `json:"gitlab_pipeline_id"`
	CommitSha        string     `json:"commit_sha"`
	ArtifactName     string     `json:"artifact_name"`
	Counts           countsJSON `json:"counts"`
	Fixable          int        `json:"fixable"`
}

type trendsJSON struct {
	Ref       *string          `json:"ref"`
	RefSource string           `json:"ref_source"`
	Points    []trendPointJSON `json:"points"`
}

// trends returns one point per scan of a ref within the window, oldest first.
func (f *Findings) trends(w http.ResponseWriter, r *http.Request) {
	svc, ok := f.service(w, r)
	if !ok {
		return
	}
	requested, ok := refParam(w, r)
	if !ok {
		return
	}
	days, err := intParam(r, "days", defaultTrendDays, 1, maxTrendDays)
	if err != nil {
		invalid(w, err.Error())
		return
	}
	ref, source, err := f.resolveRef(r.Context(), svc, requested)
	if err != nil {
		f.internalError(w, r, "cannot pick a ref", err)
		return
	}
	out := trendsJSON{Points: []trendPointJSON{}, RefSource: source}
	if ref == "" {
		httpapi.WriteJSON(w, http.StatusOK, out)
		return
	}
	out.Ref = &ref
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	rows, err := f.q.FindingsTrends(r.Context(), store.FindingsTrendsParams{
		ServiceID: svc.ID, Ref: ref, Since: timestamptz(since),
	})
	if err != nil {
		f.internalError(w, r, "cannot load trends", err)
		return
	}
	for _, row := range rows {
		out.Points = append(out.Points, trendPointJSON{
			ScanID: row.ID, Kind: row.Kind, ScannedAt: row.ScannedAt.Time, GitlabPipelineID: row.GitlabPipelineID,
			CommitSha: row.CommitSha, ArtifactName: row.ArtifactName,
			Counts: countsJSON{
				Critical: int(row.CountCritical), High: int(row.CountHigh), Medium: int(row.CountMedium),
				Low: int(row.CountLow), Unknown: int(row.CountUnknown),
			},
			Fixable: int(row.CountFixable),
		})
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}
