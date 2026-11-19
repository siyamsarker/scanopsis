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

// TestReadPerformance seeds one service with 3 artifacts x 50 scans x 2000
// vulnerabilities and checks the p95 of the overview and of the first
// vulnerabilities page over 30 calls each.
func TestReadPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds 300000 rows")
	}
	pool := storetest.New(t)
	ctx := context.Background()
	svc := newService(t, pool, "big", "grp/big", false)
	base := time.Now().UTC().Truncate(time.Second)
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
		}
	}
	if _, err := pool.Exec(ctx, "ANALYZE"); err != nil {
		t.Fatal(err)
	}
	mux := newMux(pool, "")
	for _, p := range []string{
		fmt.Sprintf("/api/v1/services/%d/overview", svc),
		fmt.Sprintf("/api/v1/services/%d/vulnerabilities", svc),
	} {
		get(t, mux, p) // warm up
		var d []time.Duration
		for range 30 {
			start := time.Now()
			if code, body := get(t, mux, p); code != 200 {
				t.Fatalf("%s = %d %s", p, code, body)
			}
			d = append(d, time.Since(start))
		}
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		p95 := d[28]
		t.Logf("%s: p50 %v p95 %v max %v", p, d[14], p95, d[29])
		if p95 > 150*time.Millisecond {
			t.Errorf("%s: p95 %v exceeds 150ms", p, p95)
		}
	}
}
