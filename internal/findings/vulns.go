// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/httpapi"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const (
	defaultVulnLimit = 100
	maxVulnLimit     = 500
	maxQueryLen      = 200
	minSearchIDLen   = 3
	maxSearchIDLen   = 100
	// maxSearchResults bounds the findings one search lists, the most severe
	// first; truncated and the totals tell the caller that more exist.
	maxSearchResults = 1000
)

var validSeverities = map[string]bool{"CRITICAL": true, "HIGH": true, "MEDIUM": true, "LOW": true, "UNKNOWN": true}

type vulnerabilityJSON struct {
	ID               int64     `json:"id"`
	ScanID           int64     `json:"scan_id"`
	ArtifactName     string    `json:"artifact_name"`
	Target           string    `json:"target"`
	Class            string    `json:"class"`
	PkgType          string    `json:"pkg_type"`
	PkgName          string    `json:"pkg_name"`
	PkgPath          string    `json:"pkg_path"`
	InstalledVersion string    `json:"installed_version"`
	FixedVersion     string    `json:"fixed_version"`
	VulnID           string    `json:"vuln_id"`
	Severity         string    `json:"severity"`
	Status           string    `json:"status"`
	Title            string    `json:"title"`
	PrimaryURL       string    `json:"primary_url"`
	Suppressed       bool      `json:"suppressed"`
	Suppression      string    `json:"suppression"`
	FirstSeenAt      time.Time `json:"first_seen_at"`
}

func newVulnerability(v store.FindingsVulnerabilityPageRow, firstSeen map[int64]time.Time) vulnerabilityJSON {
	return vulnerabilityJSON{
		ID: v.ID, ScanID: v.ScanID, ArtifactName: v.ArtifactName, Target: v.Target, Class: v.Class,
		PkgType: v.PkgType, PkgName: v.PkgName, PkgPath: v.PkgPath, InstalledVersion: v.InstalledVersion,
		FixedVersion: v.FixedVersion, VulnID: v.VulnID, Severity: v.Severity, Status: v.Status,
		Title: v.Title, PrimaryURL: v.PrimaryUrl, Suppressed: v.Suppressed, Suppression: v.Suppression,
		FirstSeenAt: firstSeen[v.ID],
	}
}

func timestamptz(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// firstSeen maps vulnerability row ids to the scan time of the earliest scan
// that contains the same finding, in one query.
func (f *Findings) firstSeen(ctx context.Context, ids []int64) (map[int64]time.Time, error) {
	out := make(map[int64]time.Time, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := f.q.FindingsFirstSeen(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = r.FirstSeenAt.Time
	}
	return out, nil
}

type vulnerabilityPageJSON struct {
	Items     []vulnerabilityJSON `json:"items"`
	NextAfter *int64              `json:"next_after"`
}

func (f *Findings) listVulnerabilities(w http.ResponseWriter, r *http.Request) {
	svc, ok := f.service(w, r)
	if !ok {
		return
	}
	requested, ok := refParam(w, r)
	if !ok {
		return
	}
	params := store.FindingsVulnerabilityPageParams{Q: r.URL.Query().Get("q")}
	if utf8.RuneCountInString(params.Q) > maxQueryLen {
		invalid(w, "The q parameter is too long.")
		return
	}
	var err error
	if params.Severities, ok = severityParam(r.URL.Query().Get("severity")); !ok {
		invalid(w, invalidSeverityMsg)
		return
	}
	fixable, hasFixable, err := boolParam(r, "fixable")
	if err != nil {
		invalid(w, err.Error())
		return
	}
	params.Fixable = pgtype.Bool{Bool: fixable, Valid: hasFixable}
	if params.IncludeSuppressed, _, err = boolParam(r, "suppressed"); err != nil {
		invalid(w, err.Error())
		return
	}
	if params.After, err = intParam(r, "after", 0, 0, 1<<62); err != nil {
		invalid(w, err.Error())
		return
	}
	limit, err := intParam(r, "limit", defaultVulnLimit, 1, maxVulnLimit)
	if err != nil {
		invalid(w, err.Error())
		return
	}
	params.MaxRows = int32(limit) + 1 //nolint:gosec // limit <= 500

	ctx := r.Context()
	ref, _, err := f.resolveRef(ctx, svc, requested)
	if err != nil {
		f.internalError(w, r, "cannot pick a ref", err)
		return
	}
	page := vulnerabilityPageJSON{Items: []vulnerabilityJSON{}}
	_, ids, err := f.currentScans(ctx, svc.ID, ref)
	if err != nil {
		f.internalError(w, r, "cannot load current scans", err)
		return
	}
	if len(ids) == 0 {
		httpapi.WriteJSON(w, http.StatusOK, page)
		return
	}
	params.ScanIds = ids
	rows, err := f.q.FindingsVulnerabilityPage(ctx, params)
	if err != nil {
		f.internalError(w, r, "cannot list vulnerabilities", err)
		return
	}
	more := len(rows) > int(limit)
	if more {
		rows = rows[:limit]
	}
	rowIDs := make([]int64, len(rows))
	for i, row := range rows {
		rowIDs[i] = row.ID
	}
	seen, err := f.firstSeen(ctx, rowIDs)
	if err != nil {
		f.internalError(w, r, "cannot find first seen times", err)
		return
	}
	for _, row := range rows {
		page.Items = append(page.Items, newVulnerability(row, seen))
	}
	if more {
		page.NextAfter = &rows[len(rows)-1].ID
	}
	httpapi.WriteJSON(w, http.StatusOK, page)
}

// severityParam parses a comma-separated severity list; empty means all.
func severityParam(v string) ([]string, bool) {
	if v == "" {
		return []string{}, true
	}
	parts := strings.Split(v, ",")
	for i, p := range parts {
		p = strings.ToUpper(strings.TrimSpace(p))
		if !validSeverities[p] {
			return nil, false
		}
		parts[i] = p
	}
	return parts, true
}

type secretFindingJSON struct {
	ID           int64  `json:"id"`
	ScanID       int64  `json:"scan_id"`
	ArtifactName string `json:"artifact_name"`
	Target       string `json:"target"`
	RuleID       string `json:"rule_id"`
	Category     string `json:"category"`
	Severity     string `json:"severity"`
	Title        string `json:"title"`
	StartLine    int    `json:"start_line"`
	EndLine      int    `json:"end_line"`
	Suppressed   bool   `json:"suppressed"`
}

func (f *Findings) listSecrets(w http.ResponseWriter, r *http.Request) {
	svc, ok := f.service(w, r)
	if !ok {
		return
	}
	requested, ok := refParam(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	ref, _, err := f.resolveRef(ctx, svc, requested)
	if err != nil {
		f.internalError(w, r, "cannot pick a ref", err)
		return
	}
	out := struct {
		Items []secretFindingJSON `json:"items"`
	}{Items: []secretFindingJSON{}}
	_, ids, err := f.currentScans(ctx, svc.ID, ref)
	if err != nil {
		f.internalError(w, r, "cannot load current scans", err)
		return
	}
	if len(ids) > 0 {
		rows, err := f.q.FindingsSecrets(ctx, ids)
		if err != nil {
			f.internalError(w, r, "cannot list secrets", err)
			return
		}
		for _, s := range rows {
			out.Items = append(out.Items, secretFindingJSON{
				ID: s.ID, ScanID: s.ScanID, ArtifactName: s.ArtifactName, Target: s.Target,
				RuleID: s.RuleID, Category: s.Category, Severity: s.Severity, Title: s.Title,
				StartLine: int(s.StartLine), EndLine: int(s.EndLine), Suppressed: s.Suppressed,
			})
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

type searchItemJSON struct {
	Service          serviceJSON `json:"service"`
	Ref              string      `json:"ref"`
	ArtifactName     string      `json:"artifact_name"`
	ScanID           int64       `json:"scan_id"`
	ScannedAt        time.Time   `json:"scanned_at"`
	PkgName          string      `json:"pkg_name"`
	InstalledVersion string      `json:"installed_version"`
	FixedVersion     string      `json:"fixed_version"`
	Severity         string      `json:"severity"`
}

// Search scopes, the values of the scope parameter.
const (
	scopeDashboard = "dashboard" // the ref each service's dashboards show, plus protected refs
	scopeAll       = "all"       // every ref
)

// search lists where a vulnerability ID is present in the current state of
// the non-archived services: by default of the ref each service's dashboards
// show and of every protected ref, with scope=all of every ref. Suppressed
// findings are not listed: the response has no way to flag them.
func (f *Findings) search(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if !validSearchID(id) {
		invalid(w, "The id parameter must be 3 to 100 characters: letters, digits, '-', '_', ':' and '.'.")
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = scopeDashboard
	}
	if scope != scopeDashboard && scope != scopeAll {
		invalid(w, "The scope parameter must be dashboard or all.")
		return
	}
	rows, err := f.q.FindingsSearch(r.Context(), store.FindingsSearchParams{
		VulnID: id, AllRefs: scope == scopeAll, MaxRows: maxSearchResults,
	})
	if err != nil {
		f.internalError(w, r, "cannot search vulnerabilities", err)
		return
	}
	out := searchJSON{VulnID: id, Scope: scope, Items: make([]searchItemJSON, 0, len(rows)), FixedVersions: []string{}}
	if len(rows) > 0 {
		t := rows[0]
		out.TotalFindings, out.TotalServices, out.TotalArtifacts = t.TotalFindings, t.TotalServices, t.TotalArtifacts
		out.Truncated = out.TotalFindings > int64(len(rows))
		out.Counts = countsJSON{
			Critical: int(t.TotalCritical), High: int(t.TotalHigh), Medium: int(t.TotalMedium),
			Low: int(t.TotalLow), Unknown: int(t.TotalUnknown),
		}
		out.Title, out.PrimaryURL = t.SummaryTitle, t.SummaryUrl
		out.FixedVersions = fixedVersions(t.FixedVersions)
	}
	for _, s := range rows {
		out.Items = append(out.Items, searchItemJSON{
			Service: newServiceJSON(s.ID, s.Name, s.GitlabProjectID, s.GitlabProjectPath,
				s.SonarProjectKey, s.CreatedVia, s.Archived, s.CreatedAt, s.DefaultRef),
			Ref: s.Ref, ArtifactName: s.ArtifactName, ScanID: s.ScanID, ScannedAt: s.ScannedAt.Time,
			PkgName: s.PkgName, InstalledVersion: s.InstalledVersion, FixedVersion: s.FixedVersion,
			Severity: s.Severity,
		})
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

type searchJSON struct {
	VulnID         string           `json:"vuln_id"`
	Scope          string           `json:"scope"`
	Items          []searchItemJSON `json:"items"`
	TotalFindings  int64            `json:"total_findings"`
	TotalServices  int64            `json:"total_services"`
	TotalArtifacts int64            `json:"total_artifacts"`
	Truncated      bool             `json:"truncated"`
	// A summary of the vulnerability over every match, listed or not.
	Counts        countsJSON `json:"counts"`
	Title         string     `json:"title"`
	PrimaryURL    string     `json:"primary_url"`
	FixedVersions []string   `json:"fixed_versions"`
}

// maxFixedVersions bounds the fixed versions a search summary lists.
const maxFixedVersions = 20

// fixedVersions splits the reported fixed versions (each may be a
// comma-separated list), drops duplicates and keeps at most
// maxFixedVersions, in the order the database sorted them.
func fixedVersions(reported []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, list := range reported {
		for _, v := range strings.Split(list, ",") {
			v = strings.TrimSpace(v)
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			if len(out) == maxFixedVersions {
				return out
			}
			out = append(out, v)
		}
	}
	return out
}

func validSearchID(id string) bool {
	if len(id) < minSearchIDLen || len(id) > maxSearchIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == ':', c == '.':
		default:
			return false
		}
	}
	return true
}
