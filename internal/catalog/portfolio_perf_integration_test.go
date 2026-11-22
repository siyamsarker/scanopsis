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

// TestPortfolioPerformance measures the portfolio and its history at the
// target scale: 500 services whose three artifacts were scanned four times a
// day for 90 days (540000 scans, several million findings, spread over four
// refs), read by one reader and by twenty at once. The history is measured
// as it is right after an upload, with nothing cached. The p95 of each must
// stay under 150 ms. The history's numbers are checked against the seed's
// formula and a plain query first.
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

	checkHistory(t, c, pool, byName)

	limit := func(what string, got time.Duration, plan func() string) {
		t.Helper()
		if got >= 150*time.Millisecond {
			t.Errorf("%s: p95 %v >= 150ms\n%s", what, got, plan())
		}
	}
	get := timed(t, c, "getPortfolio")
	s95, c95 := p95(repeat(50, get)), p95(together(20, 5, nil, get))
	t.Logf("portfolio, 500 services x 3 artifacts x 360 scans: one reader p95 %v; twenty readers p95 %v", s95, c95)
	limit("portfolio, one reader", s95, func() string { return explainPortfolio(t, pool) })
	limit("portfolio, twenty readers", c95, func() string { return explainPortfolio(t, pool) })

	// The history as it is after every upload: the cache is emptied before
	// each call by one reader, and before each round of twenty readers, who
	// then share one computation. Served from the cache, and computed by
	// twenty readers at once without sharing, it is reported only.
	history := timed(t, c, "getPortfolioHistory")
	forget := func() { c.history = &historyCache{} }
	none := func() string { return "" }
	cold := p95(repeat(30, func() time.Duration { forget(); return history() }))
	shared := p95(together(20, 5, forget, history))
	cached := p95(repeat(50, history))
	alone := p95(together(20, 2, nil, func() time.Duration {
		start := time.Now()
		if _, err := c.loadHistory(context.Background(), time.Now()); err != nil {
			t.Error(err)
		}
		return time.Since(start)
	}))
	t.Logf("history, after an upload: one reader p95 %v; twenty readers p95 %v", cold, shared)
	t.Logf("history, reported only: from the cache p95 %v; twenty readers computing each their own p95 %v", cached, alone)
	limit("history after an upload, one reader", cold, none)
	limit("history after an upload, twenty readers", shared, none)
}

// timed returns a function that calls an operation once and returns how
// long it took.
func timed(t *testing.T, c *Catalog, op string) func() time.Duration {
	return func() time.Duration {
		start := time.Now()
		if rec := call(t, c, op, "GET", "/", "", ""); rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", op, rec.Code)
		}
		return time.Since(start)
	}
}

// repeat runs f once to warm up, then n times, and returns the durations.
func repeat(n int, f func() time.Duration) []time.Duration {
	f()
	out := make([]time.Duration, n)
	for i := range out {
		out[i] = f()
	}
	return out
}

// together runs rounds of f in readers goroutines started at once; before is
// called before each round when not nil. It returns every duration.
func together(readers, rounds int, before func(), f func() time.Duration) []time.Duration {
	f() // warm up
	var out []time.Duration
	var mu sync.Mutex
	for range rounds {
		if before != nil {
			before()
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range readers {
			wg.Go(func() {
				<-start
				d := f()
				mu.Lock()
				out = append(out, d)
				mu.Unlock()
			})
		}
		close(start)
		wg.Wait()
	}
	return out
}

// checkHistory checks the history at volume: the points of services on main
// against the seed's formula, every service's last point against its
// portfolio counts, and the daily portfolio states against a plain query.
func checkHistory(t *testing.T, c *Catalog, pool *pgxpool.Pool, portfolio map[string]pfItem) {
	t.Helper()
	h := decode[historyResponse](t, call(t, c, "getPortfolioHistory", "GET", "/", "", ""), http.StatusOK)
	if len(h.Services) != 500 || len(h.Days) != 7 {
		t.Fatalf("history: %d services, %d days", len(h.Services), len(h.Days))
	}
	names := map[int64]string{}
	for name, it := range portfolio {
		names[it.Service.ID] = name
	}
	for _, s := range h.Services {
		name := names[s.ServiceID]
		last := s.Points[len(s.Points)-1]
		if last.Counts != portfolio[name].Counts {
			t.Errorf("%s: last point %+v, portfolio %+v", name, last.Counts, portfolio[name].Counts)
		}
		if s.Ref != "main" {
			continue
		}
		// After pipeline j on main every artifact was scanned in j: the
		// state is three times the counts of one artifact row.
		if len(s.Points) != 20 {
			t.Errorf("%s: %d points, want 20", name, len(s.Points))
		}
		for _, p := range s.Points {
			j := p.PipelineID
			want := counts{3 * (j % 3), 3 * (j % 5), 3 * (j % 7), 3 * (j % 11), 3 * (j % 2)}
			if j%7 == 0 || p.Counts != want {
				t.Errorf("%s pipeline %d: %+v, want %+v on main", name, j, p.Counts, want)
				break
			}
		}
	}
	for _, d := range h.Days {
		var want counts
		err := pool.QueryRow(context.Background(), `
			WITH svc AS (
			    SELECT s.id, COALESCE(s.default_ref,
			        (SELECT ref FROM scans WHERE service_id = s.id ORDER BY scanned_at DESC, ref LIMIT 1)) AS ref
			    FROM services s WHERE NOT s.archived
			), latest AS (
			    SELECT DISTINCT ON (x.service_id, x.artifact_name, x.kind)
			           x.count_critical, x.count_high, x.count_medium, x.count_low, x.count_unknown
			    FROM scans x JOIN svc ON svc.id = x.service_id AND svc.ref = x.ref
			    WHERE x.scanned_at <= $1
			    ORDER BY x.service_id, x.artifact_name, x.kind, x.scanned_at DESC, x.id DESC
			)
			SELECT COALESCE(sum(count_critical), 0), COALESCE(sum(count_high), 0), COALESCE(sum(count_medium), 0),
			       COALESCE(sum(count_low), 0), COALESCE(sum(count_unknown), 0)
			FROM latest`, d.At).Scan(&want.Critical, &want.High, &want.Medium, &want.Low, &want.Unknown)
		if err != nil {
			t.Fatal(err)
		}
		if d.Counts != want {
			t.Errorf("day %v: %+v, want %+v", d.At, d.Counts, want)
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
