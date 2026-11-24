// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package findings

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

// newKeyedScan stores a scan whose artifact key differs from its name, as an
// upload of a tagged image does.
func newKeyedScan(t testing.TB, pool *pgxpool.Pool, service int64, kind, name, key, ref string, at time.Time) int64 {
	t.Helper()
	id := newScan(t, pool, service, kind, name, ref, at)
	if _, err := pool.Exec(context.Background(), `UPDATE scans SET artifact_key = $2 WHERE id = $1`, id, key); err != nil {
		t.Fatal(err)
	}
	return id
}

// tagFixture is one service whose pipelines tag the image per run:
// web-frontend-stageImage:2, :3 and :4 on main, plus a secret scan of the
// repository from the build folder.
type tagFixture struct {
	pool             *pgxpool.Pool
	svc              int64
	t2, t3, t4, repo int64
}

const stageImage = "web-frontend-stage"

func seedTags(t *testing.T) *tagFixture {
	t.Helper()
	pool := storetest.New(t)
	base := time.Now().UTC().Truncate(time.Second)
	f := &tagFixture{pool: pool}
	f.svc = newService(t, pool, "web-frontend", "demo/web-frontend", false)

	osTarget := stageImage + " (alpine 3.20.3)"
	openssl := vuln{id: "CVE-2026-1", pkg: "openssl", version: "3.3.1", target: osTarget, class: "os-pkgs", severity: "CRITICAL", fixed: "3.3.2", title: "openssl flaw"}
	zlib := vuln{id: "CVE-2026-2", pkg: "zlib", version: "1.3", target: osTarget, class: "os-pkgs", severity: "HIGH", title: "zlib flaw"}
	musl := vuln{id: "CVE-2026-3", pkg: "musl", version: "1.2.5", target: osTarget, class: "os-pkgs", severity: "HIGH", title: "musl flaw"}

	f.t2 = newKeyedScan(t, pool, f.svc, "image", stageImage+":2", stageImage, "main", base.Add(-3*day))
	addVulns(t, pool, f.t2, openssl, zlib)
	f.t3 = newKeyedScan(t, pool, f.svc, "image", stageImage+":3", stageImage, "main", base.Add(-2*day))
	addVulns(t, pool, f.t3, openssl, zlib)
	f.t4 = newKeyedScan(t, pool, f.svc, "image", stageImage+":4", stageImage, "main", base.Add(-1*day))
	addVulns(t, pool, f.t4, openssl, musl)
	f.repo = newKeyedScan(t, pool, f.svc, "secret-fs", "/builds/x/0/demo/web-frontend", ".", "main", base.Add(-12*time.Hour))
	addSecrets(t, pool, f.repo, secret{"github-pat", ".env", "HIGH", "GitHub PAT", 3, 3, false})
	return f
}

type keyedScan struct {
	ID           int64  `json:"id"`
	Kind         string `json:"kind"`
	ArtifactName string `json:"artifact_name"`
	ArtifactKey  string `json:"artifact_key"`
}

// TestArtifactKeyOverview: the tags of one image are one artifact; the
// overview shows the latest tag and counts only it.
func TestArtifactKeyOverview(t *testing.T) {
	f := seedTags(t)
	mux := newMux(f.pool, "")
	var ov struct {
		Artifacts          []keyedScan
		Counts             countsJSON
		NewSincePrevious   int `json:"new_since_previous"`
		FixedSincePrevious int `json:"fixed_since_previous"`
	}
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/overview", f.svc), &ov)
	eq(t, "artifacts", ov.Artifacts, []keyedScan{
		{ID: f.repo, Kind: "secret-fs", ArtifactName: "/builds/x/0/demo/web-frontend", ArtifactKey: "."},
		{ID: f.t4, Kind: "image", ArtifactName: stageImage + ":4", ArtifactKey: stageImage},
	})
	eq(t, "counts", ov.Counts, countsJSON{Critical: 1, High: 1})
	eq(t, "new, fixed since :3", []int{ov.NewSincePrevious, ov.FixedSincePrevious}, []int{1, 1})

	var page struct{ Items []keyedScan }
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/scans", f.svc), &page)
	eq(t, "scan list", page.Items, []keyedScan{
		{ID: f.repo, Kind: "secret-fs", ArtifactName: "/builds/x/0/demo/web-frontend", ArtifactKey: "."},
		{ID: f.t4, Kind: "image", ArtifactName: stageImage + ":4", ArtifactKey: stageImage},
		{ID: f.t3, Kind: "image", ArtifactName: stageImage + ":3", ArtifactKey: stageImage},
		{ID: f.t2, Kind: "image", ArtifactName: stageImage + ":2", ArtifactKey: stageImage},
	})

	var scan keyedScan
	getJSON(t, mux, fmt.Sprintf("/api/v1/scans/%d", f.t3), &scan)
	eq(t, "scan", scan, keyedScan{ID: f.t3, Kind: "image", ArtifactName: stageImage + ":3", ArtifactKey: stageImage})
}

// TestArtifactKeyFindings: the current findings come from the latest tag
// only and name both the key and the scanned name.
func TestArtifactKeyFindings(t *testing.T) {
	f := seedTags(t)
	mux := newMux(f.pool, "")
	type row struct {
		ScanID       int64  `json:"scan_id"`
		VulnID       string `json:"vuln_id"`
		ArtifactName string `json:"artifact_name"`
		ArtifactKey  string `json:"artifact_key"`
	}
	var vp struct{ Items []row }
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/vulnerabilities", f.svc), &vp)
	eq(t, "vulnerabilities", vp.Items, []row{
		{ScanID: f.t4, VulnID: "CVE-2026-1", ArtifactName: stageImage + ":4", ArtifactKey: stageImage},
		{ScanID: f.t4, VulnID: "CVE-2026-3", ArtifactName: stageImage + ":4", ArtifactKey: stageImage},
	})

	var secrets struct {
		Items []struct {
			ScanID       int64  `json:"scan_id"`
			ArtifactName string `json:"artifact_name"`
			ArtifactKey  string `json:"artifact_key"`
		}
	}
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/secrets", f.svc), &secrets)
	if len(secrets.Items) != 1 || secrets.Items[0].ArtifactKey != "." ||
		secrets.Items[0].ArtifactName != "/builds/x/0/demo/web-frontend" {
		t.Errorf("secrets = %+v, want one finding of key . from the build folder", secrets.Items)
	}
}

// TestArtifactKeyDiff: the previous scan of :4 is :3, and :2 is an earlier
// scan of the same artifact.
func TestArtifactKeyDiff(t *testing.T) {
	f := seedTags(t)
	mux := newMux(f.pool, "")
	type change struct {
		VulnID      string `json:"vuln_id"`
		ArtifactKey string `json:"artifact_key"`
	}
	var diff struct {
		PreviousScanID *int64     `json:"previous_scan_id"`
		Base           *keyedScan `json:"base"`
		New            []change
		Fixed          []change
		Unchanged      int
	}
	getJSON(t, mux, fmt.Sprintf("/api/v1/scans/%d/diff", f.t4), &diff)
	if diff.PreviousScanID == nil || *diff.PreviousScanID != f.t3 {
		t.Fatalf("previous scan = %v, want %d (:3)", diff.PreviousScanID, f.t3)
	}
	eq(t, "base", diff.Base, &keyedScan{ID: f.t3, Kind: "image", ArtifactName: stageImage + ":3", ArtifactKey: stageImage})
	eq(t, "new", diff.New, []change{{"CVE-2026-3", stageImage}})
	eq(t, "fixed", diff.Fixed, []change{{"CVE-2026-2", stageImage}})
	eq(t, "unchanged", diff.Unchanged, 1)

	getJSON(t, mux, fmt.Sprintf("/api/v1/scans/%d/diff?base=%d", f.t4, f.t2), &diff)
	eq(t, "base :2", diff.Base, &keyedScan{ID: f.t2, Kind: "image", ArtifactName: stageImage + ":2", ArtifactKey: stageImage})

	var earlier struct{ Items []keyedScan }
	getJSON(t, mux, fmt.Sprintf("/api/v1/scans/%d/earlier", f.t4), &earlier)
	eq(t, "earlier", earlier.Items, []keyedScan{
		{ID: f.t3, Kind: "image", ArtifactName: stageImage + ":3", ArtifactKey: stageImage},
		{ID: f.t2, Kind: "image", ArtifactName: stageImage + ":2", ArtifactKey: stageImage},
	})
}

// TestArtifactKeyTrends: each tag is a point compared with the tag before it.
func TestArtifactKeyTrends(t *testing.T) {
	f := seedTags(t)
	mux := newMux(f.pool, "")
	type point struct {
		ScanID         int64  `json:"scan_id"`
		ArtifactName   string `json:"artifact_name"`
		ArtifactKey    string `json:"artifact_key"`
		PreviousScanID *int64 `json:"previous_scan_id"`
		Fixed          int    `json:"fixed"`
	}
	var tr struct{ Points []point }
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/trends", f.svc), &tr)
	t2, t3 := f.t2, f.t3
	eq(t, "points", tr.Points, []point{
		{ScanID: f.t2, ArtifactName: stageImage + ":2", ArtifactKey: stageImage},
		{ScanID: f.t3, ArtifactName: stageImage + ":3", ArtifactKey: stageImage, PreviousScanID: &t2},
		{ScanID: f.t4, ArtifactName: stageImage + ":4", ArtifactKey: stageImage, PreviousScanID: &t3, Fixed: 1},
		{ScanID: f.repo, ArtifactName: "/builds/x/0/demo/web-frontend", ArtifactKey: "."},
	})
}

// TestArtifactKeySearch: a finding present in every tag is found once, in
// the latest tag.
func TestArtifactKeySearch(t *testing.T) {
	f := seedTags(t)
	mux := newMux(f.pool, "")
	var out struct {
		TotalFindings  int64 `json:"total_findings"`
		TotalArtifacts int64 `json:"total_artifacts"`
		Items          []struct {
			ScanID       int64  `json:"scan_id"`
			ArtifactName string `json:"artifact_name"`
			ArtifactKey  string `json:"artifact_key"`
		}
	}
	getJSON(t, mux, "/api/v1/search/vulnerabilities?id=CVE-2026-1", &out)
	eq(t, "totals", []int64{out.TotalFindings, out.TotalArtifacts}, []int64{1, 1})
	if len(out.Items) != 1 || out.Items[0].ScanID != f.t4 || out.Items[0].ArtifactKey != stageImage ||
		out.Items[0].ArtifactName != stageImage+":4" {
		t.Errorf("items = %+v, want the :4 scan under key %s", out.Items, stageImage)
	}
	getJSON(t, mux, "/api/v1/search/vulnerabilities?id=CVE-2026-1&scope=all", &out)
	eq(t, "totals, every ref", []int64{out.TotalFindings, out.TotalArtifacts}, []int64{1, 1})
}
