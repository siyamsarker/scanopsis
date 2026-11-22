// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package storetest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Volume describes the background data SeedVolume inserts: Services services,
// each with three artifacts (two images and one secret scan) scanned
// ScansPerDay times a day for Days days, and VulnsPerScan findings in every
// image scan.
type Volume struct {
	Services         int
	Days             int
	ScansPerDay      int
	VulnsPerScan     int
	Refs             int // refs per service: main plus Refs-1 feature branches
	VulnIDPool       int // distinct vulnerability numbers the findings draw from
	CommonIDEveryNth int // every Nth service carries CommonVulnID in every image scan
}

// CommonVulnID is the identifier carried by every 25th service in the
// default volume, so searches have both a rare and a widespread match.
const CommonVulnID = "CVE-2024-3094"

// DefaultVolume is the shape of the target scale: 500 services over 90 days,
// 540000 scans and several million findings. Findings drift slowly from scan
// to scan, as real findings do, so first-seen lookups have history to walk.
var DefaultVolume = Volume{
	Services: 500, Days: 90, ScansPerDay: 4, VulnsPerScan: 12, Refs: 4, VulnIDPool: 40000, CommonIDEveryNth: 25,
}

// SeedVolume inserts v into a migrated database with set-based statements and
// runs ANALYZE. Service names are "bg-svc-N", so tests can add services of
// their own beside it.
func SeedVolume(t testing.TB, pool *pgxpool.Pool, v Volume) {
	t.Helper()
	start := time.Now()
	if err := Seed(context.Background(), pool, v); err != nil {
		t.Fatalf("storetest: seed volume: %v", err)
	}
	t.Logf("seeded %d services x 3 artifacts x %d scans, %d findings per image scan, in %v",
		v.Services, v.Days*v.ScansPerDay, v.VulnsPerScan, time.Since(start).Round(time.Second))
}

// Seed is SeedVolume without a test, for tools that load a database by hand.
// Every parameter is an integer, so the statements are built with Sprintf.
func Seed(ctx context.Context, pool *pgxpool.Pool, v Volume) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	total := v.Days * v.ScansPerDay
	steps := []string{
		`SET synchronous_commit = off`,
		`SET statement_timeout = 0`,
		fmt.Sprintf(`INSERT INTO services (name, gitlab_project_path, created_via)
		  SELECT 'bg-svc-' || g, 'bg/svc-' || g, 'manual' FROM generate_series(1, %d) g`, v.Services),
		// Scans arrive in time order across all services, as they do in
		// production, so the rows of one service are spread over the table.
		// The pipeline id doubles as the scan's sequence number within its
		// artifact; the newest scan of every artifact is at most an hour old.
		fmt.Sprintf(`INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref,
		                     ref_protected, commit_sha, schema_version, scanned_at,
		                     count_critical, count_high, count_medium, count_low, count_unknown,
		                     count_fixable, count_secrets, raw_report)
		  SELECT s.id, CASE WHEN a = 3 THEN 'secret-fs' ELSE 'image' END, 'artifact' || a, j,
		         s.id * 100000 + a * 10000 + j,
		         CASE WHEN j %% 7 = 0 THEN 'feature-' || ((j / 7) %% %d + 1) ELSE 'main' END,
		         false, 'c' || j, 2,
		         now() - ((%d - j) * interval '1 day' / %d) - ((s.id %% 60 + a) * interval '1 minute'),
		         j %% 3, j %% 5, j %% 7, j %% 11, j %% 2, j %% 4, CASE WHEN a = 3 THEN j %% 6 ELSE 0 END, '\x1f8b'
		  FROM generate_series(1, %d) j
		  CROSS JOIN services s
		  CROSS JOIN generate_series(1, 3) a
		  ORDER BY j, s.id, a`, v.Refs-1, total, v.ScansPerDay, total),
		// Each artifact has a base set of findings; one finding of the set
		// changes every ten scans, so most findings recur across scans.
		fmt.Sprintf(`INSERT INTO vulnerabilities (scan_id, target, class, pkg_type, pkg_name, installed_version,
		                             fixed_version, vuln_id, severity, title, suppressed)
		  SELECT x.id,
		         CASE WHEN g %% 2 = 0 THEN 'debian 12' ELSE 'app/package-lock.json' END,
		         CASE WHEN g %% 2 = 0 THEN 'os-pkgs' ELSE 'lang-pkgs' END,
		         CASE WHEN g %% 2 = 0 THEN 'debian' ELSE 'npm' END,
		         'pkg' || (x.n %% 500), '1.0.' || (x.n %% 7),
		         CASE WHEN x.n %% 3 = 0 THEN '' ELSE '2.0.' || (x.n %% 11) END,
		         CASE WHEN g = 2 AND x.service_id %% %d = 0 THEN '%s'
		              ELSE 'CVE-' || (2018 + x.n %% 7) || '-' || x.n END,
		         (ARRAY['CRITICAL','HIGH','MEDIUM','LOW','UNKNOWN'])[1 + x.n %% 5],
		         'title ' || x.n, x.n %% 50 = 0
		  FROM (SELECT s.id, s.service_id, g,
		               (s.service_id * 31 + (CASE WHEN s.artifact_name = 'artifact1' THEN 1 ELSE 2 END) * 17
		                + g * 7 + CASE WHEN g = 1 THEN s.gitlab_pipeline_id / 10 ELSE 0 END) %% %d AS n
		        FROM scans s CROSS JOIN generate_series(1, %d) g
		        WHERE s.kind = 'image') x
		  ORDER BY x.id, g`, v.CommonIDEveryNth, CommonVulnID, v.VulnIDPool, v.VulnsPerScan),
		`ANALYZE`,
	}
	for _, q := range steps {
		if _, err := conn.Exec(ctx, q); err != nil {
			return fmt.Errorf("%w in %.60s", err, q)
		}
	}
	return nil
}
