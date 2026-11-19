// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package retention

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

const day = 24 * time.Hour

// mutableSettings is a settings source whose retention can change between runs.
type mutableSettings struct{ days atomic.Int64 }

func newSettings(days int) *mutableSettings {
	m := &mutableSettings{}
	m.days.Store(int64(days))
	return m
}

func (m *mutableSettings) Current() settings.Snapshot {
	return settings.Snapshot{RetentionDays: int(m.days.Load())}
}

// fixture seeds rows and remembers a label for each, so a test can state the
// exact set of rows that must remain.
type fixture struct {
	t    *testing.T
	pool *pgxpool.Pool
	now  time.Time
	job  int64
	// labels maps a table-qualified id ("scans:12") to a readable name.
	labels map[string]string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return &fixture{
		t: t, pool: storetest.New(t), now: time.Now().UTC().Truncate(time.Second),
		labels: map[string]string{},
	}
}

func (f *fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
}

func (f *fixture) insertID(table, label, sql string, args ...any) int64 {
	f.t.Helper()
	var id int64
	if err := f.pool.QueryRow(context.Background(), sql, args...).Scan(&id); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
	f.labels[fmt.Sprintf("%s:%d", table, id)] = label
	return id
}

func (f *fixture) service(name string) int64 {
	f.t.Helper()
	var id int64
	err := f.pool.QueryRow(context.Background(),
		`INSERT INTO services (name, gitlab_project_path, created_via) VALUES ($1, $1, 'manual') RETURNING id`, name).Scan(&id)
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

// scan inserts a scan scannedAgo old, with one vulnerability and one secret finding.
func (f *fixture) scan(svc int64, ref, artifact, kind string, scannedAgo time.Duration) int64 {
	f.t.Helper()
	f.job++
	label := fmt.Sprintf("%d/%s/%s/%s/%dh", svc, ref, artifact, kind, int(scannedAgo.Hours()))
	id := f.insertID("scans", label, `
		INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected,
		                   commit_sha, schema_version, scanned_at, raw_report)
		VALUES ($1, $2, $3, 1, $4, $5, false, 'abc', 2, $6, '\x00') RETURNING id`,
		svc, kind, artifact, f.job, ref, f.now.Add(-scannedAgo))
	f.exec(`INSERT INTO vulnerabilities (scan_id, target, class, pkg_name, vuln_id, severity)
	        VALUES ($1, 't', 'os-pkgs', 'p', 'CVE-1', 'HIGH')`, id)
	f.exec(`INSERT INTO secret_findings (scan_id, target, rule_id, severity) VALUES ($1, 't', 'r', 'HIGH')`, id)
	return id
}

// scanAt inserts a scan with an exact scanned_at (for ties).
func (f *fixture) scanAt(svc int64, ref, artifact, kind string, at time.Time, label string) int64 {
	f.t.Helper()
	f.job++
	return f.insertID("scans", label, `
		INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected,
		                   commit_sha, schema_version, scanned_at, raw_report)
		VALUES ($1, $2, $3, 1, $4, $5, false, 'abc', 2, $6, '\x00') RETURNING id`,
		svc, kind, artifact, f.job, ref, at)
}

func (f *fixture) analysis(svc int64, ago time.Duration) int64 {
	f.t.Helper()
	n := len(f.labels)
	return f.insertID("sonar_analyses", fmt.Sprintf("analysis/%d/%dh", svc, int(ago.Hours())), `
		INSERT INTO sonar_analyses (service_id, task_id, analysed_at) VALUES ($1, $2, $3) RETURNING id`,
		svc, fmt.Sprintf("task-%d", n), f.now.Add(-ago))
}

func (f *fixture) delivery(ago time.Duration, outcome string) int64 {
	f.t.Helper()
	n := len(f.labels)
	return f.insertID("sonar_deliveries", fmt.Sprintf("delivery/%dh/%s", int(ago.Hours()), outcome), `
		INSERT INTO sonar_deliveries (task_id, project_key, received_at, outcome) VALUES ($1, 'k', $2, $3) RETURNING id`,
		fmt.Sprintf("delivery-%d", n), f.now.Add(-ago), outcome)
}

func (f *fixture) audit(ago time.Duration) int64 {
	f.t.Helper()
	return f.insertID("audit_log", fmt.Sprintf("audit/%dh", int(ago.Hours())), `
		INSERT INTO audit_log (at, actor, action) VALUES ($1, 'a', 'x') RETURNING id`, f.now.Add(-ago))
}

// ownRows leaves out the audit events the job writes about its own runs.
func ownRows(table string) string {
	if table == "audit_log" {
		return " WHERE actor <> '" + AuditActor + "'"
	}
	return ""
}

// remaining lists the labels of every row left in table.
func (f *fixture) remaining(table string) []string {
	f.t.Helper()
	rows, err := f.pool.Query(context.Background(), "SELECT id FROM "+table+ownRows(table))
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			f.t.Fatal(err)
		}
		label, ok := f.labels[fmt.Sprintf("%s:%d", table, id)]
		if !ok {
			label = fmt.Sprintf("unlabelled %s:%d", table, id)
		}
		out = append(out, label)
	}
	slices.Sort(out)
	return out
}

func (f *fixture) count(table string) int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table+ownRows(table)).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func (f *fixture) expect(table string, want ...string) {
	f.t.Helper()
	slices.Sort(want)
	if got := f.remaining(table); !slices.Equal(got, want) {
		f.t.Errorf("%s remaining:\n got %v\nwant %v", table, got, want)
	}
}

func (f *fixture) job1(cfg SettingsSource) *Retention {
	r := New(f.pool, cfg, discard)
	r.now = func() time.Time { return f.now }
	return r
}

func h(d time.Duration) string { return fmt.Sprintf("%dh", int(d.Hours())) }

// seedScans creates the scan scenario for a 30-day retention and returns the
// labels that must remain after one run.
func seedScans(f *fixture) (keep []string, deleted int) {
	a, b := f.service("svc-a"), f.service("svc-b")
	lbl := func(svc int64, ref, art, kind string, ago time.Duration) string {
		return fmt.Sprintf("%d/%s/%s/%s/%s", svc, ref, art, kind, h(ago))
	}
	add := func(svc int64, ref, art, kind string, ago time.Duration, keepIt bool) {
		f.scan(svc, ref, art, kind, ago)
		if keepIt {
			keep = append(keep, lbl(svc, ref, art, kind, ago))
		} else {
			deleted++
		}
	}
	// Old scans are deleted while a newer scan of the same key exists.
	add(a, "main", "web", "image", 100*day, false)
	add(a, "main", "web", "image", 60*day, false)
	add(a, "main", "web", "image", 31*day, false)
	add(a, "main", "web", "image", 29*day, true) // inside the period
	add(a, "main", "web", "image", 1*day, true)
	// The only scan of a key is kept however old.
	add(a, "main", "web", "secret-fs", 200*day, true)
	// The latest of a key is kept even when it is older than the period,
	// and what is older still is deleted.
	add(a, "feature", "web", "image", 100*day, false)
	add(a, "feature", "web", "image", 90*day, true)
	// Another artifact and another service are independent keys.
	add(a, "main", "api", "image", 31*day, false)
	add(a, "main", "api", "image", 29*day, true)
	add(b, "main", "web", "image", 500*day, false)
	add(b, "main", "web", "image", 29*day, true)
	add(b, "release", "web", "image", 400*day, true)
	add(b, "main", "web", "secret-fs", 300*day, false)
	add(b, "main", "web", "secret-fs", 299*day, true)

	// Two scans with the same scanned_at: only the one with the higher id is latest.
	tie := f.now.Add(-50 * day)
	f.scanAt(a, "tie", "web", "image", tie, "tie/low-id")
	f.scanAt(a, "tie", "web", "image", tie, "tie/high-id")
	keep = append(keep, "tie/high-id")
	deleted++
	return keep, deleted
}

func TestRunDeletesExactlyTheExpiredScans(t *testing.T) {
	for _, batch := range []int32{1, 2, 7, BatchSize} {
		t.Run(fmt.Sprintf("batch %d", batch), func(t *testing.T) {
			f := newFixture(t)
			keep, deleted := seedScans(f)
			vulnsBefore := f.count("vulnerabilities")
			if vulnsBefore != 15 {
				t.Fatalf("seeded vulnerabilities = %d", vulnsBefore)
			}
			r := f.job1(newSettings(30))
			r.batch = batch
			res := r.RunOnce(context.Background())
			if res.Skipped || res.FailedSteps != 0 || res.Scans != int64(deleted) {
				t.Fatalf("result = %+v, want %d scans deleted", res, deleted)
			}
			f.expect("scans", keep...)

			// Findings of deleted scans are gone; findings of kept scans remain.
			kept := len(keep) - 1 // the tie scan has no findings
			if got := f.count("vulnerabilities"); got != kept {
				t.Errorf("vulnerabilities = %d, want %d", got, kept)
			}
			if got := f.count("secret_findings"); got != kept {
				t.Errorf("secret_findings = %d, want %d", got, kept)
			}
			var orphans int
			err := f.pool.QueryRow(context.Background(),
				`SELECT (SELECT count(*) FROM vulnerabilities v WHERE NOT EXISTS (SELECT 1 FROM scans s WHERE s.id = v.scan_id))
				      + (SELECT count(*) FROM secret_findings v WHERE NOT EXISTS (SELECT 1 FROM scans s WHERE s.id = v.scan_id))`).Scan(&orphans)
			if err != nil || orphans != 0 {
				t.Errorf("orphaned findings = %d, %v", orphans, err)
			}

			// A second run has nothing left to do.
			if again := r.RunOnce(context.Background()); again.Scans != 0 {
				t.Errorf("second run deleted %d scans", again.Scans)
			}
			f.expect("scans", keep...)
		})
	}
}

func TestRunNeverDeletesWithinThePeriod(t *testing.T) {
	f := newFixture(t)
	svc := f.service("svc")
	var want []string
	for i := 1; i <= 29; i++ {
		f.scan(svc, "main", "web", "image", time.Duration(i)*day-time.Hour)
		want = append(want, fmt.Sprintf("%d/main/web/image/%dh", svc, i*24-1))
	}
	res := f.job1(newSettings(30)).RunOnce(context.Background())
	if res.Scans != 0 {
		t.Fatalf("result = %+v", res)
	}
	f.expect("scans", want...)
}

func TestRunWithMoreThanOneBatch(t *testing.T) {
	f := newFixture(t)
	svc := f.service("svc")
	other := f.service("other")
	const n = 2500
	f.exec(`INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected,
	                           commit_sha, schema_version, scanned_at, raw_report)
	        SELECT $1, 'image', 'web', 1, g, 'main', false, 'abc', 2, $2::timestamptz - interval '100 days' + g * interval '1 second', '\x00'
	        FROM generate_series(1, $3) g`, svc, f.now, n)
	// Old latest-only scans sit between the deletable ones in time and must be skipped, not retried forever.
	for i := range 30 {
		f.exec(`INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected,
		                           commit_sha, schema_version, scanned_at, raw_report)
		        VALUES ($1, 'image', 'web', 1, 100000 + $2, $3, false, 'abc', 2, $4::timestamptz - interval '100 days' + $2 * interval '80 seconds', '\x00')`,
			other, i, fmt.Sprintf("branch-%d", i), f.now)
	}
	f.exec(`INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected,
	                           commit_sha, schema_version, scanned_at, raw_report)
	        VALUES ($1, 'image', 'web', 1, 999999, 'main', false, 'abc', 2, $2, '\x00')`, svc, f.now.Add(-day))

	res := f.job1(newSettings(30)).RunOnce(context.Background())
	if res.Scans != n || res.FailedSteps != 0 {
		t.Fatalf("result = %+v, want %d scans", res, n)
	}
	if got := f.count("scans"); got != 31 {
		t.Fatalf("scans remaining = %d, want 31 (30 latest-only + the newest of main)", got)
	}
}

func TestRunSonarAnalyses(t *testing.T) {
	f := newFixture(t)
	a, b, c := f.service("a"), f.service("b"), f.service("c")
	f.analysis(a, 100*day)
	f.analysis(a, 50*day)
	f.analysis(a, 5*day)
	f.analysis(b, 400*day) // the only one of its service
	f.analysis(c, 300*day)
	f.analysis(c, 200*day)
	f.analysis(c, 29*day)
	// Same analysed_at: the higher id is the latest.
	d := f.service("d")
	tie := f.now.Add(-60 * day)
	f.insertID("sonar_analyses", "tie/low", `INSERT INTO sonar_analyses (service_id, task_id, analysed_at) VALUES ($1, 't1', $2) RETURNING id`, d, tie)
	f.insertID("sonar_analyses", "tie/high", `INSERT INTO sonar_analyses (service_id, task_id, analysed_at) VALUES ($1, 't2', $2) RETURNING id`, d, tie)

	r := f.job1(newSettings(30))
	r.batch = 2
	res := r.RunOnce(context.Background())
	if res.SonarAnalyses != 5 || res.FailedSteps != 0 {
		t.Fatalf("result = %+v, want 5 analyses deleted", res)
	}
	f.expect("sonar_analyses",
		fmt.Sprintf("analysis/%d/%dh", a, 5*24), fmt.Sprintf("analysis/%d/%dh", b, 400*24),
		fmt.Sprintf("analysis/%d/%dh", c, 29*24), "tie/high")
}

func TestRunSonarDeliveries(t *testing.T) {
	f := newFixture(t)
	f.delivery(100*day, "linked")
	f.delivery(31*day, "failed")
	f.delivery(40*day, "pending") // a stuck delivery older than the period goes too
	f.delivery(29*day, "unlinked")
	f.delivery(1*day, "linked")
	r := f.job1(newSettings(30))
	r.batch = 2
	res := r.RunOnce(context.Background())
	if res.SonarDeliveries != 3 {
		t.Fatalf("result = %+v", res)
	}
	f.expect("sonar_deliveries", "delivery/696h/unlinked", "delivery/24h/linked")
}

func TestRunAuditEvents(t *testing.T) {
	t.Run("365 days", func(t *testing.T) {
		f := newFixture(t)
		f.audit(400 * day)
		f.audit(366 * day)
		f.audit(364 * day)
		f.audit(100 * day)
		f.audit(1 * day)
		r := f.job1(newSettings(30)) // the scan period does not shorten audit retention
		r.batch = 2
		res := r.RunOnce(context.Background())
		if res.AuditEvents != 2 {
			t.Fatalf("result = %+v", res)
		}
		f.expect("audit_log", "audit/8736h", "audit/2400h", "audit/24h")
	})
	t.Run("never shorter than the scan retention", func(t *testing.T) {
		f := newFixture(t)
		f.audit(900 * day)
		f.audit(799 * day)
		f.audit(400 * day)
		res := f.job1(newSettings(800)).RunOnce(context.Background())
		if res.AuditEvents != 1 {
			t.Fatalf("result = %+v", res)
		}
		f.expect("audit_log", "audit/19176h", "audit/9600h")
	})
}

func TestRunWithMoreThanOneBatchOfEverything(t *testing.T) {
	f := newFixture(t)
	svc := f.service("svc")
	f.exec(`INSERT INTO sonar_analyses (service_id, task_id, analysed_at)
	        SELECT $1, 'a' || g, $2::timestamptz - interval '100 days' + g * interval '1 second' FROM generate_series(1, 2100) g`, svc, f.now)
	f.exec(`INSERT INTO sonar_deliveries (task_id, project_key, received_at, outcome)
	        SELECT 'd' || g, 'k', $1::timestamptz - interval '100 days' + g * interval '1 second', 'linked' FROM generate_series(1, 2100) g`, f.now)
	f.exec(`INSERT INTO audit_log (at, actor, action)
	        SELECT $1::timestamptz - interval '400 days' + g * interval '1 second', 'a', 'x' FROM generate_series(1, 2100) g`, f.now)
	res := f.job1(newSettings(30)).RunOnce(context.Background())
	if res.SonarAnalyses != 2099 || res.SonarDeliveries != 2100 || res.AuditEvents != 2100 || res.FailedSteps != 0 {
		t.Fatalf("result = %+v", res)
	}
	if f.count("sonar_analyses") != 1 || f.count("sonar_deliveries") != 0 || f.count("audit_log") != 0 {
		t.Fatalf("remaining: analyses %d, deliveries %d, audit %d", f.count("sonar_analyses"), f.count("sonar_deliveries"), f.count("audit_log"))
	}
}

func TestAdvisoryLockStopsASecondRun(t *testing.T) {
	f := newFixture(t)
	svc := f.service("svc")
	f.scan(svc, "main", "web", "image", 100*day)
	f.scan(svc, "main", "web", "image", 1*day)
	f.delivery(100*day, "linked")
	f.audit(400 * day)

	// Another instance holds the lock.
	holder, err := f.pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(context.Background(), "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		t.Fatal(err)
	}
	res := f.job1(newSettings(30)).RunOnce(context.Background())
	if !res.Skipped || res.Scans != 0 {
		t.Fatalf("result = %+v, want skipped", res)
	}
	if f.count("scans") != 2 || f.count("sonar_deliveries") != 1 || f.count("audit_log") != 1 {
		t.Fatal("a run without the lock deleted rows")
	}
	if _, err := holder.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLockKey); err != nil {
		t.Fatal(err)
	}
	holder.Release()

	// The lock is released after a run, so the next one proceeds.
	if res := f.job1(newSettings(30)).RunOnce(context.Background()); res.Skipped || res.Scans != 1 {
		t.Fatalf("result after release = %+v", res)
	}
	if res := f.job1(newSettings(30)).RunOnce(context.Background()); res.Skipped {
		t.Fatalf("the lock was not released by the previous run: %+v", res)
	}
}

func TestConcurrentRunsDeleteEachRowOnce(t *testing.T) {
	f := newFixture(t)
	svc := f.service("svc")
	const n = 3000
	f.exec(`INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected,
	                           commit_sha, schema_version, scanned_at, raw_report)
	        SELECT $1, 'image', 'web', 1, g, 'main', false, 'abc', 2, $2::timestamptz - interval '100 days' + g * interval '1 second', '\x00'
	        FROM generate_series(1, $3) g`, svc, f.now, n)

	var wg sync.WaitGroup
	results := make([]Result, 4)
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = f.job1(newSettings(30)).RunOnce(context.Background())
		}()
	}
	close(start)
	wg.Wait()

	var deleted int64
	for _, r := range results {
		deleted += r.Scans
		if !r.Skipped && r.FailedSteps != 0 {
			t.Errorf("failed run: %+v", r)
		}
	}
	if deleted != n-1 || f.count("scans") != 1 {
		t.Fatalf("deleted %d, remaining %d; want %d and 1 (results %+v)", deleted, f.count("scans"), n-1, results)
	}
}

func TestSettingsChangeAppliesOnTheNextRun(t *testing.T) {
	f := newFixture(t)
	svc := f.service("svc")
	f.scan(svc, "main", "web", "image", 200*day)
	f.scan(svc, "main", "web", "image", 100*day)
	f.scan(svc, "main", "web", "image", 40*day)
	f.scan(svc, "main", "web", "image", 10*day)
	f.scan(svc, "main", "web", "image", 1*day)
	cfg := newSettings(365)
	r := f.job1(cfg)

	if res := r.RunOnce(context.Background()); res.Scans != 0 || res.RetentionDays != 365 {
		t.Fatalf("365 days: %+v", res)
	}
	cfg.days.Store(90)
	if res := r.RunOnce(context.Background()); res.Scans != 2 || res.RetentionDays != 90 {
		t.Fatalf("90 days: %+v", res)
	}
	name := func(d time.Duration) string { return fmt.Sprintf("%d/main/web/image/%dh", svc, int(d.Hours())) }
	f.expect("scans", name(40*day), name(10*day), name(day))
	// Seven is the shortest retention; the newest scan always stays.
	cfg.days.Store(7)
	if res := r.RunOnce(context.Background()); res.Scans != 2 {
		t.Fatalf("7 days: %+v", res)
	}
	f.expect("scans", name(day))
	// A value below the minimum, which settings never produce, is raised to it.
	cfg.days.Store(1)
	if res := r.RunOnce(context.Background()); res.Scans != 0 || res.RetentionDays != 7 {
		t.Fatalf("1 day: %+v", res)
	}
}

func TestRunLogsOneInfoLineWithCounts(t *testing.T) {
	f := newFixture(t)
	svc := f.service("svc")
	f.scan(svc, "main", "web", "image", 100*day)
	f.scan(svc, "main", "web", "image", 1*day)
	f.delivery(100*day, "linked")
	var buf bytes.Buffer
	r := New(f.pool, newSettings(30), slog.New(slog.NewTextHandler(&buf, nil)))
	r.RunOnce(context.Background())
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("log lines = %q", lines)
	}
	for _, want := range []string{"level=INFO", `msg="data retention run finished"`, "retention_days=30", "scans_deleted=1",
		"sonar_analyses_deleted=0", "sonar_deliveries_deleted=1", "audit_events_deleted=0", "failed_steps=0", "duration="} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("log line lacks %q: %s", want, lines[0])
		}
	}
}

func TestFailedStepIsLoggedAndOthersStillRun(t *testing.T) {
	f := newFixture(t)
	svc := f.service("svc")
	f.scan(svc, "main", "web", "image", 100*day)
	f.scan(svc, "main", "web", "image", 1*day)
	f.audit(400 * day)
	f.exec("DROP TABLE sonar_deliveries")
	var buf bytes.Buffer
	r := New(f.pool, newSettings(30), slog.New(slog.NewTextHandler(&buf, nil)))
	res := r.RunOnce(context.Background())
	if res.FailedSteps != 1 || res.Scans != 1 || res.AuditEvents != 1 {
		t.Fatalf("result = %+v", res)
	}
	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "step=\"sonar deliveries\"") || !strings.Contains(out, "failed_steps=1") {
		t.Errorf("log = %s", out)
	}
}

func TestRunWithoutAConnection(t *testing.T) {
	f := newFixture(t)
	var buf bytes.Buffer
	r := New(f.pool, newSettings(30), slog.New(slog.NewTextHandler(&buf, nil)))
	f.pool.Close()
	res := r.RunOnce(context.Background())
	if res.FailedSteps != 1 || !strings.Contains(buf.String(), "level=WARN") {
		t.Fatalf("result = %+v, log %s", res, buf.String())
	}
}

func TestRunSchedulesRepeatedRuns(t *testing.T) {
	f := newFixture(t)
	svc := f.service("svc")
	f.scan(svc, "main", "web", "image", 100*day)
	f.scan(svc, "main", "web", "image", 1*day)
	var buf syncBuffer
	r := New(f.pool, newSettings(30), slog.New(slog.NewTextHandler(&buf, nil)))
	r.firstDelay, r.interval = 10*time.Millisecond, 20*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	deadline := time.Now().Add(20 * time.Second)
	for strings.Count(buf.String(), "data retention run finished") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("fewer than two runs happened; log: %s", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if got := f.count("scans"); got != 1 {
		t.Fatalf("scans remaining = %d, want 1", got)
	}
}

// syncBuffer is a bytes.Buffer safe for a logger and a reader at once.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestRunThatDeletesIsAudited(t *testing.T) {
	f := newFixture(t)
	svc := f.service("payments")
	f.scan(svc, "main", "app", "image", 200*day)
	f.scan(svc, "main", "app", "image", 100*day)
	f.scan(svc, "main", "app", "image", 1*day)
	f.audit(400 * day)
	r := f.job1(newSettings(90))
	if res := r.RunOnce(context.Background()); res.Scans != 2 || res.AuditEvents != 1 || res.FailedSteps != 0 {
		t.Fatalf("result = %+v", res)
	}
	var action string
	var detail map[string]any
	err := f.pool.QueryRow(context.Background(),
		`SELECT action, detail FROM audit_log WHERE actor = $1`, AuditActor).Scan(&action, &detail)
	if err != nil {
		t.Fatal(err)
	}
	if action != "retention.deleted" || detail["scans"] != float64(2) || detail["audit_events"] != float64(1) ||
		detail["retention_days"] != float64(90) || detail["sonar_analyses"] != float64(0) {
		t.Errorf("event = %s %v", action, detail)
	}

	// A run that deletes nothing writes no event.
	if res := r.RunOnce(context.Background()); res.Scans != 0 || res.AuditEvents != 0 {
		t.Fatalf("second run = %+v", res)
	}
	var n int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE actor = $1`, AuditActor).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("retention events = %d, want 1", n)
	}
}
