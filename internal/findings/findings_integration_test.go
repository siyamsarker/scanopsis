// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package findings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

// fixture is the data every test reads. All times are relative to base.
//
// Service alpha, ref main:
//
//	web (image):    s1 -10d  CVE-1 openssl HIGH, CVE-2 zlib CRITICAL
//	                s2  -5d  CVE-1, CVE-2, CVE-3 libx HIGH
//	                s3  -1d  CVE-1, CVE-3, CVE-4 curl LOW (twice, same key), CVE-9 busybox (suppressed)
//	worker (image): w0  -4d  CVE-1 openssl
//	                w1  -2d  CVE-1, CVE-5 left-pad MEDIUM, CVE-6 openssl CRITICAL, CVE-8 busybox UNKNOWN
//	repo (secret-fs): r0 -3d one CRITICAL secret
//	                r1 -12h  HIGH, MEDIUM and one suppressed LOW secret
//
// Service alpha, ref feature: f1 -20d web CVE-7 CRITICAL.
// Service beta, ref main: b1 -1d web CVE-1 openssl CRITICAL.
// Service gamma (archived), ref main: g1 CVE-1.
// Service empty: no scans.
type fixture struct {
	pool                           *pgxpool.Pool
	base                           time.Time
	alpha, beta, gamma, empty      int64
	s1, s2, s3, w0, w1, r0, r1, f1 int64
	b1                             int64
	s3Rows, w1Rows                 []int64
}

func at(base time.Time, d time.Duration) time.Time { return base.Add(-d) }

const day = 24 * time.Hour

func seed(t *testing.T) *fixture {
	t.Helper()
	pool := storetest.New(t)
	base := time.Now().UTC().Truncate(time.Second)
	f := &fixture{pool: pool, base: base}
	f.alpha = newService(t, pool, "alpha", "grp/alpha", false)
	f.beta = newService(t, pool, "beta", "grp/beta", false)
	f.gamma = newService(t, pool, "gamma", "grp/gamma", true)
	f.empty = newService(t, pool, "empty", "grp/empty", false)

	const os, lang = "os-pkgs", "lang-pkgs"
	const deb, lock = "debian 12", "app/package-lock.json"
	openssl := vuln{id: "CVE-1", pkg: "openssl", version: "3.0.1", target: deb, class: os, severity: "HIGH", fixed: "3.0.9", title: "openssl flaw"}
	zlib := vuln{id: "CVE-2", pkg: "zlib", version: "1.2.11", target: deb, class: os, severity: "CRITICAL", title: "zlib overflow"}
	libx := vuln{id: "CVE-3", pkg: "libx", version: "1.0.0", target: lock, class: lang, severity: "HIGH", fixed: "1.0.5, 1.1.2", title: "libx bug"}
	curl := vuln{id: "CVE-4", pkg: "curl", version: "7.8", target: deb, class: os, severity: "LOW", fixed: "7.9", title: "curl bug", path: "/usr/bin/curl"}
	curl2 := curl
	curl2.path = "/usr/lib/libcurl.so"
	busyboxSuppressed := vuln{id: "CVE-9", pkg: "busybox", version: "1.35", target: deb, class: os, severity: "MEDIUM", title: "busybox bug", suppressed: true}
	leftpad := vuln{id: "CVE-5", pkg: "left-pad", version: "1.0.0", target: "app/yarn.lock", class: lang, severity: "MEDIUM", fixed: "1.3.0", title: "Prototype pollution"}
	critSSL := vuln{id: "CVE-6", pkg: "openssl", version: "3.0.1", target: deb, class: os, severity: "CRITICAL", fixed: "3.0.12", title: "openssl crit"}
	busybox := vuln{id: "CVE-8", pkg: "busybox", version: "1.35", target: deb, class: os, severity: "UNKNOWN", title: "busybox unknown"}

	f.s1 = newScan(t, pool, f.alpha, "image", "web", "main", at(base, 10*day))
	addVulns(t, pool, f.s1, openssl, zlib)
	f.s2 = newScan(t, pool, f.alpha, "image", "web", "main", at(base, 5*day))
	addVulns(t, pool, f.s2, openssl, zlib, libx)
	f.s3 = newScan(t, pool, f.alpha, "image", "web", "main", at(base, 1*day))
	f.s3Rows = addVulns(t, pool, f.s3, openssl, libx, curl, curl2, busyboxSuppressed)
	f.w0 = newScan(t, pool, f.alpha, "image", "worker", "main", at(base, 4*day))
	addVulns(t, pool, f.w0, openssl)
	f.w1 = newScan(t, pool, f.alpha, "image", "worker", "main", at(base, 2*day))
	f.w1Rows = addVulns(t, pool, f.w1, openssl, leftpad, critSSL, busybox)
	f.r0 = newScan(t, pool, f.alpha, "secret-fs", "repo", "main", at(base, 3*day))
	addSecrets(t, pool, f.r0, secret{"old-key", ".env", "CRITICAL", "Old", 1, 1, false})
	f.r1 = newScan(t, pool, f.alpha, "secret-fs", "repo", "main", at(base, 12*time.Hour))
	addSecrets(t, pool, f.r1,
		secret{"generic-secret", "config.yml", "LOW", "Generic", 9, 9, true},
		secret{"github-pat", ".env", "HIGH", "GitHub PAT", 3, 3, false},
		secret{"slack-token", "ci/vars.sh", "MEDIUM", "Slack token", 12, 14, false})
	f.f1 = newScan(t, pool, f.alpha, "image", "web", "feature", at(base, 20*day))
	addVulns(t, pool, f.f1, vuln{id: "CVE-7", pkg: "x", version: "1", target: deb, class: os, severity: "CRITICAL", title: "feature only"})

	crit := openssl
	crit.severity = "CRITICAL"
	f.b1 = newScan(t, pool, f.beta, "image", "web", "main", at(base, 1*day))
	addVulns(t, pool, f.b1, crit)
	g1 := newScan(t, pool, f.gamma, "image", "web", "main", at(base, 1*day))
	addVulns(t, pool, g1, openssl)
	return f
}

func unix(t time.Time) int64 { return t.Unix() }

func TestHandlersCoverEveryOperation(t *testing.T) {
	h := New(nil, cfgStub{}, discardLog()).Handlers()
	var got []string
	for k, v := range h {
		if v == nil {
			t.Errorf("nil handler for %s", k)
		}
		got = append(got, k)
	}
	sort.Strings(got)
	eq(t, "operations", got, []string{
		"getScan", "getScanDiff", "getServiceOverview", "getServiceTrends", "listEarlierScans", "listServiceScans",
		"listServiceSecrets", "listServiceVulnerabilities", "searchVulnerabilities",
	})
}

func TestOverview(t *testing.T) {
	f := seed(t)
	mux := newMux(f.pool, "")
	var ov overviewJSON
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/overview", f.alpha), &ov)

	if ov.Service.ID != f.alpha || ov.Service.Name != "alpha" || ov.Service.GitlabProjectPath != "grp/alpha" ||
		ov.Service.GitlabProjectID != nil || ov.Service.SonarProjectKey != nil || ov.Service.Archived {
		t.Errorf("service = %+v", ov.Service)
	}
	if ov.Ref == nil || *ov.Ref != "main" {
		t.Fatalf("ref = %v, want main (most recent scan)", ov.Ref)
	}
	eq(t, "refs", ov.Refs, []string{"main", "feature"})
	var arts []string
	for _, a := range ov.Artifacts {
		arts = append(arts, fmt.Sprintf("%s/%s#%d", a.ArtifactName, a.Kind, a.ID))
	}
	eq(t, "artifacts", arts, []string{
		fmt.Sprintf("repo/secret-fs#%d", f.r1), fmt.Sprintf("web/image#%d", f.s3), fmt.Sprintf("worker/image#%d", f.w1),
	})
	eq(t, "counts", ov.Counts, countsJSON{Critical: 1, High: 3, Medium: 1, Low: 2, Unknown: 1})
	eq(t, "fixable/secrets/suppressed", []int{ov.Fixable, ov.Secrets, ov.Suppressed}, []int{7, 2, 2})
	eq(t, "new/fixed", []int{ov.NewSincePrevious, ov.FixedSincePrevious}, []int{4, 1})
	eq(t, "by_class", ov.ByClass, map[string]countsJSON{
		"os-pkgs":   {Critical: 1, High: 2, Low: 2, Unknown: 1},
		"lang-pkgs": {High: 1, Medium: 1},
	})
	// The upgrade that clears the most risk points comes first: curl has two
	// findings but they are low (2 points); busybox has no fix.
	eq(t, "top_packages", ov.TopPackages, []packageImpactJSON{
		{"openssl", "3.0.1", "3.0.12", 3, countsJSON{Critical: 1, High: 2}, 3, 20, 20},
		{"libx", "1.0.0", "1.1.2", 1, countsJSON{High: 1}, 1, 5, 5},
		{"left-pad", "1.0.0", "1.3.0", 1, countsJSON{Medium: 1}, 1, 3, 3},
		{"curl", "7.8", "7.9", 2, countsJSON{Low: 2}, 2, 2, 2},
		{"busybox", "1.35", "", 1, countsJSON{Unknown: 1}, 0, 5, 0},
	})
	if ov.DataAt == nil || unix(*ov.DataAt) != unix(at(f.base, 12*time.Hour)) {
		t.Errorf("data_at = %v, want %v", ov.DataAt, at(f.base, 12*time.Hour))
	}

	// Another ref: no previous scan, so its single finding is new.
	var feat overviewJSON
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/overview?ref=feature", f.alpha), &feat)
	if feat.Ref == nil || *feat.Ref != "feature" || len(feat.Artifacts) != 1 {
		t.Fatalf("feature overview = %+v", feat)
	}
	eq(t, "feature counts", feat.Counts, countsJSON{Critical: 1})
	eq(t, "feature new/fixed", []int{feat.NewSincePrevious, feat.FixedSincePrevious}, []int{1, 0})
	eq(t, "feature refs", feat.Refs, []string{"main", "feature"})

	// A ref without scans echoes the ref and reports nothing.
	var none overviewJSON
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/overview?ref=nope", f.alpha), &none)
	if none.Ref == nil || *none.Ref != "nope" || len(none.Artifacts) != 0 || none.DataAt != nil {
		t.Errorf("unknown ref overview = %+v", none)
	}
}

func TestOverviewEmptyService(t *testing.T) {
	f := seed(t)
	code, body := get(t, newMux(f.pool, ""), fmt.Sprintf("/api/v1/services/%d/overview", f.empty))
	if code != 200 {
		t.Fatalf("status %d %s", code, body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"ref": "null", "refs": "[]", "artifacts": "[]", "by_class": "{}", "top_packages": "[]", "data_at": "null",
		"counts":  `{"critical":0,"high":0,"medium":0,"low":0,"unknown":0}`,
		"fixable": "0", "secrets": "0", "suppressed": "0", "new_since_previous": "0", "fixed_since_previous": "0",
	} {
		if string(raw[k]) != want {
			t.Errorf("%s = %s, want %s", k, raw[k], want)
		}
	}
}

func TestOverviewTopPackagesLimit(t *testing.T) {
	f := seed(t)
	scan := newScan(t, f.pool, f.beta, "image", "big", "dev", f.base)
	var vs []vuln
	for i := range 14 {
		// Package pN has N+1 findings so the order is fully determined.
		for j := 0; j <= i; j++ {
			vs = append(vs, vuln{id: fmt.Sprintf("CVE-%d-%d", i, j), pkg: fmt.Sprintf("p%02d", i), version: "1",
				target: "t", class: "os-pkgs", severity: "LOW"})
		}
	}
	addVulns(t, f.pool, scan, vs...)
	var ov overviewJSON
	getJSON(t, newMux(f.pool, ""), fmt.Sprintf("/api/v1/services/%d/overview?ref=dev", f.beta), &ov)
	var names []string
	for _, p := range ov.TopPackages {
		names = append(names, fmt.Sprintf("%s:%d", p.PkgName, p.Findings))
	}
	eq(t, "top packages", names, []string{"p13:14", "p12:13", "p11:12", "p10:11", "p09:10", "p08:9", "p07:8", "p06:7", "p05:6", "p04:5"})
}

func TestUnknownServiceAndBadIDs(t *testing.T) {
	f := seed(t)
	mux := newMux(f.pool, "")
	for _, p := range []string{
		"/api/v1/services/999999/overview", "/api/v1/services/999999/scans", "/api/v1/services/999999/vulnerabilities",
		"/api/v1/services/999999/secrets", "/api/v1/services/999999/trends",
		"/api/v1/services/abc/overview", "/api/v1/services/0/overview",
		"/api/v1/scans/999999", "/api/v1/scans/999999/diff", "/api/v1/scans/x", "/api/v1/scans/-1/diff",
	} {
		wantError(t, mux, p, 404, "not_found")
	}
}

func TestListScans(t *testing.T) {
	f := seed(t)
	mux := newMux(f.pool, "")
	base := fmt.Sprintf("/api/v1/services/%d/scans", f.alpha)
	ids := func(p scanPageJSON) []int64 {
		out := []int64{}
		for _, s := range p.Items {
			out = append(out, s.ID)
		}
		return out
	}

	// Newest first by scan time, not by id: w0 has a higher id than s3.
	var p1 scanPageJSON
	getJSON(t, mux, base+"?limit=3", &p1)
	eq(t, "page 1", ids(p1), []int64{f.r1, f.s3, f.w1})
	if p1.NextBefore == nil || *p1.NextBefore != f.w1 {
		t.Fatalf("next_before = %v, want %d", p1.NextBefore, f.w1)
	}
	var p2 scanPageJSON
	getJSON(t, mux, fmt.Sprintf("%s?limit=3&before=%d", base, f.w1), &p2)
	eq(t, "page 2", ids(p2), []int64{f.r0, f.w0, f.s2})
	if p2.NextBefore == nil || *p2.NextBefore != f.s2 {
		t.Fatalf("next_before = %v, want %d", p2.NextBefore, f.s2)
	}
	var p3 scanPageJSON
	getJSON(t, mux, fmt.Sprintf("%s?limit=3&before=%d", base, f.s2), &p3)
	eq(t, "page 3", ids(p3), []int64{f.s1})
	if p3.NextBefore != nil {
		t.Errorf("last page next_before = %d, want null", *p3.NextBefore)
	}

	// A page that ends exactly at the last scan has no next page.
	var all scanPageJSON
	getJSON(t, mux, base+"?limit=7", &all)
	eq(t, "all", ids(all), []int64{f.r1, f.s3, f.w1, f.r0, f.w0, f.s2, f.s1})
	if all.NextBefore != nil {
		t.Errorf("next_before = %d, want null", *all.NextBefore)
	}

	// Summary values come from the scan row.
	s3 := p1.Items[1]
	eq(t, "s3 counts", s3.Counts, countsJSON{High: 2, Low: 2})
	eq(t, "s3 fixable/secrets/suppressed", []int{s3.Fixable, s3.Secrets, s3.Suppressed}, []int{4, 0, 1})
	if s3.Kind != "image" || s3.ArtifactName != "web" || s3.Ref != "main" || !s3.RefProtected || s3.TrivyVersion != "0.58.0" ||
		unix(s3.ScannedAt) != unix(at(f.base, day)) || s3.ServiceID != f.alpha {
		t.Errorf("s3 = %+v", s3)
	}

	var feature scanPageJSON
	getJSON(t, mux, base+"?ref=feature", &feature)
	eq(t, "feature", ids(feature), []int64{f.f1})

	var none scanPageJSON
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/scans", f.empty), &none)
	if none.Items == nil || len(none.Items) != 0 || none.NextBefore != nil {
		t.Errorf("empty = %+v", none)
	}

	for _, q := range []string{"limit=0", "limit=201", "limit=x", "before=-1", "before=x", "ref=" + url.QueryEscape(strings.Repeat("r", 1025))} {
		wantError(t, mux, base+"?"+q, 400, "invalid_parameter")
	}
}

func TestGetScanURLs(t *testing.T) {
	f := seed(t)
	path := fmt.Sprintf("/api/v1/scans/%d", f.s3)
	var scan struct {
		scanSummaryJSON
		GitlabProjectPath string  `json:"gitlab_project_path"`
		PipelineURL       *string `json:"pipeline_url"`
		JobURL            *string `json:"job_url"`
		CommitURL         *string `json:"commit_url"`
	}
	var pipeline, job int64
	err := f.pool.QueryRow(context.Background(),
		`SELECT gitlab_pipeline_id, gitlab_job_id FROM scans WHERE id = $1`, f.s3).Scan(&pipeline, &job)
	if err != nil {
		t.Fatal(err)
	}
	sha := fmt.Sprintf("%040x", job)

	getJSON(t, newMux(f.pool, "https://gitlab.example.com/"), path, &scan)
	if scan.ID != f.s3 || scan.GitlabProjectPath != "grp/alpha" || scan.CommitSha != sha {
		t.Errorf("scan = %+v", scan)
	}
	want := []string{
		fmt.Sprintf("https://gitlab.example.com/grp/alpha/-/pipelines/%d", pipeline),
		fmt.Sprintf("https://gitlab.example.com/grp/alpha/-/jobs/%d", job),
		"https://gitlab.example.com/grp/alpha/-/commit/" + sha,
	}
	if scan.PipelineURL == nil || scan.JobURL == nil || scan.CommitURL == nil ||
		*scan.PipelineURL != want[0] || *scan.JobURL != want[1] || *scan.CommitURL != want[2] {
		t.Errorf("urls = %v %v %v, want %v", scan.PipelineURL, scan.JobURL, scan.CommitURL, want)
	}

	// Without a GitLab URL the links are null, and no raw report is exposed.
	code, body := get(t, newMux(f.pool, ""), path)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"pipeline_url", "job_url", "commit_url"} {
		if string(raw[k]) != "null" {
			t.Errorf("%s = %s, want null", k, raw[k])
		}
	}
	if _, ok := raw["raw_report"]; ok {
		t.Error("raw_report exposed")
	}
	var keys []string
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	eq(t, "keys", keys, []string{
		"artifact_name", "commit_sha", "commit_url", "counts", "fixable", "gitlab_job_id", "gitlab_pipeline_id",
		"gitlab_project_path", "id", "job_url", "kind", "pipeline_url", "received_at", "ref", "ref_protected",
		"scanned_at", "secrets", "service_id", "suppressed", "trivy_version",
	})
}

// diffView summarises diff items as "artifact/vuln_id/severity".
func view(items []vulnerabilityJSON) []string {
	out := []string{}
	for _, v := range items {
		out = append(out, v.ArtifactName+"/"+v.VulnID+"/"+v.Severity)
	}
	return out
}

func TestScanDiff(t *testing.T) {
	f := seed(t)
	mux := newMux(f.pool, "")
	diff := func(scan int64) scanDiffJSON {
		var d scanDiffJSON
		getJSON(t, mux, fmt.Sprintf("/api/v1/scans/%d/diff", scan), &d)
		return d
	}

	// Without a previous scan every finding is new, most severe first.
	d := diff(f.s1)
	if d.ScanID != f.s1 || d.PreviousScanID != nil {
		t.Errorf("s1 = %d previous %v", d.ScanID, d.PreviousScanID)
	}
	eq(t, "s1 new", view(d.New), []string{"web/CVE-2/CRITICAL", "web/CVE-1/HIGH"})
	eq(t, "s1 fixed/unchanged", []any{view(d.Fixed), d.Unchanged}, []any{[]string{}, 0})
	if got := unix(d.New[0].FirstSeenAt); got != unix(at(f.base, 10*day)) {
		t.Errorf("first_seen_at = %d", got)
	}

	// s3 against s2: CVE-4 is new (once, although listed twice), CVE-2 is
	// fixed, CVE-1 and CVE-3 are unchanged, and the suppressed CVE-9 appears
	// nowhere.
	d = diff(f.s3)
	if d.PreviousScanID == nil || *d.PreviousScanID != f.s2 {
		t.Errorf("s3 previous = %v, want %d", d.PreviousScanID, f.s2)
	}
	eq(t, "s3 new", view(d.New), []string{"web/CVE-4/LOW"})
	eq(t, "s3 fixed", view(d.Fixed), []string{"web/CVE-2/CRITICAL"})
	eq(t, "s3 unchanged", d.Unchanged, 2)
	eq(t, "s3 unchanged items", view(d.UnchangedItems), []string{"web/CVE-1/HIGH", "web/CVE-3/HIGH"})
	eq(t, "s3 counts", []countsJSON{d.NewCounts, d.FixedCounts, d.UnchangedCounts},
		[]countsJSON{{Low: 1}, {Critical: 1}, {High: 2}})
	if got := unix(d.New[0].FirstSeenAt); got != unix(at(f.base, day)) {
		t.Errorf("new first_seen_at = %d", got)
	}
	if got := unix(d.Fixed[0].FirstSeenAt); got != unix(at(f.base, 10*day)) {
		t.Errorf("fixed first_seen_at = %d, want the scan s1 time", got)
	}
	if d.New[0].ScanID != f.s3 || d.Fixed[0].ScanID != *d.PreviousScanID {
		t.Errorf("scan ids = %d, %d", d.New[0].ScanID, d.Fixed[0].ScanID)
	}

	// Previous scans are per artifact: w1 compares with w0, not with s3.
	d = diff(f.w1)
	if d.PreviousScanID == nil || *d.PreviousScanID != f.w0 {
		t.Errorf("w1 previous = %v, want %d", d.PreviousScanID, f.w0)
	}
	eq(t, "w1 new", view(d.New), []string{"worker/CVE-6/CRITICAL", "worker/CVE-5/MEDIUM", "worker/CVE-8/UNKNOWN"})
	eq(t, "w1 fixed", view(d.Fixed), []string{})
	eq(t, "w1 unchanged", d.Unchanged, 1)

	// Secret scans have no vulnerabilities and diff to nothing.
	d = diff(f.r1)
	if d.PreviousScanID == nil || *d.PreviousScanID != f.r0 {
		t.Errorf("r1 previous = %v", d.PreviousScanID)
	}
	eq(t, "r1", []any{view(d.New), view(d.Fixed), d.Unchanged}, []any{[]string{}, []string{}, 0})

	// Another ref has its own history.
	if d = diff(f.f1); d.PreviousScanID != nil || len(d.New) != 1 {
		t.Errorf("f1 = %+v", d)
	}
}

func TestScanDiffBase(t *testing.T) {
	f := seed(t)
	mux := newMux(f.pool, "")
	diff := func(scan, base int64) scanDiffJSON {
		var d scanDiffJSON
		getJSON(t, mux, fmt.Sprintf("/api/v1/scans/%d/diff?base=%d", scan, base), &d)
		return d
	}

	// s3 against s1 instead of its previous scan s2: CVE-3 and CVE-4 are new,
	// CVE-2 is fixed and CVE-1 is unchanged.
	d := diff(f.s3, f.s1)
	if d.PreviousScanID == nil || *d.PreviousScanID != f.s1 || d.Base == nil || d.Base.ID != f.s1 {
		t.Fatalf("s3 base = %v %+v, want %d", d.PreviousScanID, d.Base, f.s1)
	}
	eq(t, "base summary", []any{d.Base.Ref, d.Base.ArtifactName, d.Base.Kind, d.Base.Counts.Critical, d.Base.Counts.High},
		[]any{"main", "web", "image", 1, 1})
	eq(t, "s3/s1 new", view(d.New), []string{"web/CVE-3/HIGH", "web/CVE-4/LOW"})
	eq(t, "s3/s1 fixed", view(d.Fixed), []string{"web/CVE-2/CRITICAL"})
	eq(t, "s3/s1 totals", []int{d.NewTotal, d.FixedTotal, d.Unchanged}, []int{2, 1, 1})
	eq(t, "s3/s1 unchanged items", view(d.UnchangedItems), []string{"web/CVE-1/HIGH"})
	eq(t, "s3/s1 counts", []countsJSON{d.NewCounts, d.FixedCounts, d.UnchangedCounts},
		[]countsJSON{{High: 1, Low: 1}, {Critical: 1}, {High: 1}})
	if d.Fixed[0].ScanID != f.s1 {
		t.Errorf("fixed scan id = %d, want %d", d.Fixed[0].ScanID, f.s1)
	}

	// The base may be on another ref: s3 against the older feature scan f1.
	d = diff(f.s3, f.f1)
	if d.Base == nil || d.Base.Ref != "feature" {
		t.Fatalf("f1 base = %+v", d.Base)
	}
	eq(t, "s3/f1 new", view(d.New), []string{"web/CVE-1/HIGH", "web/CVE-3/HIGH", "web/CVE-4/LOW"})
	eq(t, "s3/f1 fixed", view(d.Fixed), []string{"web/CVE-7/CRITICAL"})
	eq(t, "s3/f1 unchanged", d.Unchanged, 0)

	// Without base the previous scan of the same ref is used, and named.
	var plain scanDiffJSON
	getJSON(t, mux, fmt.Sprintf("/api/v1/scans/%d/diff", f.s3), &plain)
	if plain.Base == nil || plain.Base.ID != f.s2 {
		t.Errorf("default base = %+v, want %d", plain.Base, f.s2)
	}
	getJSON(t, mux, fmt.Sprintf("/api/v1/scans/%d/diff", f.s1), &plain)
	if plain.Base != nil || plain.PreviousScanID != nil {
		t.Errorf("first scan base = %+v", plain.Base)
	}

	// The base must be an earlier scan of the same service, artifact and kind.
	for name, base := range map[string]string{
		"itself": fmt.Sprint(f.s3), "newer": fmt.Sprint(f.s3), "other artifact": fmt.Sprint(f.w0),
		"other kind": fmt.Sprint(f.r0), "other service": fmt.Sprint(f.b1), "unknown": "999999",
		"zero": "0", "negative": "-4", "text": "x",
	} {
		scan := f.s3
		if name == "newer" {
			scan = f.s2
		}
		t.Run(name, func(t *testing.T) {
			wantError(t, mux, fmt.Sprintf("/api/v1/scans/%d/diff?base=%s", scan, base), 400, "invalid_parameter")
		})
	}
	wantError(t, mux, fmt.Sprintf("/api/v1/scans/999999/diff?base=%d", f.s1), 404, "not_found")
}

func TestEarlierScans(t *testing.T) {
	f := seed(t)
	mux := newMux(f.pool, "")
	type page struct {
		Items []struct {
			ID         int64  `json:"id"`
			Ref        string `json:"ref"`
			Artifact   string `json:"artifact_name"`
			PipelineID int64  `json:"gitlab_pipeline_id"`
		}
		NextBefore *int64 `json:"next_before"`
	}
	list := func(path string) ([]int64, *int64) {
		var p page
		getJSON(t, mux, path, &p)
		ids := []int64{}
		for _, it := range p.Items {
			ids = append(ids, it.ID)
		}
		return ids, p.NextBefore
	}

	// Every ref, newest first; never the scan itself, other artifacts or kinds.
	ids, next := list(fmt.Sprintf("/api/v1/scans/%d/earlier", f.s3))
	eq(t, "s3 earlier", ids, []int64{f.s2, f.s1, f.f1})
	if next != nil {
		t.Errorf("next_before = %d", *next)
	}
	ids, _ = list(fmt.Sprintf("/api/v1/scans/%d/earlier?ref=main", f.s3))
	eq(t, "s3 earlier on main", ids, []int64{f.s2, f.s1})
	ids, _ = list(fmt.Sprintf("/api/v1/scans/%d/earlier", f.w1))
	eq(t, "w1 earlier", ids, []int64{f.w0})
	ids, _ = list(fmt.Sprintf("/api/v1/scans/%d/earlier", f.f1))
	eq(t, "f1 earlier", ids, []int64{})

	// Pages follow next_before.
	ids, next = list(fmt.Sprintf("/api/v1/scans/%d/earlier?limit=2", f.s3))
	eq(t, "page 1", ids, []int64{f.s2, f.s1})
	if next == nil || *next != f.s1 {
		t.Fatalf("next_before = %v, want %d", next, f.s1)
	}
	ids, next = list(fmt.Sprintf("/api/v1/scans/%d/earlier?limit=2&before=%d", f.s3, *next))
	eq(t, "page 2", ids, []int64{f.f1})
	if next != nil {
		t.Errorf("last page next_before = %d", *next)
	}

	for _, q := range []string{"limit=0", "limit=201", "before=-1", "before=x", "ref=" + strings.Repeat("r", 1025)} {
		wantError(t, mux, fmt.Sprintf("/api/v1/scans/%d/earlier?%s", f.s3, q), 400, "invalid_parameter")
	}
	wantError(t, mux, "/api/v1/scans/999999/earlier", 404, "not_found")
	wantError(t, mux, "/api/v1/scans/x/earlier", 404, "not_found")
}

type vulnPage = vulnerabilityPageJSON

func TestVulnerabilities(t *testing.T) {
	f := seed(t)
	mux := newMux(f.pool, "")
	base := fmt.Sprintf("/api/v1/services/%d/vulnerabilities", f.alpha)

	var all vulnPage
	getJSON(t, mux, base, &all)
	eq(t, "unsuppressed in id order", view(all.Items), []string{
		"web/CVE-1/HIGH", "web/CVE-3/HIGH", "web/CVE-4/LOW", "web/CVE-4/LOW",
		"worker/CVE-1/HIGH", "worker/CVE-5/MEDIUM", "worker/CVE-6/CRITICAL", "worker/CVE-8/UNKNOWN",
	})
	if all.NextAfter != nil {
		t.Errorf("next_after = %d, want null", *all.NextAfter)
	}
	ids := []int64{f.s3Rows[0], f.s3Rows[1], f.s3Rows[2], f.s3Rows[3], f.w1Rows[0], f.w1Rows[1], f.w1Rows[2], f.w1Rows[3]}
	var gotIDs []int64
	for _, v := range all.Items {
		gotIDs = append(gotIDs, v.ID)
	}
	eq(t, "ids", gotIDs, ids)

	// first_seen_at: web CVE-1 first appeared in s1, CVE-3 in s2; worker CVE-1
	// first appeared in w0 (the web scans do not count for another artifact).
	first := map[string]int64{}
	for _, v := range all.Items {
		first[fmt.Sprintf("%s/%s", v.ArtifactName, v.VulnID)] = unix(v.FirstSeenAt)
	}
	eq(t, "first_seen_at", first, map[string]int64{
		"web/CVE-1": unix(at(f.base, 10*day)), "web/CVE-3": unix(at(f.base, 5*day)), "web/CVE-4": unix(at(f.base, day)),
		"worker/CVE-1": unix(at(f.base, 4*day)), "worker/CVE-5": unix(at(f.base, 2*day)),
		"worker/CVE-6": unix(at(f.base, 2*day)), "worker/CVE-8": unix(at(f.base, 2*day)),
	})

	// Every field of one row.
	v := all.Items[1]
	if v.ScanID != f.s3 || v.Target != "app/package-lock.json" || v.Class != "lang-pkgs" || v.PkgName != "libx" ||
		v.InstalledVersion != "1.0.0" || v.FixedVersion != "1.0.5, 1.1.2" || v.Title != "libx bug" || v.Suppressed ||
		v.Suppression != "" || v.Status != "" || v.PkgType != "" || v.PrimaryURL != "" {
		t.Errorf("row = %+v", v)
	}
	if all.Items[2].PkgPath != "/usr/bin/curl" || all.Items[3].PkgPath != "/usr/lib/libcurl.so" {
		t.Errorf("paths = %q %q", all.Items[2].PkgPath, all.Items[3].PkgPath)
	}

	// Paging by id: 8 rows in pages of 3, 3, 2; a page that ends exactly at the
	// end has no next page, one short of it has.
	var seen []int64
	after := int64(0)
	for i, wantLen := range []int{3, 3, 2} {
		var p vulnPage
		getJSON(t, mux, fmt.Sprintf("%s?limit=3&after=%d", base, after), &p)
		if len(p.Items) != wantLen {
			t.Fatalf("page %d has %d items, want %d", i, len(p.Items), wantLen)
		}
		for _, it := range p.Items {
			seen = append(seen, it.ID)
		}
		if i < 2 {
			if p.NextAfter == nil || *p.NextAfter != p.Items[2].ID {
				t.Fatalf("page %d next_after = %v", i, p.NextAfter)
			}
			after = *p.NextAfter
		} else if p.NextAfter != nil {
			t.Errorf("last page next_after = %d", *p.NextAfter)
		}
	}
	eq(t, "paged ids", seen, ids)
	var exact, short vulnPage
	getJSON(t, mux, base+"?limit=8", &exact)
	getJSON(t, mux, base+"?limit=7", &short)
	if exact.NextAfter != nil || short.NextAfter == nil || len(short.Items) != 7 {
		t.Errorf("exact next_after %v, short next_after %v (%d items)", exact.NextAfter, short.NextAfter, len(short.Items))
	}

	// Filters.
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"severity=CRITICAL", []string{"worker/CVE-6"}},
		{"severity=high,low", []string{"web/CVE-1", "web/CVE-3", "web/CVE-4", "web/CVE-4", "worker/CVE-1"}},
		{"severity=critical", []string{"worker/CVE-6"}},
		{"fixable=true", []string{"web/CVE-1", "web/CVE-3", "web/CVE-4", "web/CVE-4", "worker/CVE-1", "worker/CVE-5", "worker/CVE-6"}},
		{"fixable=false", []string{"worker/CVE-8"}},
		{"suppressed=true&severity=MEDIUM", []string{"web/CVE-9", "worker/CVE-5"}},
		{"suppressed=false&severity=MEDIUM", []string{"worker/CVE-5"}},
		{"q=LIBX", []string{"web/CVE-3"}},
		{"q=cve-6", []string{"worker/CVE-6"}},
		{"q=pollution", []string{"worker/CVE-5"}},
		{"q=%25", nil},
		{"q=_", nil},
		{"severity=HIGH&q=openssl", []string{"web/CVE-1", "worker/CVE-1"}},
		{"ref=feature", []string{"web/CVE-7"}},
	} {
		var p vulnPage
		getJSON(t, mux, base+"?"+tc.query, &p)
		var got []string
		for _, it := range p.Items {
			got = append(got, it.ArtifactName+"/"+it.VulnID)
		}
		eq(t, tc.query, got, tc.want)
	}

	// Including suppressed findings adds CVE-9 with its reason, in id order.
	var withSuppressed vulnPage
	getJSON(t, mux, base+"?suppressed=true", &withSuppressed)
	if len(withSuppressed.Items) != 9 || withSuppressed.Items[4].VulnID != "CVE-9" ||
		!withSuppressed.Items[4].Suppressed || withSuppressed.Items[4].Suppression != "accepted" {
		t.Errorf("with suppressed = %+v", view(withSuppressed.Items))
	}

	var none vulnPage
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/vulnerabilities", f.empty), &none)
	if none.Items == nil || len(none.Items) != 0 || none.NextAfter != nil {
		t.Errorf("empty = %+v", none)
	}

	for _, q := range []string{
		"severity=BOGUS", "severity=HIGH,", "severity=", "fixable=maybe", "suppressed=1", "after=-1", "after=x",
		"limit=0", "limit=501", "limit=x", "q=" + strings.Repeat("q", 201),
	} {
		code, _ := get(t, mux, base+"?"+q)
		want := 400
		if q == "severity=" {
			want = 200 // an empty list means no filter
		}
		if code != want {
			t.Errorf("%s = %d, want %d", q, code, want)
		}
	}
	wantError(t, mux, base+"?severity=BOGUS", 400, "invalid_parameter")
}

func TestSecrets(t *testing.T) {
	f := seed(t)
	mux := newMux(f.pool, "")
	code, body := get(t, mux, fmt.Sprintf("/api/v1/services/%d/secrets", f.alpha))
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	var out struct{ Items []secretFindingJSON }
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range out.Items {
		got = append(got, fmt.Sprintf("%s/%s/%s/%s/%d-%d/%v/%d", s.ArtifactName, s.RuleID, s.Severity, s.Target, s.StartLine, s.EndLine, s.Suppressed, s.ScanID))
	}
	// Severity, then id; only the current secret scan (r1), suppressed flagged.
	eq(t, "secrets", got, []string{
		fmt.Sprintf("repo/github-pat/HIGH/.env/3-3/false/%d", f.r1),
		fmt.Sprintf("repo/slack-token/MEDIUM/ci/vars.sh/12-14/false/%d", f.r1),
		fmt.Sprintf("repo/generic-secret/LOW/config.yml/9-9/true/%d", f.r1),
	})
	if out.Items[0].Title != "GitHub PAT" || out.Items[0].Category != "cat" {
		t.Errorf("first = %+v", out.Items[0])
	}

	// No matched value (or anything beyond the schema) is ever returned.
	var raw struct{ Items []map[string]json.RawMessage }
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range raw.Items[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	eq(t, "keys", keys, []string{"artifact_name", "category", "end_line", "id", "rule_id", "scan_id", "severity", "start_line", "suppressed", "target", "title"})

	var none struct{ Items []secretFindingJSON }
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/secrets?ref=feature", f.alpha), &none)
	if none.Items == nil || len(none.Items) != 0 {
		t.Errorf("feature secrets = %+v", none)
	}
	_, body = get(t, mux, fmt.Sprintf("/api/v1/services/%d/secrets", f.empty))
	if strings.TrimSpace(string(body)) != `{"items":[]}` {
		t.Errorf("empty = %s", body)
	}
}

func TestTrends(t *testing.T) {
	f := seed(t)
	mux := newMux(f.pool, "")
	base := fmt.Sprintf("/api/v1/services/%d/trends", f.alpha)
	pts := func(query string) (string, []trendPointJSON) {
		var tr trendsJSON
		getJSON(t, mux, base+query, &tr)
		ref := "<nil>"
		if tr.Ref != nil {
			ref = *tr.Ref
		}
		return ref, tr.Points
	}
	idsOf := func(ps []trendPointJSON) []int64 {
		out := []int64{}
		for _, p := range ps {
			out = append(out, p.ScanID)
		}
		return out
	}

	ref, ps := pts("")
	if ref != "main" {
		t.Errorf("ref = %s", ref)
	}
	eq(t, "all points oldest first", idsOf(ps), []int64{f.s1, f.s2, f.w0, f.r0, f.w1, f.s3, f.r1})
	kinds := []string{}
	for _, p := range ps {
		kinds = append(kinds, p.Kind)
	}
	eq(t, "point kinds", kinds, []string{"image", "image", "image", "secret-fs", "image", "image", "secret-fs"})
	s3 := ps[5]
	eq(t, "s3 point", []any{s3.ArtifactName, s3.Counts, s3.Fixable, s3.GitlabPipelineID > 1000, len(s3.CommitSha), unix(s3.ScannedAt)},
		[]any{"web", countsJSON{High: 2, Low: 2}, 4, true, 40, unix(at(f.base, day))})

	_, ps = pts("?days=6")
	eq(t, "6 days", idsOf(ps), []int64{f.s2, f.w0, f.r0, f.w1, f.s3, f.r1})
	_, ps = pts("?days=1")
	eq(t, "1 day", idsOf(ps), []int64{f.r1})
	ref, ps = pts("?ref=feature")
	eq(t, "feature", []any{ref, idsOf(ps)}, []any{"feature", []int64{f.f1}})
	ref, ps = pts("?ref=feature&days=15")
	eq(t, "feature outside the window", []any{ref, idsOf(ps)}, []any{"feature", []int64{}})

	_, body := get(t, mux, fmt.Sprintf("/api/v1/services/%d/trends", f.empty))
	if strings.TrimSpace(string(body)) != `{"ref":null,"ref_source":"latest","points":[],"total":0,"truncated":false}` {
		t.Errorf("empty = %s", body)
	}
	for _, q := range []string{"days=0", "days=366", "days=x"} {
		wantError(t, mux, base+"?"+q, 400, "invalid_parameter")
	}
}

func TestTrendChanges(t *testing.T) {
	f := seed(t)
	mux := newMux(f.pool, "")
	base := fmt.Sprintf("/api/v1/services/%d/trends", f.alpha)
	type change struct {
		scan, prev int64
		ref        string
		new        countsJSON
		fixed      int
	}
	changes := func(query string) (string, []change) {
		t.Helper()
		var tr trendsJSON
		getJSON(t, mux, base+query, &tr)
		out := []change{}
		for _, p := range tr.Points {
			c := change{scan: p.ScanID, ref: p.Ref, new: p.New, fixed: p.Fixed}
			if p.PreviousScanID != nil {
				c.prev = *p.PreviousScanID
			}
			out = append(out, c)
		}
		ref := "<nil>"
		if tr.Ref != nil {
			ref = *tr.Ref
		}
		return ref, out
	}

	// See the fixture: s3 has curl twice under one key and a suppressed
	// busybox, neither of which counts twice or at all; it drops zlib.
	_, got := changes("")
	want := []change{
		{f.s1, 0, "main", countsJSON{Critical: 1, High: 1}, 0},
		{f.s2, f.s1, "main", countsJSON{High: 1}, 0},
		{f.w0, 0, "main", countsJSON{High: 1}, 0},
		{f.r0, 0, "main", countsJSON{}, 0},
		{f.w1, f.w0, "main", countsJSON{Critical: 1, Medium: 1, Unknown: 1}, 0},
		{f.s3, f.s2, "main", countsJSON{Low: 1}, 1},
		{f.r1, f.r0, "main", countsJSON{}, 0},
	}
	eq(t, "changes", got, want)

	// The previous scan may lie before the window.
	_, got = changes("?days=6")
	eq(t, "6 days", got, want[1:])

	// Every point agrees with the scan diff.
	for _, c := range want {
		var d scanDiffJSON
		getJSON(t, mux, fmt.Sprintf("/api/v1/scans/%d/diff", c.scan), &d)
		total := c.new.Critical + c.new.High + c.new.Medium + c.new.Low + c.new.Unknown
		if d.NewTotal != total || d.FixedTotal != c.fixed {
			t.Errorf("scan %d: trend new %d fixed %d, diff new %d fixed %d", c.scan, total, c.fixed, d.NewTotal, d.FixedTotal)
		}
		prev := int64(0)
		if d.PreviousScanID != nil {
			prev = *d.PreviousScanID
		}
		if prev != c.prev {
			t.Errorf("scan %d: trend previous %d, diff previous %d", c.scan, c.prev, prev)
		}
	}

	// Every ref: the feature branch scan joins, the dashboard ref is still named.
	ref, got := changes("?all_refs=true")
	eq(t, "all refs", []any{ref, got}, []any{"main", append([]change{{f.f1, 0, "feature", countsJSON{Critical: 1}, 0}}, want...)})
	ref, got = changes("?ref=feature&all_refs=false")
	eq(t, "one ref", []any{ref, got}, []any{"feature", []change{{f.f1, 0, "feature", countsJSON{Critical: 1}, 0}}})
	wantError(t, mux, base+"?all_refs=yes", 400, "invalid_parameter")
	_, body := get(t, mux, fmt.Sprintf("/api/v1/services/%d/trends?all_refs=true", f.empty))
	if strings.TrimSpace(string(body)) != `{"ref":null,"ref_source":"latest","points":[],"total":0,"truncated":false}` {
		t.Errorf("empty, all refs = %s", body)
	}

	// A key reported twice with different severities is one finding, at its
	// most severe; a severity change alone is neither new nor fixed.
	svc := newService(t, f.pool, "dup", "grp/dup", false)
	low := vuln{id: "CVE-77", pkg: "p", version: "1", target: "t", class: "os-pkgs", severity: "LOW"}
	high := low
	high.severity = "HIGH"
	d1 := newScan(t, f.pool, svc, "image", "img", "main", at(f.base, 2*day))
	addVulns(t, f.pool, d1, low, high)
	d2 := newScan(t, f.pool, svc, "image", "img", "main", at(f.base, day))
	addVulns(t, f.pool, d2, high)
	var tr trendsJSON
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/trends", svc), &tr)
	if len(tr.Points) != 2 || tr.Points[0].New != (countsJSON{High: 1}) || tr.Points[1].New != (countsJSON{}) ||
		tr.Points[1].Fixed != 0 {
		t.Errorf("duplicate keys = %+v", tr.Points)
	}
}

// The trends list the newest scans of the window up to the cap, over every
// ref together, and say how many the window holds.
func TestTrendsCap(t *testing.T) {
	f := seed(t)
	mux := newMux(f.pool, "")
	base := fmt.Sprintf("/api/v1/services/%d/trends", f.alpha)
	read := func(query string) trendsJSON {
		t.Helper()
		var tr trendsJSON
		getJSON(t, mux, base+query, &tr)
		return tr
	}
	type point struct {
		scan, prev int64
		new        countsJSON
		fixed      int
	}
	points := func(tr trendsJSON) []point {
		out := []point{}
		for _, p := range tr.Points {
			pt := point{scan: p.ScanID, new: p.New, fixed: p.Fixed}
			if p.PreviousScanID != nil {
				pt.prev = *p.PreviousScanID
			}
			out = append(out, pt)
		}
		return out
	}

	full := read("")
	eq(t, "below the cap", []any{len(full.Points), full.Total, full.Truncated}, []any{7, 7, false})

	defer func(n int) { maxTrendScans = n }(maxTrendScans)
	maxTrendScans = 4
	// The newest four of main; w1 and s3 still compare with scans left out.
	cut := read("")
	eq(t, "cut", []any{points(cut), cut.Total, cut.Truncated}, []any{[]point{
		{f.r0, 0, countsJSON{}, 0},
		{f.w1, f.w0, countsJSON{Critical: 1, Medium: 1, Unknown: 1}, 0},
		{f.s3, f.s2, countsJSON{Low: 1}, 1},
		{f.r1, f.r0, countsJSON{}, 0},
	}, 7, true})
	// Every ref: the feature scan is the oldest, so the same four are listed
	// out of eight.
	all := read("?all_refs=true")
	eq(t, "cut, every ref", []any{points(all), all.Total, all.Truncated}, []any{points(cut), 8, true})
	// Exactly at the cap nothing is left out.
	maxTrendScans = 7
	at := read("")
	eq(t, "at the cap", []any{len(at.Points), at.Total, at.Truncated}, []any{7, 7, false})
}

// With the real cap: 305 scans in the window list the newest 300.
func TestTrendsCapDefault(t *testing.T) {
	f := seed(t)
	svc := newService(t, f.pool, "busy", "grp/busy", false)
	_, err := f.pool.Exec(context.Background(), `
		INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref,
		                   ref_protected, commit_sha, schema_version, scanned_at, raw_report)
		SELECT $1, 'image', 'img', 5000 + g, 900000 + g, 'main', true, 'c', 2,
		       now() - (306 - g) * interval '1 hour', '\x00'
		FROM generate_series(1, 305) g`, svc)
	if err != nil {
		t.Fatal(err)
	}
	var tr trendsJSON
	getJSON(t, newMux(f.pool, ""), fmt.Sprintf("/api/v1/services/%d/trends?days=30", svc), &tr)
	if len(tr.Points) != 300 || tr.Total != 305 || !tr.Truncated {
		t.Fatalf("%d points of %d, truncated %v; want 300 of 305, truncated", len(tr.Points), tr.Total, tr.Truncated)
	}
	first, last := tr.Points[0], tr.Points[299]
	if first.GitlabPipelineID != 5006 || last.GitlabPipelineID != 5305 || first.PreviousScanID == nil {
		t.Errorf("first %d (previous %v), last %d; want 5006 with a previous scan, 5305",
			first.GitlabPipelineID, first.PreviousScanID, last.GitlabPipelineID)
	}
}

func TestSearch(t *testing.T) {
	f := seed(t)
	mux := newMux(f.pool, "")
	type item = struct {
		Service          serviceJSON `json:"service"`
		Ref              string
		ArtifactName     string `json:"artifact_name"`
		ScanID           int64  `json:"scan_id"`
		ScannedAt        time.Time
		PkgName          string `json:"pkg_name"`
		InstalledVersion string `json:"installed_version"`
		FixedVersion     string `json:"fixed_version"`
		Severity         string
	}
	search := func(id string) (string, []string) {
		var out struct {
			VulnID string `json:"vuln_id"`
			Items  []item
		}
		getJSON(t, mux, "/api/v1/search/vulnerabilities?id="+url.QueryEscape(id), &out)
		got := []string{}
		for _, it := range out.Items {
			got = append(got, fmt.Sprintf("%s/%s/%s/%d/%s %s %s %s", it.Service.Name, it.Ref, it.ArtifactName, it.ScanID,
				it.PkgName, it.InstalledVersion, it.FixedVersion, it.Severity))
		}
		return out.VulnID, got
	}

	// Case-insensitive; archived gamma is excluded; the older scan s1 is not
	// current; the severity is the one reported in each artifact.
	vid, got := search("cve-1")
	if vid != "cve-1" {
		t.Errorf("vuln_id = %q", vid)
	}
	// The most severe findings are listed first, then by service name.
	eq(t, "cve-1", got, []string{
		fmt.Sprintf("beta/main/web/%d/openssl 3.0.1 3.0.9 CRITICAL", f.b1),
		fmt.Sprintf("alpha/main/web/%d/openssl 3.0.1 3.0.9 HIGH", f.s3),
		fmt.Sprintf("alpha/main/worker/%d/openssl 3.0.1 3.0.9 HIGH", f.w1),
	})
	// The summary counts every match: severities as reported per artifact,
	// the artifacts per service, the distinct fixed versions and a title.
	var sum searchJSON
	getJSON(t, mux, "/api/v1/search/vulnerabilities?id=CVE-1", &sum)
	eq(t, "cve-1 summary", []any{sum.Counts, sum.TotalServices, sum.TotalArtifacts, sum.Title, sum.PrimaryURL, sum.FixedVersions},
		[]any{countsJSON{Critical: 1, High: 2}, int64(2), int64(3), "openssl flaw", "", []string{"3.0.9"}})
	getJSON(t, mux, "/api/v1/search/vulnerabilities?id=CVE-3", &sum)
	eq(t, "cve-3 fixed versions", sum.FixedVersions, []string{"1.0.5", "1.1.2"})
	getJSON(t, mux, "/api/v1/search/vulnerabilities?id=CVE-404", &sum)
	eq(t, "no match summary", []any{sum.Counts, sum.TotalArtifacts, sum.Title, sum.FixedVersions},
		[]any{countsJSON{}, int64(0), "", []string{}})

	// Fixed since: CVE-2 was in s1 and s2 but not in the current scan.
	_, got = search("CVE-2")
	eq(t, "cve-2", got, []string{})
	// Other refs count, suppressed findings do not.
	_, got = search("CVE-7")
	eq(t, "cve-7", got, []string{fmt.Sprintf("alpha/feature/web/%d/x 1  CRITICAL", f.f1)})
	_, got = search("CVE-9")
	eq(t, "cve-9 suppressed", got, []string{})
	// Exact match, not substring.
	_, got = search("CVE")
	eq(t, "cve", got, []string{})

	// A finding moves out of the current state when a newer scan lacks it.
	extra := newScan(t, f.pool, f.beta, "image", "web", "main", f.base)
	addVulns(t, f.pool, extra, vuln{id: "CVE-100", pkg: "p", version: "1", target: "t", class: "os-pkgs", severity: "LOW"})
	_, got = search("CVE-1")
	eq(t, "cve-1 after beta rescanned", len(got), 2)

	for _, id := range []string{"", "ab", "CVE 1", "CVE-1;", strings.Repeat("a", 101), "é-12"} {
		wantError(t, mux, "/api/v1/search/vulnerabilities?id="+url.QueryEscape(id), 400, "invalid_parameter")
	}
	wantError(t, mux, "/api/v1/search/vulnerabilities", 400, "invalid_parameter")
	for _, id := range []string{"CVE-2024-3094", "GHSA-xxxx-yyyy-zzzz", "abc", "a:b_c.d", strings.Repeat("a", 100)} {
		if code, body := get(t, mux, "/api/v1/search/vulnerabilities?id="+id); code != 200 {
			t.Errorf("id %q = %d %s", id, code, body)
		}
	}
}

func TestDatabaseFailureIs500(t *testing.T) {
	f := seed(t)
	mux := newMux(f.pool, "")
	f.pool.Close()
	for _, p := range []string{
		fmt.Sprintf("/api/v1/services/%d/overview", f.alpha),
		fmt.Sprintf("/api/v1/scans/%d", f.s1),
		fmt.Sprintf("/api/v1/scans/%d/diff", f.s1),
		fmt.Sprintf("/api/v1/scans/%d/earlier", f.s3),
		"/api/v1/search/vulnerabilities?id=CVE-1",
	} {
		wantError(t, mux, p, 500, "internal_error")
	}
}
