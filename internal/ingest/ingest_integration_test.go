// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

type iEnv struct {
	t    *testing.T
	pool *pgxpool.Pool
	g    *gitlab
	cfg  *fakeSettings
	in   *Ingest
	logs *syncBuffer
}

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

func newIEnv(t *testing.T, mutate func(*Options)) *iEnv {
	t.Helper()
	pool := storetest.New(t)
	e := &iEnv{t: t, pool: pool, g: newGitLab(t), logs: &syncBuffer{}}
	e.cfg = &fakeSettings{snap: settings.Snapshot{GitLabURL: e.g.url}}
	log := slog.New(slog.NewTextHandler(e.logs, nil))
	opts := Options{
		Audience: testAudience, Logger: log,
		ClientIP: func(*http.Request) netip.Addr { return netip.MustParseAddr("192.0.2.10") },
	}
	if mutate != nil {
		mutate(&opts)
	}
	e.in = New(pool, e.cfg, audit.New(store.New(pool), log), opts)
	return e
}

type upload struct {
	body   []byte
	gzip   bool
	query  string
	claims func(map[string]any)
}

func (e *iEnv) upload(u upload) (int, []byte) {
	e.t.Helper()
	hr := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/ingest/trivy"+u.query, bytes.NewReader(u.body))
	hr.Header.Set("Authorization", "Bearer "+e.g.token(e.t, u.claims))
	hr.Header.Set("Content-Type", "application/json")
	if u.gzip {
		hr.Header.Set("Content-Encoding", "gzip")
	}
	w := httptest.NewRecorder()
	e.in.Handlers()["ingestTrivy"].ServeHTTP(w, hr)
	return w.Code, w.Body.Bytes()
}

func (e *iEnv) mustUpload(u upload) Result {
	e.t.Helper()
	code, body := e.upload(u)
	if code != http.StatusCreated {
		e.t.Fatalf("upload: %d %s", code, body)
	}
	var r Result
	if err := json.Unmarshal(body, &r); err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *iEnv) count(table string) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// expectedRows walks the report JSON without Parse and lists what must be
// stored: one line per finding.
func expectedRows(t testing.TB, raw []byte) (vulns, secrets []string) {
	t.Helper()
	var doc struct{ Results []map[string]any }
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	s := func(m map[string]any, k string) string { v, _ := m[k].(string); return v }
	vuln := func(res, f map[string]any, supp bool, why string) string {
		return strings.Join([]string{s(res, "Target"), s(res, "Class"), s(res, "Type"), s(f, "PkgName"), s(f, "PkgPath"),
			s(f, "InstalledVersion"), s(f, "FixedVersion"), s(f, "VulnerabilityID"), s(f, "Severity"), s(f, "Status"),
			s(f, "Title"), s(f, "PrimaryURL"), fmt.Sprint(supp), why}, "|")
	}
	num := func(f map[string]any, k string) string { v, _ := f[k].(float64); return fmt.Sprint(int(v)) }
	secret := func(res, f map[string]any, supp bool) string {
		return strings.Join([]string{s(res, "Target"), s(f, "RuleID"), s(f, "Category"), s(f, "Severity"), s(f, "Title"),
			num(f, "StartLine"), num(f, "EndLine"), fmt.Sprint(supp)}, "|")
	}
	for _, res := range doc.Results {
		for _, x := range list(res, "Vulnerabilities") {
			vulns = append(vulns, vuln(res, x.(map[string]any), false, ""))
		}
		for _, x := range list(res, "Secrets") {
			secrets = append(secrets, secret(res, x.(map[string]any), false))
		}
		for _, x := range list(res, "ExperimentalModifiedFindings") {
			m := x.(map[string]any)
			f, _ := m["Finding"].(map[string]any)
			why := s(m, "Status")
			if st := s(m, "Statement"); st != "" {
				why += ": " + st
			}
			if src := s(m, "Source"); src != "" {
				why += " (" + src + ")"
			}
			switch s(m, "Type") {
			case "vulnerability":
				vulns = append(vulns, vuln(res, f, true, why))
			case "secret":
				secrets = append(secrets, secret(res, f, true))
			}
		}
	}
	sort.Strings(vulns)
	sort.Strings(secrets)
	return vulns, secrets
}

func list(m map[string]any, k string) []any { l, _ := m[k].([]any); return l }

func (e *iEnv) storedRows(scanID int64) (vulns, secrets []string) {
	e.t.Helper()
	ctx := context.Background()
	rows, err := e.pool.Query(ctx, `SELECT target, class, pkg_type, pkg_name, pkg_path, installed_version, fixed_version,
		vuln_id, severity, status, title, primary_url, suppressed, suppression FROM vulnerabilities WHERE scan_id = $1`, scanID)
	if err != nil {
		e.t.Fatal(err)
	}
	for rows.Next() {
		var f [12]string
		var supp bool
		var why string
		if err := rows.Scan(&f[0], &f[1], &f[2], &f[3], &f[4], &f[5], &f[6], &f[7], &f[8], &f[9], &f[10], &f[11], &supp, &why); err != nil {
			e.t.Fatal(err)
		}
		vulns = append(vulns, strings.Join(f[:], "|")+"|"+fmt.Sprint(supp)+"|"+why)
	}
	rows.Close()
	rows, err = e.pool.Query(ctx, `SELECT target, rule_id, category, severity, title, start_line, end_line, suppressed
		FROM secret_findings WHERE scan_id = $1`, scanID)
	if err != nil {
		e.t.Fatal(err)
	}
	for rows.Next() {
		var a, b, c, d, f string
		var l1, l2 int
		var supp bool
		if err := rows.Scan(&a, &b, &c, &d, &f, &l1, &l2, &supp); err != nil {
			e.t.Fatal(err)
		}
		secrets = append(secrets, strings.Join([]string{a, b, c, d, f, fmt.Sprint(l1), fmt.Sprint(l2), fmt.Sprint(supp)}, "|"))
	}
	rows.Close()
	sort.Strings(vulns)
	sort.Strings(secrets)
	return vulns, secrets
}

func TestIntegrationUploadMatchesFixture(t *testing.T) {
	tests := []struct {
		file, kind string
		want       Result
	}{
		{"image-full.json", "image", Result{Counts: SeverityCounts{Critical: 2, High: 44, Medium: 58, Low: 31, Unknown: 3}, Fixable: 66}},
		{"image-suppressed.json", "image", Result{Counts: SeverityCounts{Critical: 1, High: 43, Medium: 57, Low: 31, Unknown: 3}, Fixable: 64, Suppressed: 3}},
		{"secret-fs.json", "secret-fs", Result{Secrets: 2}},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			e := newIEnv(t, nil)
			body := readFixture(t, tc.file)
			res := e.mustUpload(upload{body: body, query: "?kind=" + tc.kind})
			want := tc.want
			want.ScanID, want.ServiceID, want.ServiceCreated = res.ScanID, res.ServiceID, true
			if res != want {
				t.Fatalf("result = %+v, want %+v", res, want)
			}

			var (
				kind, artifact, ref, sha, trivyVer string
				pipeline, job                      int64
				protected                          bool
				schema                             int
				scanned                            time.Time
				c                                  [8]int
				stored                             []byte
				received                           time.Time
			)
			err := e.pool.QueryRow(context.Background(), `SELECT kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected,
				commit_sha, trivy_version, schema_version, scanned_at, received_at, count_critical, count_high, count_medium, count_low,
				count_unknown, count_fixable, count_secrets, count_suppressed, raw_report FROM scans WHERE id = $1`, res.ScanID).Scan(
				&kind, &artifact, &pipeline, &job, &ref, &protected, &sha, &trivyVer, &schema, &scanned, &received,
				&c[0], &c[1], &c[2], &c[3], &c[4], &c[5], &c[6], &c[7], &stored)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				ArtifactName, CreatedAt string
			}
			_ = json.Unmarshal(body, &doc)
			created, _ := time.Parse(time.RFC3339Nano, doc.CreatedAt)
			if kind != tc.kind || artifact != doc.ArtifactName || pipeline != 1001 || job != 2002 || ref != "main" || !protected ||
				sha != "0123456789abcdef0123456789abcdef01234567" || trivyVer != "0.76.2" || schema != 2 || !scanned.Equal(created) {
				t.Errorf("scan row: %s %s %d %d %s %v %s %s %d %v", kind, artifact, pipeline, job, ref, protected, sha, trivyVer, schema, scanned)
			}
			if time.Since(received) > time.Minute {
				t.Errorf("received_at = %v", received)
			}
			wantCounts := [8]int{int(tc.want.Counts.Critical), int(tc.want.Counts.High), int(tc.want.Counts.Medium), int(tc.want.Counts.Low),
				int(tc.want.Counts.Unknown), int(tc.want.Fixable), int(tc.want.Secrets), int(tc.want.Suppressed)}
			if c != wantCounts {
				t.Errorf("counts = %v, want %v", c, wantCounts)
			}
			if !bytes.Equal(gunzip(t, stored), body) {
				t.Error("stored report does not decompress to the upload")
			}

			wantV, wantS := expectedRows(t, body)
			gotV, gotS := e.storedRows(res.ScanID)
			if strings.Join(gotV, "\n") != strings.Join(wantV, "\n") {
				t.Errorf("vulnerability rows differ: %d stored, %d expected", len(gotV), len(wantV))
			}
			if strings.Join(gotS, "\n") != strings.Join(wantS, "\n") {
				t.Errorf("secret rows differ: %v vs %v", gotS, wantS)
			}

			// Counts on the scan row equal what the rows say.
			var n int
			err = e.pool.QueryRow(context.Background(), `SELECT
				(SELECT count(*) FROM vulnerabilities WHERE scan_id=$1 AND NOT suppressed AND severity='CRITICAL') +
				(SELECT count(*) FROM vulnerabilities WHERE scan_id=$1 AND NOT suppressed AND severity='HIGH')`, res.ScanID).Scan(&n)
			if err != nil || n != c[0]+c[1] {
				t.Errorf("critical+high rows = %d, counts %d (%v)", n, c[0]+c[1], err)
			}

			var svcName, path, via string
			var pid *int64
			err = e.pool.QueryRow(context.Background(), `SELECT name, gitlab_project_path, gitlab_project_id, created_via FROM services WHERE id=$1`,
				res.ServiceID).Scan(&svcName, &path, &pid, &via)
			if err != nil || svcName != "payments-api" || path != "platform/payments-api" || pid == nil || *pid != 42 || via != "ingest" {
				t.Errorf("service = %s %s %v %s (%v)", svcName, path, pid, via, err)
			}
		})
	}
}

func TestIntegrationIdempotentReplace(t *testing.T) {
	e := newIEnv(t, nil)
	full := readFixture(t, "image-full.json")
	sup := readFixture(t, "image-suppressed.json")

	first := e.mustUpload(upload{body: full})
	if first.Replaced || !first.ServiceCreated {
		t.Fatalf("first = %+v", first)
	}
	second := e.mustUpload(upload{body: sup})
	if !second.Replaced || second.ServiceCreated || second.ServiceID != first.ServiceID || second.ScanID == first.ScanID {
		t.Fatalf("second = %+v", second)
	}
	if e.count("scans") != 1 || e.count("services") != 1 || e.count("vulnerabilities") != 138 {
		t.Fatalf("scans %d services %d vulnerabilities %d", e.count("scans"), e.count("services"), e.count("vulnerabilities"))
	}
	var crit int
	_ = e.pool.QueryRow(context.Background(), `SELECT count_critical FROM scans`).Scan(&crit)
	if crit != 1 {
		t.Errorf("the second report must win: critical = %d", crit)
	}

	// The same report again, byte for byte, gives the same stored data.
	third := e.mustUpload(upload{body: sup})
	if !third.Replaced || e.count("scans") != 1 || e.count("vulnerabilities") != 138 {
		t.Fatalf("third = %+v scans %d", third, e.count("scans"))
	}

	// A different job, artifact or kind is a different scan.
	e.mustUpload(upload{body: sup, claims: func(c map[string]any) { c["job_id"] = "2003" }})
	e.mustUpload(upload{body: []byte(`{"SchemaVersion":2,"ArtifactName":"registry.example.com/team/other:1"}`)})
	e.mustUpload(upload{body: sup, query: "?kind=secret-fs"})
	if e.count("scans") != 4 {
		t.Errorf("scans = %d, want 4", e.count("scans"))
	}
	// A scan with no findings stores no rows.
	var n int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM vulnerabilities v JOIN scans s ON s.id=v.scan_id WHERE s.artifact_name='registry.example.com/team/other:1'`).Scan(&n)
	if n != 0 {
		t.Errorf("rows for an empty scan: %d", n)
	}
}

func TestIntegrationManualServiceAdopted(t *testing.T) {
	e := newIEnv(t, nil)
	var id int64
	err := e.pool.QueryRow(context.Background(), `INSERT INTO services (name, gitlab_project_path, created_via)
		VALUES ('Payments', 'platform/payments-api', 'manual') RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	res := e.mustUpload(upload{body: readFixture(t, "secret-fs.json"), query: "?kind=secret-fs"})
	if res.ServiceID != id || res.ServiceCreated {
		t.Fatalf("result = %+v, want service %d adopted", res, id)
	}
	var name, via string
	var pid int64
	_ = e.pool.QueryRow(context.Background(), `SELECT name, gitlab_project_id, created_via FROM services WHERE id=$1`, id).Scan(&name, &pid, &via)
	if name != "Payments" || pid != 42 || via != "manual" || e.count("services") != 1 {
		t.Errorf("service = %s %d %s, %d services", name, pid, via, e.count("services"))
	}
	// The adoption is audited, in the same transaction.
	var actor, detail string
	if err := e.pool.QueryRow(context.Background(), `SELECT actor, detail::text FROM audit_log WHERE action='service.updated'`).Scan(&actor, &detail); err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	_ = json.Unmarshal([]byte(detail), &d)
	want := map[string]any{"id": float64(id), "gitlab_project_path": "platform/payments-api", "fields": []any{"gitlab_project_id"},
		"gitlab_project_id": 42.0, "reason": "adopted by the first verified upload"}
	if actor != "ci:platform/payments-api" || !jsonEqual(d, want) {
		t.Errorf("audit = %s %s", actor, detail)
	}
	// A later upload is not an adoption: no second event.
	e.mustUpload(upload{body: []byte(`{"SchemaVersion":2,"ArtifactName":"b"}`)})
	var n int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action='service.updated'`).Scan(&n)
	if n != 1 {
		t.Errorf("%d service.updated events", n)
	}
}

func TestIntegrationRename(t *testing.T) {
	e := newIEnv(t, nil)
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	first := e.mustUpload(upload{body: body})
	rename := func(path string) { // the project moved
		e.mustUpload(upload{body: body, claims: func(c map[string]any) {
			c["project_path"], c["namespace_path"] = path, path[:strings.LastIndex(path, "/")]
		}})
	}
	pathOf := func() string {
		var p string
		_ = e.pool.QueryRow(context.Background(), `SELECT gitlab_project_path FROM services WHERE id=$1`, first.ServiceID).Scan(&p)
		return p
	}
	rename("platform/payments-v2")
	if got := pathOf(); got != "platform/payments-v2" || e.count("services") != 1 {
		t.Fatalf("path = %q, services %d", got, e.count("services"))
	}

	// Another service holds the new path: the old path is kept, the upload
	// still works and the warning is logged.
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO services (name, gitlab_project_path, gitlab_project_id, created_via)
		VALUES ('x', 'platform/taken', 99, 'ingest')`); err != nil {
		t.Fatal(err)
	}
	rename("platform/taken")
	if got := pathOf(); got != "platform/payments-v2" {
		t.Fatalf("path = %q, want the old path kept", got)
	}
	if !strings.Contains(e.logs.String(), "project path not updated") {
		t.Errorf("no warning logged: %s", e.logs)
	}
}

func TestIntegrationPathTakenByOtherProject(t *testing.T) {
	e := newIEnv(t, nil)
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO services (name, gitlab_project_path, gitlab_project_id, created_via)
		VALUES ('old', 'platform/payments-api', 7, 'ingest')`); err != nil {
		t.Fatal(err)
	}
	code, body := e.upload(upload{body: []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)})
	if code != http.StatusConflict || !strings.Contains(string(body), "service_conflict") {
		t.Fatalf("%d %s", code, body)
	}
	if e.count("scans") != 0 || e.count("services") != 1 {
		t.Error("a rejected upload left rows behind")
	}
	var detail string
	err := e.pool.QueryRow(context.Background(), `SELECT detail::text FROM audit_log WHERE action='ingest.rejected'`).Scan(&detail)
	if err != nil || !strings.Contains(detail, "service_conflict") {
		t.Errorf("audit: %s %v", detail, err)
	}
}

func TestIntegrationPolicyAndAudit(t *testing.T) {
	e := newIEnv(t, nil)
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)

	e.cfg.set(func(s *settings.Snapshot) { s.IngestAllowedNamespaces = []string{"security"} })
	if code, _ := e.upload(upload{body: body}); code != http.StatusForbidden {
		t.Fatalf("namespace: %d", code)
	}
	e.cfg.set(func(s *settings.Snapshot) { s.IngestAllowedNamespaces = []string{"platform"} })
	if code, b := e.upload(upload{body: body, claims: func(c map[string]any) {
		c["namespace_path"], c["project_path"], c["project_id"] = "platform/team-a", "platform/team-a/svc", "43"
	}}); code != http.StatusCreated {
		t.Fatalf("subgroup of an allowed group: %d %s", code, b)
	}
	if code, _ := e.upload(upload{body: body, claims: func(c map[string]any) {
		c["namespace_path"], c["project_path"] = "platformx", "platformx/svc"
	}}); code != http.StatusForbidden {
		t.Fatalf("sibling with a common prefix: %d", code)
	}

	e.cfg.set(func(s *settings.Snapshot) { s.IngestAllowedNamespaces = nil; s.IngestProtectedRefsOnly = true })
	if code, _ := e.upload(upload{body: body, claims: func(c map[string]any) { c["ref_protected"] = "false" }}); code != http.StatusForbidden {
		t.Fatalf("unprotected ref: %d", code)
	}
	if code, b := e.upload(upload{body: body}); code != http.StatusCreated {
		t.Fatalf("protected ref: %d %s", code, b)
	}
	if e.count("scans") != 2 {
		t.Errorf("scans = %d, want 2", e.count("scans"))
	}

	rows, err := e.pool.Query(context.Background(), `SELECT actor, detail->>'reason', detail->>'project_path', detail->>'ip', detail::text FROM audit_log
		WHERE action = 'ingest.rejected' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var actor, reason, ip, detail string
		var project *string
		if err := rows.Scan(&actor, &reason, &project, &ip, &detail); err != nil {
			t.Fatal(err)
		}
		p := ""
		if project != nil {
			p = *project
		}
		got = append(got, actor+" "+reason+" "+p+" "+ip)
		if strings.Contains(detail, "eyJ") {
			t.Errorf("token in audit detail: %s", detail)
		}
	}
	want := []string{
		"ci:platform/payments-api namespace_not_allowed platform/payments-api 192.0.2.10",
		"ci:platformx/svc namespace_not_allowed platformx/svc 192.0.2.10",
		"ci:platform/payments-api ref_not_protected platform/payments-api 192.0.2.10",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("audit rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestIntegrationGzipUpload(t *testing.T) {
	e := newIEnv(t, nil)
	body := readFixture(t, "image-full.json")
	packed := gz(t, body)
	res := e.mustUpload(upload{body: packed, gzip: true})
	var stored []byte
	if err := e.pool.QueryRow(context.Background(), `SELECT raw_report FROM scans WHERE id=$1`, res.ScanID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, packed) {
		t.Error("the uploaded gzip bytes must be stored unchanged")
	}
	if e.count("vulnerabilities") != 138 || res.Counts.Critical != 2 {
		t.Errorf("rows %d result %+v", e.count("vulnerabilities"), res)
	}
}

func TestIntegrationSizeLimit(t *testing.T) {
	e := newIEnv(t, func(o *Options) { o.MaxReportBytes = 100 << 10 })
	code, body := e.upload(upload{body: readFixture(t, "image-full.json")})
	if code != http.StatusRequestEntityTooLarge || !strings.Contains(string(body), "report_too_large") {
		t.Fatalf("%d %s", code, body)
	}
	// A gzip bomb: small upload, large report.
	big := append([]byte(`{"SchemaVersion":2,"ArtifactName":"`), bytes.Repeat([]byte("a"), 5<<20)...)
	big = append(big, `"}`...)
	packed := gz(t, big)
	if len(packed) > 100<<10 {
		t.Fatalf("test setup: %d bytes", len(packed))
	}
	code, body = e.upload(upload{body: packed, gzip: true})
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("bomb: %d %s", code, body)
	}
	if e.count("scans") != 0 || e.count("services") != 0 {
		t.Error("a rejected upload left rows behind")
	}
}

func TestIntegrationConcurrentUploads(t *testing.T) {
	e := newIEnv(t, nil)
	body := readFixture(t, "image-suppressed.json")
	const n = 8
	var wg sync.WaitGroup
	results := make([]Result, n)
	codes := make([]int, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, b := e.upload(upload{body: body})
			codes[i] = code
			_ = json.Unmarshal(b, &results[i])
		}()
	}
	wg.Wait()
	created, replaced := 0, 0
	for i := range n {
		if codes[i] != http.StatusCreated {
			t.Fatalf("upload %d: %d", i, codes[i])
		}
		if results[i].ServiceCreated {
			created++
		}
		if results[i].Replaced {
			replaced++
		}
	}
	if created != 1 || replaced != n-1 {
		t.Errorf("created %d replaced %d, want 1 and %d", created, replaced, n-1)
	}
	if e.count("scans") != 1 || e.count("services") != 1 || e.count("vulnerabilities") != 138 {
		t.Errorf("scans %d services %d vulnerabilities %d", e.count("scans"), e.count("services"), e.count("vulnerabilities"))
	}
}

// synthetic builds a report of about size bytes by repeating the vulnerabilities
// of a real report under distinct targets.
func synthetic(t testing.TB, size int, dense bool) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(readFixture(t, "image-full.json"), &doc); err != nil {
		t.Fatal(err)
	}
	results := doc["Results"].([]any)
	template := results[0].(map[string]any)
	vulns := template["Vulnerabilities"].([]any)
	if dense {
		small := make([]any, len(vulns))
		for i, v := range vulns {
			m := v.(map[string]any)
			small[i] = map[string]any{
				"VulnerabilityID": m["VulnerabilityID"], "PkgName": m["PkgName"], "InstalledVersion": m["InstalledVersion"],
				"FixedVersion": m["FixedVersion"], "Severity": m["Severity"], "Status": m["Status"], "Title": m["Title"],
				"PrimaryURL": m["PrimaryURL"],
			}
		}
		vulns = small
	}
	var buf bytes.Buffer
	head, _ := json.Marshal(map[string]any{"SchemaVersion": 2, "CreatedAt": doc["CreatedAt"], "ArtifactName": "registry.example.com/team/app:1.0",
		"ArtifactType": "container_image", "Trivy": doc["Trivy"]})
	buf.Write(head[:len(head)-1])
	buf.WriteString(`,"Results":[`)
	for i := 0; buf.Len() < size; i++ {
		if i > 0 {
			buf.WriteByte(',')
		}
		res := map[string]any{"Target": fmt.Sprintf("registry.example.com/team/app:1.0 (debian 10.13) layer %d", i),
			"Class": "os-pkgs", "Type": "debian", "Vulnerabilities": vulns}
		b, _ := json.Marshal(res)
		buf.Write(b)
	}
	buf.WriteString(`]}`)
	return buf.Bytes()
}

func TestIntegrationIngest20MB(t *testing.T) {
	for _, tc := range []struct {
		name  string
		dense bool
		gzip  bool
	}{
		{"full-detail plain", false, false},
		{"full-detail gzip", false, true},
		{"dense plain", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newIEnv(t, nil)
			body := synthetic(t, 20<<20, tc.dense)
			if len(body) < 20<<20 {
				t.Fatalf("report is %d bytes", len(body))
			}
			var want Counts
			rep, err := Parse(bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			want = rep.Counts
			sent := body
			if tc.gzip {
				sent = gz(t, body)
			}
			start := time.Now()
			res := e.mustUpload(upload{body: sent, gzip: tc.gzip})
			elapsed := time.Since(start)
			t.Logf("%s: %.1f MB report (%d bytes sent), %d vulnerabilities, ingested in %v", tc.name, float64(len(body))/(1<<20), len(sent), len(rep.Vulns), elapsed)
			// 3 s is the target; the race detector multiplies the time.
			if limit := 3 * time.Second * raceSlowdown; elapsed > limit {
				t.Errorf("ingest took %v, want under %v", elapsed, limit)
			}
			if res.Counts.Critical != want.Critical || int(res.Fixable) != int(want.Fixable) || e.count("vulnerabilities") != len(rep.Vulns) {
				t.Errorf("result %+v rows %d, want counts %+v rows %d", res, e.count("vulnerabilities"), want, len(rep.Vulns))
			}
			var stored []byte
			_ = e.pool.QueryRow(context.Background(), `SELECT raw_report FROM scans`).Scan(&stored)
			zr := gunzip(t, stored)
			if !bytes.Equal(zr, body) {
				t.Error("stored report differs")
			}
		})
	}
}

// Long artifact names and refs are clipped to fit their index instead of
// failing the insert.
func TestIntegrationLongIndexedValues(t *testing.T) {
	e := newIEnv(t, nil)
	// Incompressible, so PostgreSQL cannot shrink the index entry.
	var long strings.Builder
	for i := 0; long.Len() < 10_000; i++ {
		fmt.Fprintf(&long, "%x", sha256.Sum256([]byte{byte(i), byte(i >> 8)}))
	}
	name, ref := long.String(), long.String()
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"` + name + `"}`)
	res := e.mustUpload(upload{body: body, claims: func(c map[string]any) { c["ref"] = ref }})
	var gotName, gotRef string
	if err := e.pool.QueryRow(context.Background(), `SELECT artifact_name, ref FROM scans WHERE id=$1`, res.ScanID).Scan(&gotName, &gotRef); err != nil {
		t.Fatal(err)
	}
	if gotName != name[:maxArtifactLen] || gotRef != ref[:maxRefLen] {
		t.Errorf("stored lengths %d and %d", len(gotName), len(gotRef))
	}
	// The same upload again replaces it: the clipped name is the key.
	if again := e.mustUpload(upload{body: body, claims: func(c map[string]any) { c["ref"] = ref }}); !again.Replaced {
		t.Error("second upload did not replace the first")
	}
}

func TestIntegrationArchivedServices(t *testing.T) {
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	reasons := func(e *iEnv) []string {
		rows, err := e.pool.Query(context.Background(), `SELECT detail->>'reason' FROM audit_log WHERE action='ingest.rejected' ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var r string
			_ = rows.Scan(&r)
			out = append(out, r)
		}
		return out
	}

	t.Run("manual service not adopted", func(t *testing.T) {
		e := newIEnv(t, nil)
		if _, err := e.pool.Exec(context.Background(), `INSERT INTO services (name, gitlab_project_path, created_via, archived)
			VALUES ('Payments', 'platform/payments-api', 'manual', true)`); err != nil {
			t.Fatal(err)
		}
		code, b := e.upload(upload{body: body})
		if code != http.StatusConflict || !strings.Contains(string(b), `"service_archived"`) ||
			!strings.Contains(string(b), "Ask an administrator to unarchive it.") {
			t.Fatalf("%d %s", code, b)
		}
		var pid *int64
		_ = e.pool.QueryRow(context.Background(), `SELECT gitlab_project_id FROM services`).Scan(&pid)
		if pid != nil || e.count("services") != 1 || e.count("scans") != 0 {
			t.Errorf("project id %v, %d services, %d scans", pid, e.count("services"), e.count("scans"))
		}
		var n int
		_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action='service.updated'`).Scan(&n)
		if n != 0 || strings.Join(reasons(e), ",") != "service_archived" {
			t.Errorf("service.updated events %d, rejections %v", n, reasons(e))
		}
	})

	t.Run("service with the project id", func(t *testing.T) {
		e := newIEnv(t, nil)
		first := e.mustUpload(upload{body: body})
		if _, err := e.pool.Exec(context.Background(), `UPDATE services SET archived = true WHERE id=$1`, first.ServiceID); err != nil {
			t.Fatal(err)
		}
		code, b := e.upload(upload{body: body, claims: func(c map[string]any) { c["job_id"] = "3003" }})
		if code != http.StatusConflict || !strings.Contains(string(b), `"service_archived"`) {
			t.Fatalf("%d %s", code, b)
		}
		if e.count("scans") != 1 {
			t.Errorf("scans = %d: an archived service received data", e.count("scans"))
		}
		if strings.Join(reasons(e), ",") != "service_archived" {
			t.Errorf("rejections %v", reasons(e))
		}
	})

	t.Run("path held by another project is still a conflict", func(t *testing.T) {
		e := newIEnv(t, nil)
		if _, err := e.pool.Exec(context.Background(), `INSERT INTO services (name, gitlab_project_path, gitlab_project_id, created_via, archived)
			VALUES ('old', 'platform/payments-api', 7, 'ingest', true)`); err != nil {
			t.Fatal(err)
		}
		code, b := e.upload(upload{body: body})
		if code != http.StatusConflict || !strings.Contains(string(b), `"service_conflict"`) {
			t.Fatalf("%d %s", code, b)
		}
	})
}

// GitLab paths are case-insensitive: a service an administrator typed in
// another case is adopted, and takes GitLab's spelling.
func TestIntegrationPathCaseDifference(t *testing.T) {
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	t.Run("adopted with the spelling of the token", func(t *testing.T) {
		e := newIEnv(t, nil)
		var id int64
		if err := e.pool.QueryRow(context.Background(), `INSERT INTO services (name, gitlab_project_path, created_via)
			VALUES ('Payments', 'Platform/Payments-API', 'manual') RETURNING id`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		res := e.mustUpload(upload{body: body})
		if res.ServiceID != id || res.ServiceCreated || e.count("services") != 1 {
			t.Fatalf("result = %+v, %d services", res, e.count("services"))
		}
		var path string
		var pid int64
		_ = e.pool.QueryRow(context.Background(), `SELECT gitlab_project_path, gitlab_project_id FROM services`).Scan(&path, &pid)
		if path != "platform/payments-api" || pid != 42 {
			t.Errorf("path %q id %d", path, pid)
		}
		// The audit record shows both changes, with the previous spelling.
		var detail string
		if err := e.pool.QueryRow(context.Background(), `SELECT detail::text FROM audit_log WHERE action='service.updated'`).Scan(&detail); err != nil {
			t.Fatal(err)
		}
		var d map[string]any
		_ = json.Unmarshal([]byte(detail), &d)
		want := map[string]any{"id": float64(id), "gitlab_project_path": "platform/payments-api",
			"fields": []any{"gitlab_project_id", "gitlab_project_path"}, "old_gitlab_project_path": "Platform/Payments-API",
			"gitlab_project_id": 42.0, "reason": "adopted by the first verified upload"}
		if !jsonEqual(d, want) {
			t.Errorf("audit detail = %s", detail)
		}
	})
	t.Run("archived manual service in another case", func(t *testing.T) {
		e := newIEnv(t, nil)
		if _, err := e.pool.Exec(context.Background(), `INSERT INTO services (name, gitlab_project_path, created_via, archived)
			VALUES ('Payments', 'PLATFORM/payments-api', 'manual', true)`); err != nil {
			t.Fatal(err)
		}
		code, b := e.upload(upload{body: body})
		if code != http.StatusConflict || !strings.Contains(string(b), `"service_archived"`) || e.count("services") != 1 {
			t.Fatalf("%d %s", code, b)
		}
	})
	t.Run("renamed project keeps its service", func(t *testing.T) {
		e := newIEnv(t, nil)
		first := e.mustUpload(upload{body: body})
		again := e.mustUpload(upload{body: body, claims: func(c map[string]any) { c["project_path"] = "Platform/Payments-API" }})
		var path string
		_ = e.pool.QueryRow(context.Background(), `SELECT gitlab_project_path FROM services`).Scan(&path)
		if again.ServiceID != first.ServiceID || path != "Platform/Payments-API" || e.count("services") != 1 {
			t.Errorf("service %d path %q services %d", again.ServiceID, path, e.count("services"))
		}
	})
	t.Run("another project with a path differing in case is a conflict", func(t *testing.T) {
		e := newIEnv(t, nil)
		if _, err := e.pool.Exec(context.Background(), `INSERT INTO services (name, gitlab_project_path, gitlab_project_id, created_via)
			VALUES ('old', 'Platform/Payments-API', 7, 'ingest')`); err != nil {
			t.Fatal(err)
		}
		code, b := e.upload(upload{body: body})
		if code != http.StatusConflict || !strings.Contains(string(b), `"service_conflict"`) {
			t.Fatalf("%d %s", code, b)
		}
		// A rename onto a path that differs only in case from another service is refused too.
		e.mustUpload(upload{body: body, claims: func(c map[string]any) { c["project_id"], c["project_path"] = "8", "platform/other" }})
		e.mustUpload(upload{body: body, claims: func(c map[string]any) { c["project_id"], c["project_path"] = "8", "PLATFORM/PAYMENTS-api" }})
		var path string
		_ = e.pool.QueryRow(context.Background(), `SELECT gitlab_project_path FROM services WHERE gitlab_project_id = 8`).Scan(&path)
		if path != "platform/other" {
			t.Errorf("path = %q, want the old path kept", path)
		}
	})
}
