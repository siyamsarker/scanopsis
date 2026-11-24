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

	"github.com/siyamsarker/scanopsis/internal/findings"
)

// TestArtifactKeyMatchesMigration runs the key cases through the statement of
// migration 00011 that fills in the keys of older scans: an upload and the
// migration must give every name the same key.
func TestArtifactKeyMatchesMigration(t *testing.T) {
	src, err := os.ReadFile("../store/migrations/00011_artifact_key.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, block, ok := strings.Cut(string(src), "-- +goose StatementBegin\n")
	if ok {
		block, _, ok = strings.Cut(block, "-- +goose StatementEnd")
	}
	if !ok || !strings.Contains(block, "SET artifact_key") {
		t.Fatal("the statement that fills in the keys was not found in migration 00011")
	}

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

// tagReport is an image report of registry.example.com/demo/web-frontend-stage
// with the given tag, scanned at minute, with one vulnerability per package:
// a, b and c are critical, high and medium, any other package is low.
func tagReport(tag string, minute int, pkgs ...string) []byte {
	sev := map[string]string{"a": "CRITICAL", "b": "HIGH", "c": "MEDIUM"}
	vulns := make([]map[string]any, 0, len(pkgs))
	for _, p := range pkgs {
		s := sev[p]
		if s == "" {
			s = "LOW"
		}
		vulns = append(vulns, map[string]any{
			"VulnerabilityID": "CVE-2026-1" + strconv.Itoa(int(p[0])), "PkgName": "pkg-" + p,
			"InstalledVersion": "1.0.0", "FixedVersion": "1.0.1", "Severity": s, "Title": "flaw in " + p,
		})
	}
	b, err := json.Marshal(map[string]any{
		"SchemaVersion": 2, "ArtifactType": "container_image",
		"ArtifactName": "registry.example.com/demo/web-frontend-stage:" + tag,
		"CreatedAt":    fmt.Sprintf("2026-11-20T10:%02d:00Z", minute),
		"Results": []map[string]any{{
			"Target": "app/package-lock.json", "Class": "lang-pkgs", "Type": "npm", "Vulnerabilities": vulns,
		}},
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

func (e *iEnv) overview(serviceID int64) overviewOf {
	e.t.Helper()
	h := findings.New(e.pool, e.cfg, slog.New(slog.NewTextHandler(io.Discard, nil))).Handlers()["getServiceOverview"]
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/services/"+strconv.FormatInt(serviceID, 10)+"/overview", nil)
	r.SetPathValue("id", strconv.FormatInt(serviceID, 10))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		e.t.Fatalf("overview: %d %s", w.Code, w.Body)
	}
	var ov overviewOf
	if err := json.Unmarshal(w.Body.Bytes(), &ov); err != nil {
		e.t.Fatal(err)
	}
	return ov
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

	// An invalid value stores nothing.
	for _, q := range []string{"?artifact=", "?artifact=" + strings.Repeat("x", 201), "?artifact=a%00b", "?artifact=a%0Ab"} {
		code, body := e.upload(upload{body: tagReport("6", 4, "a"), query: q, claims: pipeline(3)})
		if code != http.StatusBadRequest || !strings.Contains(string(body), `"invalid_parameter"`) {
			t.Errorf("%s: %d %s", q, code, body)
		}
	}
	if n := e.count("scans"); n != 3 {
		t.Errorf("scans = %d, want 3", n)
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
