// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/siyamsarker/scanopsis/internal/findings"
)

// migrationStatement returns the statement of migration 00011 between
// "-- +goose StatementBegin" and "-- +goose StatementEnd" that contains what.
func migrationStatement(t *testing.T, what string) string {
	t.Helper()
	src, err := os.ReadFile("../store/migrations/00011_artifact_key.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range strings.Split(string(src), "-- +goose StatementBegin\n")[1:] {
		stmt, _, ok := strings.Cut(part, "-- +goose StatementEnd")
		if ok && strings.Contains(stmt, what) {
			return stmt
		}
	}
	t.Fatalf("no statement with %q in migration 00011", what)
	return ""
}

// TestArtifactKeyMatchesMigration runs the key cases through the statement of
// migration 00011 that fills in the keys of older scans: an upload and the
// migration must give every name the same key.
func TestArtifactKeyMatchesMigration(t *testing.T) {
	block := migrationStatement(t, "UPDATE scans s")
	e := newIEnv(t, nil)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `ALTER TABLE scans ALTER COLUMN artifact_key DROP NOT NULL`); err != nil {
		t.Fatal(err)
	}
	// Project paths are unique in any letter case, so each spelling gets a
	// database of its own turn.
	byProject := map[string][]keyCase{}
	var order []string
	for _, tc := range keyCases {
		if _, seen := byProject[tc.project]; !seen {
			order = append(order, tc.project)
		}
		byProject[tc.project] = append(byProject[tc.project], tc)
	}
	for _, project := range order {
		if _, err := e.pool.Exec(ctx, `TRUNCATE services CASCADE`); err != nil {
			t.Fatal(err)
		}
		var svc int64
		if err := e.pool.QueryRow(ctx, `INSERT INTO services (name, gitlab_project_path, created_via)
			VALUES ('web-frontend', $1, 'manual') RETURNING id`, project).Scan(&svc); err != nil {
			t.Fatal(err)
		}
		ids := map[int64]keyCase{}
		for i, tc := range byProject[project] {
			var id int64
			if err := e.pool.QueryRow(ctx, `INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id,
				gitlab_job_id, ref, ref_protected, commit_sha, trivy_version, schema_version, scanned_at, raw_report)
				VALUES ($1, $2, $3, 1, $4, 'main', true, $5, '0.58.0', 2, now(), '\x00') RETURNING id`,
				svc, tc.kind, tc.name, i+1, fmt.Sprintf("%040x", i+1)).Scan(&id); err != nil {
				t.Fatalf("%+v: %v", tc, err)
			}
			ids[id] = tc
		}
		if _, err := e.pool.Exec(ctx, block); err != nil {
			t.Fatal(err)
		}
		rows, err := e.pool.Query(ctx, `SELECT id, artifact_key FROM scans`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id int64
			var key string
			if err := rows.Scan(&id, &key); err != nil {
				t.Fatal(err)
			}
			tc := ids[id]
			got, err := ArtifactKey(tc.kind, tc.name, tc.project, "")
			if err != nil || got != key || key != tc.want {
				t.Errorf("%s %q in %q: migration %q, upload %q (%v), want %q", tc.kind, tc.name, tc.project, key, got, err, tc.want)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestVulnTargetMatchesMigration runs the target cases through the statement
// of migration 00011 that renames the targets of older image scans: an
// upload and the migration must store every finding with the same target.
// The statement runs twice, as a start that was interrupted repeats it.
func TestVulnTargetMatchesMigration(t *testing.T) {
	block := migrationStatement(t, "UPDATE vulnerabilities v")
	e := newIEnv(t, nil)
	ctx := context.Background()
	var svc int64
	if err := e.pool.QueryRow(ctx, `INSERT INTO services (name, gitlab_project_path, created_via)
		VALUES ('web-frontend', 'demo/web-frontend', 'manual') RETURNING id`).Scan(&svc); err != nil {
		t.Fatal(err)
	}
	ids := map[int64]targetCase{}
	for i, tc := range targetCases {
		var scan, id int64
		if err := e.pool.QueryRow(ctx, `INSERT INTO scans (service_id, kind, artifact_name, artifact_key, gitlab_pipeline_id,
			gitlab_job_id, ref, ref_protected, commit_sha, trivy_version, schema_version, scanned_at, raw_report)
			VALUES ($1, $2, $3, $4, 1, $5, 'main', true, $6, '0.58.0', 2, now(), '\x00') RETURNING id`,
			svc, tc.kind, tc.name, tc.key, i+1, fmt.Sprintf("%040x", i+1)).Scan(&scan); err != nil {
			t.Fatalf("%+v: %v", tc, err)
		}
		if err := e.pool.QueryRow(ctx, `INSERT INTO vulnerabilities (scan_id, target, class, pkg_type, pkg_name,
			installed_version, fixed_version, vuln_id, severity, title, suppressed)
			VALUES ($1, $2, 'os-pkgs', 'alpine', 'pkg', '1', '', 'CVE-2026-1', 'HIGH', 't', false) RETURNING id`,
			scan, tc.target).Scan(&id); err != nil {
			t.Fatalf("%+v: %v", tc, err)
		}
		ids[id] = tc
	}
	for run := 1; run <= 2; run++ {
		if _, err := e.pool.Exec(ctx, block); err != nil {
			t.Fatal(err)
		}
		rows, err := e.pool.Query(ctx, `SELECT id, target FROM vulnerabilities`)
		if err != nil {
			t.Fatal(err)
		}
		type row struct {
			ID     int64
			Target string
		}
		list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != len(targetCases) {
			t.Fatalf("%d vulnerabilities, want %d", len(list), len(targetCases))
		}
		for _, r := range list {
			tc := ids[r.ID]
			if got := vulnTarget(tc.kind, tc.name, tc.key, tc.target); got != r.Target || r.Target != tc.want {
				t.Errorf("run %d: %s %q (key %q) target %q: migration %q, upload %q, want %q", run, tc.kind, tc.name, tc.key, tc.target, r.Target, got, tc.want)
			}
		}
	}
}

// tagReport is an image report of registry.example.com/demo/web-frontend-stage
// with the given tag, scanned minute minutes into the last hour, with one
// vulnerability per package. Packages named os* are OS packages, whose
// target Trivy names after the image ("<image> (alpine 3.20.3)"); the others
// are npm packages. a, b, c, os1 and os2 are critical, high, medium, high and
// medium; any other package is low.
func tagReport(tag string, minute int, pkgs ...string) []byte {
	name := "registry.example.com/demo/web-frontend-stage:" + tag
	sev := map[string]string{"a": "CRITICAL", "b": "HIGH", "c": "MEDIUM", "os1": "HIGH", "os2": "MEDIUM"}
	var osVulns, langVulns []map[string]any
	for _, p := range pkgs {
		s := sev[p]
		if s == "" {
			s = "LOW"
		}
		v := map[string]any{
			"VulnerabilityID": "CVE-2026-" + p, "PkgName": "pkg-" + p,
			"InstalledVersion": "1.0.0", "FixedVersion": "1.0.1", "Severity": s, "Title": "flaw in " + p,
		}
		if strings.HasPrefix(p, "os") {
			osVulns = append(osVulns, v)
		} else {
			langVulns = append(langVulns, v)
		}
	}
	var results []map[string]any
	if len(osVulns) > 0 {
		results = append(results, map[string]any{"Target": name + " (alpine 3.20.3)", "Class": "os-pkgs", "Type": "alpine", "Vulnerabilities": osVulns})
	}
	if len(langVulns) > 0 {
		results = append(results, map[string]any{"Target": "app/package-lock.json", "Class": "lang-pkgs", "Type": "npm", "Vulnerabilities": langVulns})
	}
	b, err := json.Marshal(map[string]any{
		"SchemaVersion": 2, "ArtifactType": "container_image", "ArtifactName": name,
		"CreatedAt": time.Now().UTC().Truncate(time.Hour).Add(-time.Hour + time.Duration(minute)*time.Minute).Format(time.RFC3339),
		"Results":   results,
	})
	if err != nil {
		panic(err)
	}
	return b
}

// overviewOf is the part of the service overview these tests check.
type overviewOf struct {
	Artifacts []struct {
		ArtifactName string `json:"artifact_name"`
		Kind         string `json:"kind"`
	} `json:"artifacts"`
	Counts             SeverityCounts `json:"counts"`
	NewSincePrevious   int            `json:"new_since_previous"`
	FixedSincePrevious int            `json:"fixed_since_previous"`
}

// get calls a read operation of the service and decodes its answer into v.
func (e *iEnv) get(op string, serviceID int64, query string, v any) {
	e.t.Helper()
	h := findings.New(e.pool, e.cfg, slog.New(slog.NewTextHandler(io.Discard, nil))).Handlers()[op]
	id := strconv.FormatInt(serviceID, 10)
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/services/"+id+"/x"+query, nil)
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		e.t.Fatalf("%s: %d %s", op, w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		e.t.Fatal(err)
	}
}

func (e *iEnv) overview(serviceID int64) overviewOf {
	e.t.Helper()
	var ov overviewOf
	e.get("getServiceOverview", serviceID, "", &ov)
	return ov
}

// targets lists the distinct targets of the vulnerabilities of the scans
// with the given artifact name.
func (e *iEnv) targets(artifactName string) []string {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT DISTINCT v.target FROM vulnerabilities v
		JOIN scans s ON s.id = v.scan_id WHERE s.artifact_name = $1 ORDER BY 1`, artifactName)
	if err != nil {
		e.t.Fatal(err)
	}
	list, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		e.t.Fatal(err)
	}
	return list
}

// keys lists artifact_name -> artifact_key of every stored scan.
func (e *iEnv) keys() map[string]string {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT artifact_name, artifact_key FROM scans`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var n, k string
		if err := rows.Scan(&n, &k); err != nil {
			e.t.Fatal(err)
		}
		m[n] = k
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return m
}

func pipeline(n int) func(map[string]any) {
	return func(c map[string]any) {
		c["pipeline_id"], c["job_id"] = strconv.Itoa(1000+n), strconv.Itoa(2000+n)
	}
}

// TestIngestPerRunTags uploads the same image tagged per pipeline (:2, :3,
// :4). The tags are one artifact: the service shows one report, and new and
// fixed findings are counted from one tag to the next.
func TestIngestPerRunTags(t *testing.T) {
	e := newIEnv(t, nil)
	one := SeverityCounts{Critical: 1, High: 1, Medium: 1}

	first := e.mustUpload(upload{body: tagReport("2", 1, "a", "b", "c"), claims: pipeline(1)})
	ov := e.overview(first.ServiceID)
	// The first scan of an artifact has no previous one: all its findings are new.
	if len(ov.Artifacts) != 1 || ov.Counts != one || ov.NewSincePrevious != 3 || ov.FixedSincePrevious != 0 {
		t.Fatalf("after :2: %+v", ov)
	}

	second := e.mustUpload(upload{body: tagReport("3", 2, "a", "b", "c"), claims: pipeline(2)})
	if second.Replaced {
		t.Error("a scan of another pipeline must not replace the earlier one")
	}
	ov = e.overview(first.ServiceID)
	if len(ov.Artifacts) != 1 || ov.Artifacts[0].ArtifactName != "registry.example.com/demo/web-frontend-stage:3" ||
		ov.Counts != one || ov.NewSincePrevious != 0 || ov.FixedSincePrevious != 0 {
		t.Fatalf("after :3, the same findings: %+v", ov)
	}

	// c is fixed in :4 and d is new.
	e.mustUpload(upload{body: tagReport("4", 3, "a", "b", "d"), claims: pipeline(3)})
	ov = e.overview(first.ServiceID)
	if len(ov.Artifacts) != 1 || ov.Artifacts[0].ArtifactName != "registry.example.com/demo/web-frontend-stage:4" {
		t.Fatalf("artifacts after :4: %+v", ov.Artifacts)
	}
	if want := (SeverityCounts{Critical: 1, High: 1, Low: 1}); ov.Counts != want {
		t.Errorf("counts after :4 = %+v, want %+v (one report)", ov.Counts, want)
	}
	if ov.NewSincePrevious != 1 || ov.FixedSincePrevious != 1 {
		t.Errorf("after :4: new %d fixed %d, want 1 and 1", ov.NewSincePrevious, ov.FixedSincePrevious)
	}

	if n := e.count("scans"); n != 3 {
		t.Errorf("scans = %d, want 3 (every pipeline's scan is kept)", n)
	}
	for name, key := range e.keys() {
		if key != "registry.example.com/demo/web-frontend-stage" {
			t.Errorf("key of %s = %q", name, key)
		}
	}
}

// TestIngestTagsOSTargets: Trivy names the OS packages of an image after the
// image, tag included. They are stored under the artifact key, so the same
// OS findings in the next tag are neither new nor fixed, keep the time they
// were first seen, and the trend counts only real changes.
func TestIngestTagsOSTargets(t *testing.T) {
	e := newIEnv(t, nil)
	first := e.mustUpload(upload{body: tagReport("2", 1, "a", "os1", "os2"), claims: pipeline(1)})
	e.mustUpload(upload{body: tagReport("3", 2, "a", "os1", "os2"), claims: pipeline(2)})

	stage := "registry.example.com/demo/web-frontend-stage"
	for _, tag := range []string{"2", "3"} {
		if got := e.targets(stage + ":" + tag); strings.Join(got, "|") != "app/package-lock.json|"+stage+" (alpine 3.20.3)" {
			t.Errorf("targets of :%s = %q", tag, got)
		}
	}
	ov := e.overview(first.ServiceID)
	if len(ov.Artifacts) != 1 || ov.Counts != (SeverityCounts{Critical: 1, High: 1, Medium: 1}) || ov.NewSincePrevious != 0 || ov.FixedSincePrevious != 0 {
		t.Fatalf("after :3 with the same findings: %+v", ov)
	}

	var page struct {
		Items []struct {
			PkgName     string    `json:"pkg_name"`
			Target      string    `json:"target"`
			FirstSeenAt time.Time `json:"first_seen_at"`
		} `json:"items"`
	}
	e.get("listServiceVulnerabilities", first.ServiceID, "", &page)
	var firstScan time.Time
	if err := e.pool.QueryRow(context.Background(), `SELECT scanned_at FROM scans WHERE id = $1`, first.ScanID).Scan(&firstScan); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("vulnerabilities = %+v", page.Items)
	}
	for _, v := range page.Items {
		if !v.FirstSeenAt.Equal(firstScan) {
			t.Errorf("%s (%s) first seen %v, want %v (the scan of :2)", v.PkgName, v.Target, v.FirstSeenAt, firstScan)
		}
	}

	// os2 is fixed in :4.
	e.mustUpload(upload{body: tagReport("4", 3, "a", "os1"), claims: pipeline(3)})
	ov = e.overview(first.ServiceID)
	if len(ov.Artifacts) != 1 || ov.Counts != (SeverityCounts{Critical: 1, High: 1}) || ov.NewSincePrevious != 0 || ov.FixedSincePrevious != 1 {
		t.Fatalf("after :4 fixed os2: %+v", ov)
	}

	var trends struct {
		Points []struct {
			ArtifactName string         `json:"artifact_name"`
			New          SeverityCounts `json:"new"`
			Fixed        int            `json:"fixed"`
		} `json:"points"`
	}
	e.get("getServiceTrends", first.ServiceID, "", &trends)
	type point struct {
		name  string
		new   SeverityCounts
		fixed int
	}
	want := []point{
		{stage + ":2", SeverityCounts{Critical: 1, High: 1, Medium: 1}, 0},
		{stage + ":3", SeverityCounts{}, 0},
		{stage + ":4", SeverityCounts{}, 1},
	}
	if len(trends.Points) != len(want) {
		t.Fatalf("trend points = %+v", trends.Points)
	}
	for i, p := range trends.Points {
		if (point{p.ArtifactName, p.New, p.Fixed}) != want[i] {
			t.Errorf("trend point %d = %+v, want %+v", i, p, want[i])
		}
	}
}

// TestIngestRetrySameJob: a job that uploads the same artifact again
// replaces its own scan, as before; the tag is still part of what the job
// uploaded, so another tag from the same job is another scan of the same
// artifact.
func TestIngestRetrySameJob(t *testing.T) {
	e := newIEnv(t, nil)
	first := e.mustUpload(upload{body: tagReport("2", 1, "a", "b"), claims: pipeline(1)})
	retry := e.mustUpload(upload{body: tagReport("2", 2, "a"), claims: pipeline(1)})
	if !retry.Replaced || retry.ScanID == first.ScanID || e.count("scans") != 1 {
		t.Fatalf("retry = %+v, scans %d", retry, e.count("scans"))
	}
	ov := e.overview(first.ServiceID)
	if len(ov.Artifacts) != 1 || ov.Counts != (SeverityCounts{Critical: 1}) {
		t.Fatalf("after the retry: %+v", ov)
	}

	other := e.mustUpload(upload{body: tagReport("3", 3, "a"), claims: pipeline(1)})
	if other.Replaced || e.count("scans") != 2 {
		t.Fatalf("another tag from the same job: %+v, scans %d", other, e.count("scans"))
	}
	if ov := e.overview(first.ServiceID); len(ov.Artifacts) != 1 || ov.Counts != (SeverityCounts{Critical: 1}) {
		t.Fatalf("after another tag: %+v", ov)
	}
}

// TestIngestOverride: the artifact parameter keeps variants of one image
// apart, such as the builds for two platforms.
func TestIngestOverride(t *testing.T) {
	e := newIEnv(t, nil)
	amd := e.mustUpload(upload{body: tagReport("4", 1, "a"), query: "?artifact=web-frontend-stage-amd64", claims: pipeline(1)})
	arm := e.mustUpload(upload{body: tagReport("4", 2, "b"), query: "?artifact=web-frontend-stage-arm64", claims: func(c map[string]any) {
		pipeline(1)(c)
		c["job_id"] = "2101"
	}})
	if arm.Replaced {
		t.Fatal("the arm64 scan replaced the amd64 scan")
	}
	ov := e.overview(amd.ServiceID)
	if len(ov.Artifacts) != 2 || ov.Counts != (SeverityCounts{Critical: 1, High: 1}) {
		t.Fatalf("two variants: %+v", ov)
	}

	// The next pipeline's amd64 build replaces only the amd64 scan: a fixed
	// and c is new against the amd64 scan; b is new as the arm64 scan has no
	// previous one.
	e.mustUpload(upload{body: tagReport("5", 3, "c"), query: "?artifact=" + url.QueryEscape("web-frontend-stage-amd64"), claims: pipeline(2)})
	ov = e.overview(amd.ServiceID)
	if len(ov.Artifacts) != 2 || ov.Counts != (SeverityCounts{High: 1, Medium: 1}) || ov.NewSincePrevious != 2 || ov.FixedSincePrevious != 1 {
		t.Fatalf("after the next amd64 build: %+v", ov)
	}
	var keys []string
	rows, err := e.pool.Query(context.Background(), `SELECT artifact_key FROM scans ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	if rows.Err() != nil || strings.Join(keys, ",") != "web-frontend-stage-amd64,web-frontend-stage-arm64,web-frontend-stage-amd64" {
		t.Errorf("keys = %v, %v", keys, rows.Err())
	}
	// OS packages are stored under the name the upload gave.
	e.mustUpload(upload{body: tagReport("6", 5, "os1"), query: "?artifact=web-frontend-stage-amd64", claims: pipeline(4)})
	if got := e.targets("registry.example.com/demo/web-frontend-stage:6"); len(got) != 1 || got[0] != "web-frontend-stage-amd64 (alpine 3.20.3)" {
		t.Errorf("targets of :6 = %q", got)
	}

	// An invalid value stores nothing.
	for _, q := range []string{"?artifact=", "?artifact=" + strings.Repeat("x", 201), "?artifact=a%00b", "?artifact=a%0Ab"} {
		code, body := e.upload(upload{body: tagReport("6", 4, "a"), query: q, claims: pipeline(3)})
		if code != http.StatusBadRequest || !strings.Contains(string(body), `"invalid_parameter"`) {
			t.Errorf("%s: %d %s", q, code, body)
		}
	}
	if n := e.count("scans"); n != 4 {
		t.Errorf("scans = %d, want 4", n)
	}
}

// TestIngestSecretFolder: a repository scan named by the runner's build
// folder and one named "." are the same artifact.
func TestIngestSecretFolder(t *testing.T) {
	e := newIEnv(t, nil)
	report := func(name string, minute int, rules ...string) []byte {
		secrets := make([]map[string]any, 0, len(rules))
		for _, r := range rules {
			secrets = append(secrets, map[string]any{"RuleID": r, "Category": "AWS", "Severity": "CRITICAL", "Title": r, "StartLine": 1, "EndLine": 1})
		}
		b, _ := json.Marshal(map[string]any{
			"SchemaVersion": 2, "ArtifactType": "filesystem", "ArtifactName": name,
			"CreatedAt": fmt.Sprintf("2026-11-20T10:%02d:00Z", minute),
			"Results":   []map[string]any{{"Target": "config.env", "Class": "secret", "Secrets": secrets}},
		})
		return b
	}
	first := e.mustUpload(upload{body: report("/builds/x7Kq2/0/platform/payments-api", 1, "aws-access-key-id"), query: "?kind=secret-fs", claims: pipeline(1)})
	e.mustUpload(upload{body: report(".", 2, "aws-access-key-id", "aws-secret-access-key"), query: "?kind=secret-fs", claims: pipeline(2)})
	e.mustUpload(upload{body: report("/builds/x7Kq2/0/Platform/Payments-API/services/api", 3, "github-pat"), query: "?kind=secret-fs", claims: pipeline(3)})

	keys := e.keys()
	if keys["/builds/x7Kq2/0/platform/payments-api"] != "." || keys["."] != "." ||
		keys["/builds/x7Kq2/0/Platform/Payments-API/services/api"] != "services/api" {
		t.Fatalf("keys = %v", keys)
	}
	ov := e.overview(first.ServiceID)
	if len(ov.Artifacts) != 2 {
		t.Fatalf("artifacts = %+v, want the repository and services/api", ov.Artifacts)
	}
	var secrets int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM secret_findings f JOIN scans s ON s.id = f.scan_id
		WHERE s.artifact_key = '.' AND s.artifact_name = '.'`).Scan(&secrets); err != nil || secrets != 2 {
		t.Fatalf("secrets of the latest repository scan = %d, %v", secrets, err)
	}
}
