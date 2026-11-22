// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package catalog

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

// p95 returns the 95th percentile of durations.
func p95(d []time.Duration) time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)
	return s[(len(s)*95+99)/100-1]
}

// TestPortfolioPerformance measures the portfolio at the target scale: 500
// services whose three artifacts were scanned four times a day for 90 days
// (540000 scans, several million findings, spread over four refs), read by one
// reader and by twenty at once. The p95 must stay under 150 ms.
func TestPortfolioPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds 540000 scans")
	}
	c, pool, _ := newCatalog(t, "")
	ctx := context.Background()
	storetest.SeedVolume(t, pool, storetest.DefaultVolume)
	// A third of the services have the chosen dashboard branch main, a third
	// feature-1, the rest follow the latest scan, so the measured statement
	// includes every branch of the resolution rule.
	mustExec(t, pool, `UPDATE services SET default_ref = 'main' WHERE id % 3 = 0`)
	mustExec(t, pool, `UPDATE services SET default_ref = 'feature-1' WHERE id % 3 = 1`)
	mustExec(t, pool, `INSERT INTO sonar_analyses (service_id, task_id, analysed_at, quality_gate_status,
		reliability_rating, security_rating, maintainability_rating, coverage)
		SELECT s.id, 't-' || s.id || '-' || k, now() - (k * interval '1 day'),
		       CASE WHEN s.id % 4 = 0 THEN 'ERROR' ELSE 'OK' END, 'A', 'B', 'C', 50 + k
		FROM services s, generate_series(1, 3) k`)
	mustExec(t, pool, `ANALYZE`)

	var scans int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM scans`).Scan(&scans); err != nil || scans != 540000 {
		t.Fatalf("scans = %d (%v)", scans, err)
	}

	// Check the numbers of three services against hand computation. The
	// newest scans of every artifact are j = 360 on main; on feature-1 the
	// newest are j = 357 (a multiple of 21). Each artifact row carries
	// critical j%3, high j%5, medium j%7, low j%11, unknown j%2, fixable j%4,
	// and the secret scan also secrets j%6; three artifacts are summed.
	pf := decode[pfResponse](t, call(t, c, "getPortfolio", "GET", "/", "", ""), http.StatusOK)
	if pf.Totals.Services != 500 || len(pf.Items) != 500 {
		t.Fatalf("services = %d/%d", pf.Totals.Services, len(pf.Items))
	}
	byName := map[string]pfItem{}
	for _, it := range pf.Items {
		byName[it.Service.Name] = it
	}
	for _, tc := range []struct {
		name, ref, source string
		counts            counts
		fixable, secrets  int64
	}{
		{"bg-svc-2", "main", "latest", counts{0, 0, 9, 24, 0}, 0, 0},       // follows the latest scan
		{"bg-svc-3", "main", "default", counts{0, 0, 9, 24, 0}, 0, 0},      // chosen main
		{"bg-svc-4", "feature-1", "default", counts{0, 6, 0, 15, 3}, 3, 3}, // chosen feature-1
	} {
		it := byName[tc.name]
		if str(it.Ref) != tc.ref || it.RefSource != tc.source || it.Counts != tc.counts ||
			it.Fixable != tc.fixable || it.Secrets != tc.secrets || it.QualityGateStatus == nil {
			t.Errorf("%s = ref %s/%s counts %+v fixable %d secrets %d, want %s/%s %+v %d %d",
				tc.name, str(it.Ref), it.RefSource, it.Counts, it.Fixable, it.Secrets,
				tc.ref, tc.source, tc.counts, tc.fixable, tc.secrets)
		}
	}

	get := func() time.Duration {
		start := time.Now()
		if rec := call(t, c, "getPortfolio", "GET", "/", "", ""); rec.Code != http.StatusOK {
			t.Errorf("status %d", rec.Code)
		}
		return time.Since(start)
	}
	get() // warm up
	const runs = 50
	single := make([]time.Duration, runs)
	for i := range single {
		single[i] = get()
	}

	const readers, each = 20, 5
	var wg sync.WaitGroup
	var mu sync.Mutex
	var parallel []time.Duration
	for range readers {
		wg.Go(func() {
			for range each {
				d := get()
				mu.Lock()
				parallel = append(parallel, d)
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	s95, c95 := p95(single), p95(parallel)
	t.Logf("portfolio, 500 services x 3 artifacts x 360 scans: one reader p95 %v; %d readers p95 %v", s95, readers, c95)
	for what, got := range map[string]time.Duration{"one reader": s95, "twenty readers": c95} {
		if got >= 150*time.Millisecond {
			t.Errorf("%s: p95 %v >= 150ms\n%s", what, got, explainPortfolio(t, pool))
		}
	}
}

// explainPortfolio returns the executed plan of the portfolio statement, for
// the failure message.
func explainPortfolio(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	rows, err := pool.Query(context.Background(), "EXPLAIN (ANALYZE, BUFFERS) "+portfolioSQL)
	if err != nil {
		return err.Error()
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err == nil {
			plan.WriteString(line + "\n")
		}
	}
	return plan.String()
}
