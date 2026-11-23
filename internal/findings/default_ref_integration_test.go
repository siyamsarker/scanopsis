// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package findings

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func setDefaultRef(t *testing.T, f *fixture, service int64, ref *string) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `UPDATE services SET default_ref = $2 WHERE id = $1`, service, ref); err != nil {
		t.Fatal(err)
	}
}

func sp(s string) *string { return &s }

func scanIDs(p scanPageJSON) []int64 {
	out := []int64{}
	for _, s := range p.Items {
		out = append(out, s.ID)
	}
	return out
}

type secretList struct {
	Items []secretFindingJSON `json:"items"`
}

// Without a chosen branch, the dashboards show the ref with the most recent
// scan and say so.
func TestRefResolutionLatest(t *testing.T) {
	f := seed(t)
	mux := newMux(f.pool, "")
	var ov overviewJSON
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/overview", f.alpha), &ov)
	if ov.Ref == nil || *ov.Ref != "main" || ov.RefSource != "latest" || ov.Service.DefaultRef != nil {
		t.Errorf("overview ref = %v source %q default %v", ov.Ref, ov.RefSource, ov.Service.DefaultRef)
	}
	var tr trendsJSON
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/trends", f.alpha), &tr)
	if tr.Ref == nil || *tr.Ref != "main" || tr.RefSource != "latest" {
		t.Errorf("trends ref = %v source %q", tr.Ref, tr.RefSource)
	}
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/overview?ref=feature", f.alpha), &ov)
	if ov.Ref == nil || *ov.Ref != "feature" || ov.RefSource != "requested" {
		t.Errorf("requested ref = %v source %q", ov.Ref, ov.RefSource)
	}
}

// A chosen branch replaces the latest-scan rule on every endpoint, and an
// explicit ref still wins over it.
func TestRefResolutionChosenBranch(t *testing.T) {
	f := seed(t)
	mux := newMux(f.pool, "")
	setDefaultRef(t, f, f.alpha, sp("feature"))
	base := fmt.Sprintf("/api/v1/services/%d", f.alpha)

	var ov overviewJSON
	getJSON(t, mux, base+"/overview", &ov)
	if ov.Ref == nil || *ov.Ref != "feature" || ov.RefSource != "default" || len(ov.Artifacts) != 1 ||
		ov.Artifacts[0].ID != f.f1 || ov.Service.DefaultRef == nil || *ov.Service.DefaultRef != "feature" {
		t.Errorf("overview = %+v", ov)
	}
	eq(t, "overview refs still list every scanned ref", ov.Refs, []string{"main", "feature"})
	eq(t, "overview counts", ov.Counts, countsJSON{Critical: 1})

	var sc scanPageJSON
	getJSON(t, mux, base+"/scans", &sc)
	eq(t, "scans", scanIDs(sc), []int64{f.f1})

	var vp vulnerabilityPageJSON
	getJSON(t, mux, base+"/vulnerabilities", &vp)
	if len(vp.Items) != 1 || vp.Items[0].VulnID != "CVE-7" {
		t.Errorf("vulnerabilities = %+v", vp.Items)
	}

	var secrets secretList
	getJSON(t, mux, base+"/secrets", &secrets)
	if len(secrets.Items) != 0 {
		t.Errorf("secrets of the chosen branch = %+v", secrets.Items)
	}

	// The only scan of that branch is 20 days old.
	var tr trendsJSON
	getJSON(t, mux, base+"/trends?days=30", &tr)
	if tr.Ref == nil || *tr.Ref != "feature" || tr.RefSource != "default" || len(tr.Points) != 1 || tr.Points[0].ScanID != f.f1 {
		t.Errorf("trends = %+v", tr)
	}

	// An explicit ref wins.
	getJSON(t, mux, base+"/overview?ref=main", &ov)
	if ov.Ref == nil || *ov.Ref != "main" || ov.RefSource != "requested" || len(ov.Artifacts) != 3 {
		t.Errorf("requested overview = %+v", ov)
	}
	getJSON(t, mux, base+"/scans?ref=main", &sc)
	if len(sc.Items) != 7 {
		t.Errorf("requested scans = %d", len(sc.Items))
	}
	getJSON(t, mux, base+"/trends?ref=main", &tr)
	if tr.Ref == nil || *tr.Ref != "main" || tr.RefSource != "requested" {
		t.Errorf("requested trends = %v %q", tr.Ref, tr.RefSource)
	}

	// Other services are untouched.
	var beta overviewJSON
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/overview", f.beta), &beta)
	if beta.Ref == nil || *beta.Ref != "main" || beta.RefSource != "latest" {
		t.Errorf("beta = %v %q", beta.Ref, beta.RefSource)
	}

	// Clearing it restores the latest-scan rule.
	setDefaultRef(t, f, f.alpha, nil)
	getJSON(t, mux, base+"/overview", &ov)
	if ov.Ref == nil || *ov.Ref != "main" || ov.RefSource != "latest" {
		t.Errorf("after clearing = %v %q", ov.Ref, ov.RefSource)
	}
}

// A chosen branch that has no scans yet shows empty states for that branch;
// it never falls back to another branch.
func TestRefResolutionChosenBranchWithoutScans(t *testing.T) {
	f := seed(t)
	mux := newMux(f.pool, "")
	setDefaultRef(t, f, f.alpha, sp("release"))
	setDefaultRef(t, f, f.empty, sp("main"))
	for svc, want := range map[int64]string{f.alpha: "release", f.empty: "main"} {
		base := fmt.Sprintf("/api/v1/services/%d", svc)
		var ov overviewJSON
		getJSON(t, mux, base+"/overview", &ov)
		if ov.Ref == nil || *ov.Ref != want || ov.RefSource != "default" || len(ov.Artifacts) != 0 ||
			ov.DataAt != nil || ov.Counts != (countsJSON{}) {
			t.Errorf("overview of %d = %+v", svc, ov)
		}
		var sc scanPageJSON
		getJSON(t, mux, base+"/scans", &sc)
		var vp vulnerabilityPageJSON
		getJSON(t, mux, base+"/vulnerabilities", &vp)
		var secrets secretList
		getJSON(t, mux, base+"/secrets", &secrets)
		var tr trendsJSON
		getJSON(t, mux, base+"/trends", &tr)
		if len(sc.Items) != 0 || len(vp.Items) != 0 || len(secrets.Items) != 0 || len(tr.Points) != 0 ||
			tr.Ref == nil || *tr.Ref != want || tr.RefSource != "default" {
			t.Errorf("service %d: scans %d vulnerabilities %d secrets %d trends %+v",
				svc, len(sc.Items), len(vp.Items), len(secrets.Items), tr)
		}
	}
	// The scanned refs are still offered.
	var ov overviewJSON
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/overview", f.alpha), &ov)
	eq(t, "refs", ov.Refs, []string{"main", "feature"})
}

// The search lists every ref's current state, so the chosen branch does not
// change its matches; the service it returns carries the choice.
func TestSearchReportsChosenBranch(t *testing.T) {
	f := seed(t)
	mux := newMux(f.pool, "")
	setDefaultRef(t, f, f.alpha, sp("feature"))
	_, body := get(t, mux, "/api/v1/search/vulnerabilities?id=CVE-1")
	if !strings.Contains(string(body), `"default_ref":"feature"`) {
		t.Errorf("search body lacks the chosen branch: %s", body)
	}
}
