// Copyright 2026 Siyam Sarker
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

// maxTrendScans is the most scans a trends response lists: the newest of the
// window, over every ref asked for. A timeline shows no more pipelines than
// this, and it bounds the findings compared for the new and fixed counts. A
// variable so tests can lower it.
var maxTrendScans = 300

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
	ScanID int64 `json:"scan_id"`
	// PreviousScanID is the scan compared with: the base asked for, else the
	// previous scan of the same ref; Base summarises it.
	PreviousScanID *int64              `json:"previous_scan_id"`
	Base           *scanSummaryJSON    `json:"base"`
	New            []vulnerabilityJSON `json:"new"`
	Fixed          []vulnerabilityJSON `json:"fixed"`
	UnchangedItems []vulnerabilityJSON `json:"unchanged_items"`
	Unchanged      int                 `json:"unchanged"`
	// NewTotal and FixedTotal count every new and fixed finding; New, Fixed
	// list at most maxDiffItems each and UnchangedItems maxUnchangedItems, the most severe
	// first, and Truncated is true when any of them was cut. The counts per
	// severity cover every finding, listed or not.
	NewTotal        int        `json:"new_total"`
	FixedTotal      int        `json:"fixed_total"`
	Truncated       bool       `json:"truncated"`
	NewCounts       countsJSON `json:"new_counts"`
	FixedCounts     countsJSON `json:"fixed_counts"`
	UnchangedCounts countsJSON `json:"unchanged_counts"`
}

// severityCounts counts diff rows per severity.
func severityCounts(rows []store.FindingsDiffRowsRow) countsJSON {
	var c countsJSON
	for _, v := range rows {
		switch v.Severity {
		case "CRITICAL":
			c.Critical++
		case "HIGH":
			c.High++
		case "MEDIUM":
			c.Medium++
		case "LOW":
			c.Low++
		default:
			c.Unknown++
		}
	}
	return c
}

// maxDiffItems bounds each of the new and fixed lists of a scan diff, and
// maxUnchangedItems the unchanged list, and so the number of findings whose
// first-seen time is looked up. The unchanged findings are the current state,
// which the findings explorer lists in full, so a short list does.
const (
	maxDiffItems      = 500
	maxUnchangedItems = 100
)

const invalidBaseMsg = "The base parameter must be the id of an earlier scan of the same service, artifact and kind."

// scanDiff compares the unsuppressed findings of a scan with those of the
// previous scan of the same service, ref, artifact and kind, or with the
// scan named by the base parameter: any earlier scan of the same service,
// artifact and kind, on any ref.
func (f *Findings) scanDiff(w http.ResponseWriter, r *http.Request) {
	baseID, err := intParam(r, "base", 0, 1, 1<<62)
	if err != nil {
		invalid(w, invalidBaseMsg)
		return
	}
	scan, ok := f.loadScan(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	out := scanDiffJSON{ScanID: scan.ID, New: []vulnerabilityJSON{}, Fixed: []vulnerabilityJSON{}}
	ids := []int64{scan.ID}
	var prev int64
	if baseID != 0 {
		prev, err = f.q.FindingsDiffBase(ctx, store.FindingsDiffBaseParams{ScanID: scan.ID, BaseID: baseID})
		if errors.Is(err, pgx.ErrNoRows) {
			invalid(w, invalidBaseMsg)
			return
		}
	} else {
		prev, err = f.q.FindingsPreviousScan(ctx, scan.ID)
	}
	switch {
	case err == nil:
		base, err := f.q.FindingsGetScan(ctx, prev)
		if err != nil {
			f.internalError(w, r, "cannot load base scan", err)
			return
		}
		summary := newScanSummary(scanRow(base))
		out.PreviousScanID, out.Base = &prev, &summary
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
	key := func(v store.FindingsDiffRowsRow) domain.Key {
		return domain.Key{VulnID: v.VulnID, PkgName: v.PkgName, Target: v.Target}
	}
	added, removed, unchanged := domain.Diff(prevRows, curRows, key)
	// The unchanged findings are the current ones, once per key, that are not new.
	isNew := make(map[domain.Key]bool, len(added))
	for _, v := range added {
		isNew[key(v)] = true
	}
	kept := make([]store.FindingsDiffRowsRow, 0, unchanged)
	listed := make(map[domain.Key]bool, unchanged)
	for _, v := range curRows {
		if k := key(v); !isNew[k] && !listed[k] {
			listed[k] = true
			kept = append(kept, v)
		}
	}
	out.Unchanged = unchanged
	out.NewTotal, out.FixedTotal = len(added), len(removed)
	out.NewCounts, out.FixedCounts, out.UnchangedCounts = severityCounts(added), severityCounts(removed), severityCounts(kept)
	if len(added) > maxDiffItems {
		added, out.Truncated = added[:maxDiffItems], true
	}
	if len(removed) > maxDiffItems {
		removed, out.Truncated = removed[:maxDiffItems], true
	}
	if len(kept) > maxUnchangedItems {
		kept, out.Truncated = kept[:maxUnchangedItems], true
	}

	rowIDs := make([]int64, 0, len(added)+len(removed)+len(kept))
	for _, list := range [][]store.FindingsDiffRowsRow{added, removed, kept} {
		for _, v := range list {
			rowIDs = append(rowIDs, v.ID)
		}
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
	out.UnchangedItems = make([]vulnerabilityJSON, 0, len(kept))
	for _, v := range kept {
		out.UnchangedItems = append(out.UnchangedItems, newVulnerability(store.FindingsVulnerabilityPageRow(v), seen))
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

// listEarlierScans lists the scans before a scan of the same service,
// artifact and kind, newest first: the scans it can be compared with. They
// come from every ref unless the ref parameter names one.
func (f *Findings) listEarlierScans(w http.ResponseWriter, r *http.Request) {
	ref, ok := refParam(w, r)
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
	scan, ok := f.loadScan(w, r)
	if !ok {
		return
	}
	rows, err := f.q.FindingsEarlierScans(r.Context(), store.FindingsEarlierScansParams{
		ScanID: scan.ID, Ref: ref, Before: before, MaxRows: int32(limit) + 1, //nolint:gosec // limit <= 200
	})
	if err != nil {
		f.internalError(w, r, "cannot list earlier scans", err)
		return
	}
	page := scanPageJSON{Items: make([]scanSummaryJSON, 0, len(rows))}
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
	httpapi.WriteJSON(w, http.StatusOK, page)
}

type trendPointJSON struct {
	ScanID           int64      `json:"scan_id"`
	Kind             string     `json:"kind"`
	Ref              string     `json:"ref"`
	ScannedAt        time.Time  `json:"scanned_at"`
	GitlabPipelineID int64      `json:"gitlab_pipeline_id"`
	CommitSha        string     `json:"commit_sha"`
	ArtifactKey      string     `json:"artifact_key"`
	ArtifactName     string     `json:"artifact_name"`
	Counts           countsJSON `json:"counts"`
	Fixable          int        `json:"fixable"`
	PreviousScanID   *int64     `json:"previous_scan_id"`
	New              countsJSON `json:"new"`
	Fixed            int        `json:"fixed"`
}

type trendsJSON struct {
	Ref       *string          `json:"ref"`
	RefSource string           `json:"ref_source"`
	Points    []trendPointJSON `json:"points"`
	// Total counts the scans in the window; Truncated is true when the
	// oldest of them were left out of Points.
	Total     int  `json:"total"`
	Truncated bool `json:"truncated"`
}

// trends returns one point per scan within the window, oldest first: of the
// dashboard ref (or the requested one), or of every ref with all_refs=true.
// Each point says what the scan changed against the previous scan of its
// ref, artifact and kind.
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
	allRefs, _, err := boolParam(r, "all_refs")
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
	if ref != "" {
		out.Ref = &ref
	}
	if ref == "" && !allRefs {
		httpapi.WriteJSON(w, http.StatusOK, out)
		return
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	rows, err := f.q.FindingsTrends(r.Context(), store.FindingsTrendsParams{
		ServiceID: svc.ID, Ref: ref, AllRefs: allRefs, Since: timestamptz(since),
		MaxScans: int32(maxTrendScans), //nolint:gosec // a small constant
	})
	if err != nil {
		f.internalError(w, r, "cannot load trends", err)
		return
	}
	for _, row := range rows {
		p := trendPointJSON{
			ScanID: row.ID, Kind: row.Kind, Ref: row.Ref, ScannedAt: row.ScannedAt.Time,
			GitlabPipelineID: row.GitlabPipelineID, CommitSha: row.CommitSha,
			ArtifactKey: row.ArtifactKey, ArtifactName: row.ArtifactName,
			Counts: countsJSON{
				Critical: int(row.CountCritical), High: int(row.CountHigh), Medium: int(row.CountMedium),
				Low: int(row.CountLow), Unknown: int(row.CountUnknown),
			},
			Fixable: int(row.CountFixable),
			New: countsJSON{
				Critical: int(row.NewCritical), High: int(row.NewHigh), Medium: int(row.NewMedium),
				Low: int(row.NewLow), Unknown: int(row.NewUnknown),
			},
			Fixed: int(row.Fixed),
		}
		if row.PreviousScanID.Valid {
			id := row.PreviousScanID.Int64
			p.PreviousScanID = &id
		}
		out.Points = append(out.Points, p)
		out.Total = int(row.WindowTotal)
	}
	out.Truncated = out.Total > len(out.Points)
	httpapi.WriteJSON(w, http.StatusOK, out)
}
