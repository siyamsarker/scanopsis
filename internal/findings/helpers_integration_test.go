// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package findings

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/settings"
)

type cfgStub struct{ url string }

func (c cfgStub) Current() settings.Snapshot { return settings.Snapshot{GitLabURL: c.url} }

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newMux(pool *pgxpool.Pool, gitlabURL string) http.Handler {
	h := New(pool, cfgStub{gitlabURL}, discardLog()).Handlers()
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/services/{id}/overview", h["getServiceOverview"])
	mux.Handle("GET /api/v1/services/{id}/scans", h["listServiceScans"])
	mux.Handle("GET /api/v1/scans/{id}", h["getScan"])
	mux.Handle("GET /api/v1/scans/{id}/diff", h["getScanDiff"])
	mux.Handle("GET /api/v1/services/{id}/vulnerabilities", h["listServiceVulnerabilities"])
	mux.Handle("GET /api/v1/services/{id}/secrets", h["listServiceSecrets"])
	mux.Handle("GET /api/v1/services/{id}/trends", h["getServiceTrends"])
	mux.Handle("GET /api/v1/search/vulnerabilities", h["searchVulnerabilities"])
	return mux
}

func get(t testing.TB, mux http.Handler, path string) (int, []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	return rec.Code, rec.Body.Bytes()
}

// getJSON expects status 200 and decodes the body into v.
func getJSON(t testing.TB, mux http.Handler, path string, v any) {
	t.Helper()
	code, body := get(t, mux, path)
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, code, body)
	}
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("GET %s: bad JSON: %v\n%s", path, err, body)
	}
}

// wantError expects an API error with the given status and code.
func wantError(t testing.TB, mux http.Handler, path string, status int, code string) {
	t.Helper()
	got, body := get(t, mux, path)
	var e struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(body, &e); err != nil || got != status || e.Error.Code != code {
		t.Errorf("GET %s = %d %s, want %d %s", path, got, body, status, code)
	}
}

func eq(t testing.TB, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s =\n%+v\nwant\n%+v", what, got, want)
	}
}

func newService(t testing.TB, pool *pgxpool.Pool, name, path string, archived bool) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO services (name, gitlab_project_path, created_via, archived)
		 VALUES ($1, $2, 'manual', $3) RETURNING id`, name, path, archived).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

var jobSeq atomic.Int64

func newScan(t testing.TB, pool *pgxpool.Pool, service int64, kind, artifact, ref string, at time.Time) int64 {
	t.Helper()
	var id int64
	job := jobSeq.Add(1)
	err := pool.QueryRow(context.Background(),
		`INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref,
		                    ref_protected, commit_sha, trivy_version, schema_version, scanned_at, raw_report)
		 VALUES ($1, $2, $3, $4, $5, $6, true, $7, '0.58.0', 2, $8, '\x00')
		 RETURNING id`,
		service, kind, artifact, 1000+job, job, ref, fmt.Sprintf("%040x", job), at).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type vuln struct {
	id, pkg, version, target, class, severity, fixed, title string
	suppressed                                              bool
	path                                                    string
}

// addVulns inserts the findings of a scan in order and returns their ids.
func addVulns(t testing.TB, pool *pgxpool.Pool, scan int64, vs ...vuln) []int64 {
	t.Helper()
	ids := make([]int64, 0, len(vs))
	for _, v := range vs {
		var id int64
		err := pool.QueryRow(context.Background(),
			`INSERT INTO vulnerabilities (scan_id, target, class, pkg_name, pkg_path, installed_version,
			                              fixed_version, vuln_id, severity, title, suppressed, suppression)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, CASE WHEN $11 THEN 'accepted' ELSE '' END)
			 RETURNING id`,
			scan, v.target, v.class, v.pkg, v.path, v.version, v.fixed, v.id, v.severity, v.title, v.suppressed).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	refresh(t, pool, scan)
	return ids
}

type secret struct {
	rule, target, severity, title string
	start, end                    int
	suppressed                    bool
}

func addSecrets(t testing.TB, pool *pgxpool.Pool, scan int64, ss ...secret) {
	t.Helper()
	for _, s := range ss {
		_, err := pool.Exec(context.Background(),
			`INSERT INTO secret_findings (scan_id, target, rule_id, category, severity, title, start_line, end_line, suppressed)
			 VALUES ($1, $2, $3, 'cat', $4, $5, $6, $7, $8)`,
			scan, s.target, s.rule, s.severity, s.title, s.start, s.end, s.suppressed)
		if err != nil {
			t.Fatal(err)
		}
	}
	refresh(t, pool, scan)
}

// refresh recomputes the counts kept on the scan row the way ingestion does:
// unsuppressed findings per severity, and the suppressed ones separately.
func refresh(t testing.TB, pool *pgxpool.Pool, scan int64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		UPDATE scans s SET
		  count_critical = (SELECT count(*) FROM vulnerabilities v WHERE v.scan_id = s.id AND NOT v.suppressed AND v.severity = 'CRITICAL'),
		  count_high     = (SELECT count(*) FROM vulnerabilities v WHERE v.scan_id = s.id AND NOT v.suppressed AND v.severity = 'HIGH'),
		  count_medium   = (SELECT count(*) FROM vulnerabilities v WHERE v.scan_id = s.id AND NOT v.suppressed AND v.severity = 'MEDIUM'),
		  count_low      = (SELECT count(*) FROM vulnerabilities v WHERE v.scan_id = s.id AND NOT v.suppressed AND v.severity = 'LOW'),
		  count_unknown  = (SELECT count(*) FROM vulnerabilities v WHERE v.scan_id = s.id AND NOT v.suppressed AND v.severity = 'UNKNOWN'),
		  count_fixable  = (SELECT count(*) FROM vulnerabilities v WHERE v.scan_id = s.id AND NOT v.suppressed AND v.fixed_version <> ''),
		  count_secrets  = (SELECT count(*) FROM secret_findings f WHERE f.scan_id = s.id AND NOT f.suppressed),
		  count_suppressed = (SELECT count(*) FROM vulnerabilities v WHERE v.scan_id = s.id AND v.suppressed)
		                   + (SELECT count(*) FROM secret_findings f WHERE f.scan_id = s.id AND f.suppressed)
		WHERE s.id = $1`, scan)
	if err != nil {
		t.Fatal(err)
	}
}
