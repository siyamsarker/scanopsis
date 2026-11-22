// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package findings

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

// TestReadPerformance checks the read API at the target scale. The
// background is the 90-day volume of 500 services (540000 scans, about four
// million findings, see storetest.SeedVolume). On top of it one service has
// the heavy shape that makes first-seen lookups expensive: 3 artifacts x 50
// scans x 2000 findings, and a newest scan with 300 new findings. The p95
// over 30 calls of the overview, the findings page, a scan diff with the
// previous and with the oldest scan, the list of earlier scans, the 90-day
// trends of a background service (its dashboard branch, and every branch)
// and three searches in the default scope, the dashboard branches and
// protected refs (an ID in a few services, in a twenty-fifth of them and in
// three quarters of them), must stay under 150 ms. Searches of every ref and
// the trends of the heavy service, which compare 300000 findings, are
// measured and reported; every search's totals are checked against a plain
// join, and the trends' new and fixed counts against the scan diffs.
func TestReadPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds 540000 scans")
	}
	pool := storetest.New(t)
	ctx := context.Background()
	storetest.SeedVolume(t, pool, storetest.DefaultVolume)

	svc := newService(t, pool, "big", "grp/big", false)
	base := time.Now().UTC().Truncate(time.Second)
	var newest, oldest int64
	for a := range 3 {
		for n := range 50 {
			scan := newScan(t, pool, svc, "image", fmt.Sprintf("img%d", a), "main", base.Add(-time.Duration(50-n)*time.Hour))
			// The set drifts by one vulnerability every ten scans.
			_, err := pool.Exec(ctx, `
				INSERT INTO vulnerabilities (scan_id, target, class, pkg_name, installed_version, fixed_version,
				                             vuln_id, severity, title)
				SELECT $1, 'debian 12', CASE WHEN g % 2 = 0 THEN 'os-pkgs' ELSE 'lang-pkgs' END,
				       'pkg' || (g % 200), '1.0.' || (g % 7),
				       CASE WHEN g % 3 = 0 THEN '' ELSE '2.0.' || (g % 11) END,
				       'CVE-2024-' || (g + $2::int),
				       (ARRAY['CRITICAL','HIGH','MEDIUM','LOW','UNKNOWN'])[1 + g % 5], 'title ' || g
				FROM generate_series(1, 2000) g`, scan, n/10)
			if err != nil {
				t.Fatal(err)
			}
			refresh(t, pool, scan)
			if a == 0 && n == 49 {
				newest = scan
			}
			if a == 0 && n == 0 {
				oldest = scan
			}
		}
	}
	insertVulns(t, pool, newest, "CVE-2031-", 1, 300)
	// Give the background the two things the default scope depends on: one
	// service in ten has its main branch protected, and one in seven has a
	// chosen dashboard branch that is not the ref of its latest scan.
	for _, q := range []string{
		`UPDATE scans SET ref_protected = true WHERE ref = 'main' AND service_id % 10 = 0 AND service_id <= 500`,
		`UPDATE services SET default_ref = 'main' WHERE id % 7 = 0 AND id <= 500`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, "VACUUM (ANALYZE)"); err != nil {
		t.Fatal(err)
	}

	var typical string
	err := pool.QueryRow(ctx, `
		SELECT v.vuln_id FROM vulnerabilities v JOIN scans s ON s.id = v.scan_id
		WHERE s.service_id = 7 AND v.vuln_id <> $1 ORDER BY v.id DESC LIMIT 1`, storetest.CommonVulnID).Scan(&typical)
	if err != nil {
		t.Fatal(err)
	}

	mux := newMux(pool, "")
	cases := []struct {
		name, path string
		check      func(body []byte)
	}{
		{"overview", fmt.Sprintf("/api/v1/services/%d/overview", svc), nil},
		{"findings page", fmt.Sprintf("/api/v1/services/%d/vulnerabilities", svc), nil},
		{"scan diff", fmt.Sprintf("/api/v1/scans/%d/diff", newest), nil},
		{"scan diff with the oldest scan as base", fmt.Sprintf("/api/v1/scans/%d/diff?base=%d", newest, oldest), nil},
		{"earlier scans", fmt.Sprintf("/api/v1/scans/%d/earlier", newest), nil},
		{"search, ID in a few services", "/api/v1/search/vulnerabilities?id=" + typical, nil},
		{"search, ID in every 25th service", "/api/v1/search/vulnerabilities?id=" + storetest.CommonVulnID, nil},
		{"search, ID in 3 of 4 services", "/api/v1/search/vulnerabilities?id=" + storetest.WidespreadVulnID, nil},
		{"trends, 90 days", "/api/v1/services/7/trends?days=90", nil},
		{"trends, 90 days, every branch", "/api/v1/services/7/trends?days=90&all_refs=true", nil},
	}
	// Every ref of every service is searched only on request. It reads every
	// current scan, so it is measured and reported but has no limit.
	allRefs := []struct{ name, path string }{
		{"search, every ref, ID in every 25th service", "/api/v1/search/vulnerabilities?scope=all&id=" + storetest.CommonVulnID},
		{"search, every ref, ID in 3 of 4 services", "/api/v1/search/vulnerabilities?scope=all&id=" + storetest.WidespreadVulnID},
		{"trends of the heavy service, 150 scans of 2000 findings", fmt.Sprintf("/api/v1/services/%d/trends?days=90", svc)},
	}
	for _, c := range allRefs {
		get(t, mux, c.path)
		var d []time.Duration
		for range 10 {
			start := time.Now()
			if code, body := get(t, mux, c.path); code != 200 {
				t.Fatalf("%s: %d %s", c.name, code, body)
			}
			d = append(d, time.Since(start))
		}
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		t.Logf("%s: p50 %v max %v (reported, not limited)", c.name, d[5], d[9])
	}
	for _, c := range cases {
		get(t, mux, c.path) // warm up
		var d []time.Duration
		for range 30 {
			start := time.Now()
			if code, body := get(t, mux, c.path); code != 200 {
				t.Fatalf("%s: %s = %d %s", c.name, c.path, code, body)
			}
			d = append(d, time.Since(start))
		}
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		p95 := d[28]
		t.Logf("%s: p50 %v p95 %v max %v", c.name, d[14], p95, d[29])
		if p95 > 150*time.Millisecond {
			t.Errorf("%s: p95 %v exceeds 150ms", c.name, p95)
		}
	}

	// The measured calls return what they should. The trends of the
	// background service list the newest 300 of the 900 or more scans of
	// main in 90 days, say how many there are, and carry the new and fixed
	// counts of each scan's diff (checked for a sample).
	var tr trendsJSON
	getJSON(t, mux, "/api/v1/services/7/trends?days=90", &tr)
	var inWindow int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM scans WHERE service_id = 7 AND ref = 'main'
		AND scanned_at >= now() - interval '90 days'`).Scan(&inWindow); err != nil {
		t.Fatal(err)
	}
	if len(tr.Points) != maxTrendScans || tr.Total != inWindow || !tr.Truncated || inWindow < 900 {
		t.Errorf("trends: %d points of %d, truncated %v; want %d of %d (900 or more), truncated",
			len(tr.Points), tr.Total, tr.Truncated, maxTrendScans, inWindow)
	}
	changed := 0
	for i := 0; i < len(tr.Points); i += 7 {
		p := tr.Points[i]
		var d scanDiffJSON
		getJSON(t, mux, fmt.Sprintf("/api/v1/scans/%d/diff", p.ScanID), &d)
		n := p.New.Critical + p.New.High + p.New.Medium + p.New.Low + p.New.Unknown
		if n != d.NewTotal || p.Fixed != d.FixedTotal {
			t.Errorf("trend point %d: new %d fixed %d, diff new %d fixed %d", p.ScanID, n, p.Fixed, d.NewTotal, d.FixedTotal)
		}
		if n+p.Fixed > 0 {
			changed++
		}
	}
	if changed == 0 {
		t.Error("trends: no sampled point changed anything; the check proves nothing")
	}
	var diff struct {
		NewTotal int `json:"new_total"`
		New      []struct {
			FirstSeenAt time.Time `json:"first_seen_at"`
		} `json:"new"`
	}
	getJSON(t, mux, fmt.Sprintf("/api/v1/scans/%d/diff", newest), &diff)
	if diff.NewTotal < 300 || len(diff.New) != diff.NewTotal {
		t.Errorf("diff: %d new listed of %d, want 300 or more, all listed", len(diff.New), diff.NewTotal)
	}
	// The totals are the true ones for the scope, which a plain join over the
	// stored findings confirms: the latest scan of each (service, ref,
	// artifact, kind), restricted for the default scope to the ref the
	// dashboards show and to refs whose latest scan was protected.
	expected := func(id string, all bool) (findings, services int64) {
		t.Helper()
		err := pool.QueryRow(ctx, `
			SELECT count(*), count(DISTINCT s.service_id)
			FROM vulnerabilities v
			JOIN scans s ON s.id = v.scan_id
			JOIN services sv ON sv.id = s.service_id
			WHERE v.vuln_id = $1 AND NOT v.suppressed AND NOT sv.archived
			  AND NOT EXISTS (SELECT 1 FROM scans n WHERE n.service_id = s.service_id AND n.ref = s.ref
			                  AND n.artifact_name = s.artifact_name AND n.kind = s.kind
			                  AND (n.scanned_at, n.id) > (s.scanned_at, s.id))
			  AND ($2 OR s.ref_protected
			       OR s.ref = COALESCE(sv.default_ref,
			            (SELECT l.ref FROM scans l WHERE l.service_id = sv.id ORDER BY l.scanned_at DESC, l.ref LIMIT 1)))`,
			id, all).Scan(&findings, &services)
		if err != nil {
			t.Fatal(err)
		}
		return findings, services
	}
	var found searchBody
	for _, id := range []string{storetest.CommonVulnID, storetest.WidespreadVulnID} {
		for _, scope := range []string{"dashboard", "all"} {
			wantF, wantS := expected(id, scope == "all")
			getJSON(t, mux, "/api/v1/search/vulnerabilities?scope="+scope+"&id="+id, &found)
			wantItems := int(min(wantF, maxSearchResults))
			if len(found.Items) != wantItems || found.Truncated != (wantF > maxSearchResults) ||
				found.TotalFindings != wantF || found.TotalServices != wantS || found.Scope != scope {
				t.Errorf("search for %s, scope %s: %d items, truncated %v, totals %d in %d services, scope %q; want %d items, totals %d in %d",
					id, scope, len(found.Items), found.Truncated, found.TotalFindings, found.TotalServices, found.Scope,
					wantItems, wantF, wantS)
			}
			t.Logf("search for %s, scope %s: %d findings in %d services, %d listed", id, scope, wantF, wantS, len(found.Items))
		}
	}
	// The default scope is the smaller one, and the widespread ID still
	// overflows the list in scope all, so the cut is exercised.
	dashF, _ := expected(storetest.WidespreadVulnID, false)
	allF, allS := expected(storetest.WidespreadVulnID, true)
	if dashF >= allF || allF <= maxSearchResults || allS != 375 {
		t.Errorf("widespread ID: %d findings by default, %d in %d services in all; want fewer by default and more than %d in 375 services",
			dashF, allF, allS, maxSearchResults)
	}
	// The summary covers every match, also beyond the listed ones.
	var sum searchJSON
	getJSON(t, mux, "/api/v1/search/vulnerabilities?scope=all&id="+storetest.WidespreadVulnID, &sum)
	c := sum.Counts
	if total := int64(c.Critical + c.High + c.Medium + c.Low + c.Unknown); total != allF || sum.TotalArtifacts < sum.TotalServices {
		t.Errorf("summary: %d findings by severity, %d artifacts in %d services; want %d findings", total,
			sum.TotalArtifacts, sum.TotalServices, allF)
	}
	// Earlier scans: the other 49 scans of the artifact, newest first.
	var earlier scanPageJSON
	getJSON(t, mux, fmt.Sprintf("/api/v1/scans/%d/earlier?limit=200", newest), &earlier)
	if len(earlier.Items) != 49 || earlier.Items[48].ID != oldest {
		t.Errorf("earlier scans: %d listed, want 49 ending with %d", len(earlier.Items), oldest)
	}
	getJSON(t, mux, "/api/v1/search/vulnerabilities?id="+typical, &found)
	if len(found.Items) == 0 {
		t.Errorf("search for %s found nothing", typical)
	}
}
