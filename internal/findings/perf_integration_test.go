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
// over 30 calls of the overview, the findings page, a scan diff and three
// searches (an ID in a few services, in a twenty-fifth of them and in three
// quarters of them) must stay under 150 ms.
func TestReadPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds 540000 scans")
	}
	pool := storetest.New(t)
	ctx := context.Background()
	storetest.SeedVolume(t, pool, storetest.DefaultVolume)

	svc := newService(t, pool, "big", "grp/big", false)
	base := time.Now().UTC().Truncate(time.Second)
	var newest int64
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
		}
	}
	insertVulns(t, pool, newest, "CVE-2031-", 1, 300)
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
		{"search, ID in a few services", "/api/v1/search/vulnerabilities?id=" + typical, nil},
		{"search, ID in every 25th service", "/api/v1/search/vulnerabilities?id=" + storetest.CommonVulnID, nil},
		{"search, ID in 3 of 4 services", "/api/v1/search/vulnerabilities?id=" + storetest.WidespreadVulnID, nil},
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

	// The measured calls return what they should.
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
	var found searchBody
	getJSON(t, mux, "/api/v1/search/vulnerabilities?id="+storetest.CommonVulnID, &found)
	// 20 services carry it, in 2 image artifacts on each of 4 refs.
	if len(found.Items) != 160 || found.Truncated {
		t.Errorf("search for %s: %d items, truncated %v; want 160", storetest.CommonVulnID, len(found.Items), found.Truncated)
	}
	// Three quarters of the services carry the widespread ID: the list is cut
	// at 1000 findings, the most severe first, but the totals are the true
	// ones, which a plain join over the stored findings confirms.
	var wantFindings, wantServices int64
	err = pool.QueryRow(ctx, `
		SELECT count(*), count(DISTINCT s.service_id)
		FROM vulnerabilities v JOIN scans s ON s.id = v.scan_id
		WHERE v.vuln_id = $1 AND NOT v.suppressed
		  AND NOT EXISTS (SELECT 1 FROM scans n WHERE n.service_id = s.service_id AND n.ref = s.ref
		                  AND n.artifact_name = s.artifact_name AND n.kind = s.kind
		                  AND (n.scanned_at, n.id) > (s.scanned_at, s.id))`, storetest.WidespreadVulnID).Scan(&wantFindings, &wantServices)
	if err != nil {
		t.Fatal(err)
	}
	getJSON(t, mux, "/api/v1/search/vulnerabilities?id="+storetest.WidespreadVulnID, &found)
	if len(found.Items) != maxSearchResults || !found.Truncated || found.TotalFindings != wantFindings || found.TotalServices != wantServices || wantServices != 375 {
		t.Errorf("search for %s: %d items, truncated %v, totals %d in %d services; want %d items, truncated, %d in %d (375 services)",
			storetest.WidespreadVulnID, len(found.Items), found.Truncated, found.TotalFindings, found.TotalServices,
			maxSearchResults, wantFindings, wantServices)
	}
	getJSON(t, mux, "/api/v1/search/vulnerabilities?id="+typical, &found)
	if len(found.Items) == 0 {
		t.Errorf("search for %s found nothing", typical)
	}
}
