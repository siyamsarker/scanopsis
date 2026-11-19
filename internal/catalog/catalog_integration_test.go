// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

type fakeSettings struct{ url string }

func (f fakeSettings) Current() settings.Snapshot { return settings.Snapshot{GitLabURL: f.url} }

type recordedEvent struct {
	actor, action string
	detail        map[string]any
}

type recorder struct {
	mu     sync.Mutex
	events []recordedEvent
}

func (r *recorder) Record(_ context.Context, actor, action string, detail map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedEvent{actor, action, detail})
}

func newCatalog(t *testing.T, url string) (*Catalog, *pgxpool.Pool, *recorder) {
	t.Helper()
	pool := storetest.New(t)
	rec := &recorder{}
	return New(pool, fakeSettings{url}, rec, slog.New(slog.NewTextHandler(io.Discard, nil))), pool, rec
}

func call(t *testing.T, c *Catalog, op, method, target, body, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	req = req.WithContext(auth.WithPrincipal(req.Context(), &auth.Principal{Source: "local", Username: "root", Role: auth.RoleAdmin}))
	if id != "" {
		req.SetPathValue("id", id)
	}
	rec := httptest.NewRecorder()
	c.Handlers()[op].ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d; body %s", rec.Code, status, rec.Body.String())
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return v
}

// Seeding helpers insert straight into the tables.

func mustExec(t testing.TB, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func addService(t *testing.T, pool *pgxpool.Pool, name, path string, archived bool) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO services (name, gitlab_project_path, created_via, archived) VALUES ($1, $2, 'ingest', $3) RETURNING id`,
		name, path, archived).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type scanSeed struct {
	svc                                         int64
	kind, artifact, ref                         string
	job                                         int64
	at                                          time.Time
	crit, high, med, low, unk, fixable, secrets int
}

func addScan(t *testing.T, pool *pgxpool.Pool, s scanSeed) {
	t.Helper()
	mustExec(t, pool, `INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref,
		ref_protected, commit_sha, schema_version, scanned_at, count_critical, count_high, count_medium, count_low,
		count_unknown, count_fixable, count_secrets, raw_report)
		VALUES ($1,$2,$3,1,$4,$5,false,'abc',2,$6,$7,$8,$9,$10,$11,$12,$13,'\x1f8b')`,
		s.svc, s.kind, s.artifact, s.job, s.ref, s.at, s.crit, s.high, s.med, s.low, s.unk, s.fixable, s.secrets)
}

func addSonar(t *testing.T, pool *pgxpool.Pool, svc int64, task string, at time.Time, gate, rel, sec, maint string, cov *float64) {
	t.Helper()
	mustExec(t, pool, `INSERT INTO sonar_analyses (service_id, task_id, analysed_at, quality_gate_status,
		reliability_rating, security_rating, maintainability_rating, coverage) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		svc, task, at, gate, rel, sec, maint, cov)
}

type counts struct{ Critical, High, Medium, Low, Unknown int64 }

type pfItem struct {
	Service struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"service"`
	Ref                   *string  `json:"ref"`
	RefSource             string   `json:"ref_source"`
	Counts                counts   `json:"counts"`
	Fixable               int64    `json:"fixable"`
	Secrets               int64    `json:"secrets"`
	LastScanAt            *string  `json:"last_scan_at"`
	QualityGateStatus     *string  `json:"quality_gate_status"`
	ReliabilityRating     *string  `json:"reliability_rating"`
	SecurityRating        *string  `json:"security_rating"`
	MaintainabilityRating *string  `json:"maintainability_rating"`
	Coverage              *float64 `json:"coverage"`
	LastAnalysisAt        *string  `json:"last_analysis_at"`
}

type pfResponse struct {
	Items  []pfItem `json:"items"`
	Totals struct {
		Services            int   `json:"services"`
		FailingQualityGates int   `json:"failing_quality_gates"`
		ServicesWithCrit    int   `json:"services_with_critical"`
		Fixable             int64 `json:"fixable"`
	} `json:"totals"`
	GeneratedAt string `json:"generated_at"`
}

func lowerCounts(c counts) counts {
	return counts{c.Critical, c.High, c.Medium, c.Low, c.Unknown}
}

func sameTime(t *testing.T, field string, got *string, want time.Time) {
	t.Helper()
	if got == nil {
		t.Errorf("%s = nil, want %s", field, want)
		return
	}
	g, err := time.Parse(time.RFC3339Nano, *got)
	if err != nil || !g.Equal(want) {
		t.Errorf("%s = %q, want %s (%v)", field, *got, want, err)
	}
}

func str(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestPortfolio(t *testing.T) {
	c, pool, _ := newCatalog(t, "")
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	h := func(n int) time.Time { return base.Add(time.Duration(n) * time.Hour) }

	alpha := addService(t, pool, "alpha", "g/alpha", false)
	addService(t, pool, "bravo", "g/bravo", false)
	charlie := addService(t, pool, "charlie", "g/charlie", true)
	delta := addService(t, pool, "delta", "g/delta", false)
	echo := addService(t, pool, "echo", "g/echo", false)

	// alpha: "main" has an old, large scan; "feature" has the most recent scan.
	addScan(t, pool, scanSeed{svc: alpha, kind: "image", artifact: "x", ref: "main", job: 1, at: h(-10), crit: 50, high: 50, fixable: 50})
	// feature/api: job 2 is replaced by the newer job 3 (older scans are ignored).
	addScan(t, pool, scanSeed{svc: alpha, kind: "image", artifact: "api", ref: "feature", job: 2, at: h(-5), crit: 9, high: 9, med: 9, low: 9, unk: 9, fixable: 9, secrets: 9})
	addScan(t, pool, scanSeed{svc: alpha, kind: "image", artifact: "api", ref: "feature", job: 3, at: h(-2), crit: 1, high: 2, med: 3, low: 4, unk: 5, fixable: 6})
	addScan(t, pool, scanSeed{svc: alpha, kind: "image", artifact: "web", ref: "feature", job: 3, at: h(-1), high: 1, med: 1, low: 1, unk: 1, fixable: 2})
	// Same artifact name, other kind, counts as its own current scan.
	addScan(t, pool, scanSeed{svc: alpha, kind: "secret-fs", artifact: "api", ref: "feature", job: 3, at: h(-3), crit: 1, secrets: 2})

	// bravo: no scans, no analyses. charlie: archived with scans.
	addScan(t, pool, scanSeed{svc: charlie, kind: "image", artifact: "x", ref: "main", job: 1, at: h(-1), crit: 7, fixable: 7})
	addSonar(t, pool, charlie, "t-charlie", h(-1), "ERROR", "E", "E", "E", nil)

	// delta: scans on main only; two analyses, the newer one has gaps.
	addScan(t, pool, scanSeed{svc: delta, kind: "image", artifact: "x", ref: "main", job: 1, at: h(-4), high: 3, fixable: 4})
	addSonar(t, pool, delta, "t-delta-old", h(-8), "ERROR", "E", "E", "E", ptr(12.5))
	addSonar(t, pool, delta, "t-delta-new", h(-6), "OK", "A", "", "C", nil)

	// echo: failing gate with complete ratings and coverage.
	addScan(t, pool, scanSeed{svc: echo, kind: "image", artifact: "x", ref: "main", job: 1, at: h(-9), crit: 3, fixable: 1})
	addSonar(t, pool, echo, "t-echo", h(-7), "ERROR", "B", "D", "E", ptr(55.5))

	rec := call(t, c, "getPortfolio", "GET", "/", "", "")
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	pf := decode[pfResponse](t, rec, http.StatusOK)

	var names []string
	for _, it := range pf.Items {
		names = append(names, it.Service.Name)
	}
	if want := []string{"alpha", "bravo", "delta", "echo"}; !slices.Equal(names, want) {
		t.Fatalf("services = %v, want %v", names, want)
	}
	if g, err := time.Parse(time.RFC3339Nano, pf.GeneratedAt); err != nil || time.Since(g) > time.Minute {
		t.Errorf("generated_at = %q (%v)", pf.GeneratedAt, err)
	}

	a, b, d, e := pf.Items[0], pf.Items[1], pf.Items[2], pf.Items[3]

	// alpha: default ref feature; api(image) + web(image) + api(secret-fs).
	if str(a.Ref) != "feature" {
		t.Errorf("alpha ref = %s", str(a.Ref))
	}
	if want := (counts{Critical: 2, High: 3, Medium: 4, Low: 5, Unknown: 6}); lowerCounts(a.Counts) != want {
		t.Errorf("alpha counts = %+v, want %+v", a.Counts, want)
	}
	if a.Fixable != 8 || a.Secrets != 2 {
		t.Errorf("alpha fixable/secrets = %d/%d, want 8/2", a.Fixable, a.Secrets)
	}
	sameTime(t, "alpha last_scan_at", a.LastScanAt, h(-1))
	if a.QualityGateStatus != nil || a.ReliabilityRating != nil || a.SecurityRating != nil ||
		a.MaintainabilityRating != nil || a.Coverage != nil || a.LastAnalysisAt != nil {
		t.Errorf("alpha has sonar values: %+v", a)
	}

	// bravo: nothing at all.
	if b.Ref != nil || (b.Counts != counts{}) || b.Fixable != 0 || b.Secrets != 0 || b.LastScanAt != nil ||
		b.QualityGateStatus != nil || b.Coverage != nil || b.LastAnalysisAt != nil {
		t.Errorf("bravo = %+v", b)
	}

	// delta: newest analysis wins; empty rating is null; coverage null.
	if str(d.Ref) != "main" || d.Counts != (counts{High: 3}) || d.Fixable != 4 || d.Secrets != 0 {
		t.Errorf("delta = %+v", d)
	}
	sameTime(t, "delta last_scan_at", d.LastScanAt, h(-4))
	if str(d.QualityGateStatus) != "OK" || str(d.ReliabilityRating) != "A" || d.SecurityRating != nil ||
		str(d.MaintainabilityRating) != "C" || d.Coverage != nil {
		t.Errorf("delta sonar = %+v", d)
	}
	sameTime(t, "delta last_analysis_at", d.LastAnalysisAt, h(-6))

	// echo.
	if e.Counts != (counts{Critical: 3}) || e.Fixable != 1 {
		t.Errorf("echo = %+v", e)
	}
	if str(e.QualityGateStatus) != "ERROR" || str(e.ReliabilityRating) != "B" || str(e.SecurityRating) != "D" ||
		str(e.MaintainabilityRating) != "E" || e.Coverage == nil || *e.Coverage != 55.5 {
		t.Errorf("echo sonar = %+v", e)
	}
	sameTime(t, "echo last_analysis_at", e.LastAnalysisAt, h(-7))

	// Totals exclude archived charlie.
	if pf.Totals.Services != 4 || pf.Totals.FailingQualityGates != 1 || pf.Totals.ServicesWithCrit != 2 || pf.Totals.Fixable != 13 {
		t.Errorf("totals = %+v, want services 4, failing 1, critical 2, fixable 13", pf.Totals)
	}
}

func TestPortfolioEmpty(t *testing.T) {
	c, _, _ := newCatalog(t, "")
	rec := call(t, c, "getPortfolio", "GET", "/", "", "")
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("body %s", rec.Body.String())
	}
	pf := decode[pfResponse](t, rec, http.StatusOK)
	if pf.Totals.Services != 0 || len(pf.Items) != 0 {
		t.Errorf("%+v", pf)
	}
}

func TestPortfolioDefaultRefTie(t *testing.T) {
	c, pool, _ := newCatalog(t, "")
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s := addService(t, pool, "s", "g/s", false)
	addScan(t, pool, scanSeed{svc: s, kind: "image", artifact: "x", ref: "zeta", job: 1, at: at, crit: 1})
	addScan(t, pool, scanSeed{svc: s, kind: "image", artifact: "x", ref: "beta", job: 2, at: at, crit: 2})
	pf := decode[pfResponse](t, call(t, c, "getPortfolio", "GET", "/", "", ""), http.StatusOK)
	if str(pf.Items[0].Ref) != "beta" || pf.Items[0].Counts.Critical != 2 {
		t.Errorf("tie should resolve like ListRefs (by ref): %+v", pf.Items[0])
	}
}

type svcJSON struct {
	ID              int64   `json:"id"`
	Name            string  `json:"name"`
	GitLabProjectID *int64  `json:"gitlab_project_id"`
	Path            string  `json:"gitlab_project_path"`
	SonarKey        *string `json:"sonar_project_key"`
	CreatedVia      string  `json:"created_via"`
	Archived        bool    `json:"archived"`
	CreatedAt       string  `json:"created_at"`
	DefaultRef      *string `json:"default_ref"`
}

type pageJSON struct {
	Items     []svcJSON `json:"items"`
	NextAfter *int64    `json:"next_after"`
}

func TestListServices(t *testing.T) {
	c, pool, _ := newCatalog(t, "")
	var ids []int64
	for i := 1; i <= 5; i++ {
		ids = append(ids, addService(t, pool, fmt.Sprintf("Svc%d", i), fmt.Sprintf("team/Proj_%d", i), false))
	}
	old := addService(t, pool, "Old", "legacy/old%x", true)

	get := func(q string) pageJSON {
		return decode[pageJSON](t, call(t, c, "listServices", "GET", "/?"+q, "", ""), http.StatusOK)
	}
	idsOf := func(p pageJSON) []int64 {
		out := []int64{}
		for _, s := range p.Items {
			out = append(out, s.ID)
		}
		return out
	}

	p := get("")
	if !slices.Equal(idsOf(p), ids) || p.NextAfter != nil {
		t.Errorf("default: %v next %v", idsOf(p), p.NextAfter)
	}
	if p.Items[0].CreatedVia != "ingest" || p.Items[0].GitLabProjectID != nil || p.Items[0].SonarKey != nil || p.Items[0].CreatedAt == "" {
		t.Errorf("item = %+v", p.Items[0])
	}

	p = get("limit=2")
	if !slices.Equal(idsOf(p), ids[:2]) || p.NextAfter == nil || *p.NextAfter != ids[1] {
		t.Errorf("page 1: %v next %v", idsOf(p), p.NextAfter)
	}
	p = get(fmt.Sprintf("limit=2&after=%d", ids[1]))
	if !slices.Equal(idsOf(p), ids[2:4]) || *p.NextAfter != ids[3] {
		t.Errorf("page 2: %v next %v", idsOf(p), p.NextAfter)
	}
	p = get(fmt.Sprintf("limit=2&after=%d", ids[3]))
	if !slices.Equal(idsOf(p), ids[4:]) || p.NextAfter != nil {
		t.Errorf("page 3: %v next %v", idsOf(p), p.NextAfter)
	}
	p = get(fmt.Sprintf("limit=1&after=%d", ids[3]))
	if !slices.Equal(idsOf(p), ids[4:]) || p.NextAfter != nil {
		t.Errorf("exact last page: %v next %v", idsOf(p), p.NextAfter)
	}

	// Name and path substring, case-insensitive; "_" and "%" are literal.
	if got := idsOf(get("q=SVC3")); !slices.Equal(got, ids[2:3]) {
		t.Errorf("q=SVC3: %v", got)
	}
	if got := idsOf(get("q=team/proj_")); !slices.Equal(got, ids) {
		t.Errorf("q=team/proj_: %v", got)
	}
	if got := idsOf(get("q=%25")); len(got) != 0 {
		t.Errorf("q=%%: %v", got)
	}
	if got := idsOf(get("q=nomatch")); got == nil || len(got) != 0 {
		t.Errorf("q=nomatch: %v", got)
	}
	rec := call(t, c, "listServices", "GET", "/?q=nomatch", "", "")
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("empty list body %s", rec.Body.String())
	}

	// Archived filter.
	if got := idsOf(get("archived=true")); !slices.Equal(got, []int64{old}) {
		t.Errorf("archived=true: %v", got)
	}
	if got := idsOf(get("archived=true&q=OLD%25")); !slices.Equal(got, []int64{old}) {
		t.Errorf("archived q: %v", got)
	}
	if got := idsOf(get("archived=true&q=svc")); len(got) != 0 {
		t.Errorf("archived q svc: %v", got)
	}
}

func TestGetService(t *testing.T) {
	c, pool, _ := newCatalog(t, "")
	mustExec(t, pool, `INSERT INTO services (name, gitlab_project_id, gitlab_project_path, sonar_project_key, created_via)
		VALUES ('n', 42, 'g/n', 'key', 'ingest')`)
	var id int64
	if err := pool.QueryRow(context.Background(), `SELECT id FROM services`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	s := decode[svcJSON](t, call(t, c, "getService", "GET", "/", "", fmt.Sprint(id)), http.StatusOK)
	if s.ID != id || s.Name != "n" || s.GitLabProjectID == nil || *s.GitLabProjectID != 42 || s.Path != "g/n" ||
		s.SonarKey == nil || *s.SonarKey != "key" || s.CreatedVia != "ingest" || s.Archived {
		t.Errorf("service = %+v", s)
	}
	if rec := call(t, c, "getService", "GET", "/", "", fmt.Sprint(id+1)); rec.Code != http.StatusNotFound {
		t.Errorf("missing: %d", rec.Code)
	}
}

type errJSON struct {
	Error struct{ Code, Message string } `json:"error"`
}

func TestCreateUpdateDeleteService(t *testing.T) {
	c, pool, rec := newCatalog(t, "")

	s := decode[svcJSON](t, call(t, c, "createService", "POST", "/", `{"gitlab_project_path":"platform/payments-api"}`, ""), http.StatusCreated)
	if s.Name != "payments-api" || s.Path != "platform/payments-api" || s.CreatedVia != "manual" || s.SonarKey != nil ||
		s.GitLabProjectID != nil || s.Archived {
		t.Fatalf("created = %+v", s)
	}
	s2 := decode[svcJSON](t, call(t, c, "createService", "POST", "/",
		`{"gitlab_project_path":"platform/ui","name":"UI","sonar_project_key":"platform:ui"}`, ""), http.StatusCreated)
	if s2.Name != "UI" || s2.SonarKey == nil || *s2.SonarKey != "platform:ui" {
		t.Fatalf("created 2 = %+v", s2)
	}

	// Conflicts name the taken value.
	e := decode[errJSON](t, call(t, c, "createService", "POST", "/", `{"gitlab_project_path":"platform/ui"}`, ""), http.StatusConflict)
	if e.Error.Code != "conflict" || !strings.Contains(e.Error.Message, "project path") {
		t.Errorf("path conflict = %+v", e)
	}
	// GitLab paths are case-insensitive: a path differing only in case is
	// the same project.
	e = decode[errJSON](t, call(t, c, "createService", "POST", "/", `{"gitlab_project_path":"Platform/UI"}`, ""), http.StatusConflict)
	if e.Error.Code != "conflict" || !strings.Contains(e.Error.Message, "project path") {
		t.Errorf("case-only path conflict = %+v", e)
	}
	e = decode[errJSON](t, call(t, c, "createService", "POST", "/",
		`{"gitlab_project_path":"platform/other","sonar_project_key":"platform:ui"}`, ""), http.StatusConflict)
	if e.Error.Code != "conflict" || !strings.Contains(e.Error.Message, "SonarQube") {
		t.Errorf("key conflict = %+v", e)
	}

	// Update: rename, link, archive.
	id := fmt.Sprint(s.ID)
	u := decode[svcJSON](t, call(t, c, "updateService", "PATCH", "/", `{"name":"Payments","sonar_project_key":"pay:key","archived":true}`, id), http.StatusOK)
	if u.Name != "Payments" || u.SonarKey == nil || *u.SonarKey != "pay:key" || !u.Archived || u.Path != "platform/payments-api" {
		t.Errorf("updated = %+v", u)
	}
	// Only the provided field changes; an absent key is left alone.
	u = decode[svcJSON](t, call(t, c, "updateService", "PATCH", "/", `{"archived":false}`, id), http.StatusOK)
	if u.Name != "Payments" || u.SonarKey == nil || *u.SonarKey != "pay:key" || u.Archived {
		t.Errorf("partial update = %+v", u)
	}
	// null unlinks.
	u = decode[svcJSON](t, call(t, c, "updateService", "PATCH", "/", `{"sonar_project_key":null}`, id), http.StatusOK)
	if u.SonarKey != nil || u.Name != "Payments" {
		t.Errorf("unlink = %+v", u)
	}
	e = decode[errJSON](t, call(t, c, "updateService", "PATCH", "/", `{"sonar_project_key":"platform:ui"}`, id), http.StatusConflict)
	if e.Error.Code != "conflict" || !strings.Contains(e.Error.Message, "SonarQube") {
		t.Errorf("update conflict = %+v", e)
	}
	if r := call(t, c, "updateService", "PATCH", "/", `{"archived":true}`, "99999"); r.Code != http.StatusNotFound {
		t.Errorf("update missing: %d", r.Code)
	}

	// Delete cascades.
	addScan(t, pool, scanSeed{svc: s.ID, kind: "image", artifact: "x", ref: "main", job: 1, at: time.Now()})
	addSonar(t, pool, s.ID, "t1", time.Now(), "OK", "A", "A", "A", nil)
	if r := call(t, c, "deleteService", "DELETE", "/", "", id); r.Code != http.StatusNoContent || r.Body.Len() != 0 {
		t.Fatalf("delete: %d %q", r.Code, r.Body.String())
	}
	var n int
	for _, q := range []string{`SELECT count(*) FROM scans`, `SELECT count(*) FROM sonar_analyses`} {
		if err := pool.QueryRow(context.Background(), q).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s = %d (%v)", q, n, err)
		}
	}
	if r := call(t, c, "deleteService", "DELETE", "/", "", id); r.Code != http.StatusNotFound {
		t.Errorf("delete again: %d", r.Code)
	}
	if r := call(t, c, "getService", "GET", "/", "", id); r.Code != http.StatusNotFound {
		t.Errorf("get deleted: %d", r.Code)
	}

	// Audit events.
	type ev = recordedEvent
	want := []struct {
		action string
		check  func(ev) bool
	}{
		{audit.ActionServiceCreated, func(e ev) bool {
			return e.detail["id"] == s.ID && e.detail["gitlab_project_path"] == "platform/payments-api"
		}},
		{audit.ActionServiceCreated, func(e ev) bool { return e.detail["id"] == s2.ID }},
		{audit.ActionServiceUpdated, func(e ev) bool {
			return slices.Equal(e.detail["fields"].([]string), []string{"name", "sonar_project_key", "archived"})
		}},
		{audit.ActionServiceUpdated, func(e ev) bool { return slices.Equal(e.detail["fields"].([]string), []string{"archived"}) }},
		{audit.ActionServiceUpdated, func(e ev) bool {
			return slices.Equal(e.detail["fields"].([]string), []string{"sonar_project_key"})
		}},
		{audit.ActionServiceDeleted, func(e ev) bool {
			return e.detail["id"] == s.ID && e.detail["gitlab_project_path"] == "platform/payments-api"
		}},
	}
	if len(rec.events) != len(want) {
		t.Fatalf("events = %+v", rec.events)
	}
	for i, w := range want {
		e := rec.events[i]
		if e.actor != "local:root" || e.action != w.action || !w.check(e) {
			t.Errorf("event %d = %+v", i, e)
		}
	}
}

// The real recorder writes the audit_log row.
func TestAuditRowWritten(t *testing.T) {
	pool := storetest.New(t)
	c := New(pool, fakeSettings{}, audit.New(store.New(pool), slog.New(slog.NewTextHandler(io.Discard, nil))),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	call(t, c, "createService", "POST", "/", `{"gitlab_project_path":"a/b"}`, "")
	var actor, action string
	var id int64
	err := pool.QueryRow(context.Background(),
		`SELECT actor, action, (detail->>'id')::bigint FROM audit_log`).Scan(&actor, &action, &id)
	if err != nil || actor != "local:root" || action != "service.created" || id == 0 {
		t.Errorf("audit row = %q %q %d (%v)", actor, action, id, err)
	}
}

type trivyJSON struct {
	Configured       bool    `json:"configured"`
	LastUploadAt     *string `json:"last_upload_at"`
	Uploads24h       int64   `json:"uploads_24h"`
	Rejections24h    int64   `json:"rejections_24h"`
	RecentRejections []struct {
		At          string  `json:"at"`
		Reason      string  `json:"reason"`
		ProjectPath *string `json:"project_path"`
	} `json:"recent_rejections"`
}

func TestTrivyIntegration(t *testing.T) {
	c, pool, _ := newCatalog(t, "https://gitlab.example.com")

	// Empty state.
	tr := decode[trivyJSON](t, call(t, c, "getTrivyIntegration", "GET", "/", "", ""), http.StatusOK)
	if !tr.Configured || tr.LastUploadAt != nil || tr.Uploads24h != 0 || tr.Rejections24h != 0 || tr.RecentRejections == nil || len(tr.RecentRejections) != 0 {
		t.Errorf("empty = %+v", tr)
	}
	if rec := call(t, c, "getTrivyIntegration", "GET", "/", "", ""); !strings.Contains(rec.Body.String(), `"recent_rejections":[]`) {
		t.Errorf("body %s", rec.Body.String())
	}
	unconfigured, _, _ := newCatalog(t, "")
	if tr := decode[trivyJSON](t, call(t, unconfigured, "getTrivyIntegration", "GET", "/", "", ""), http.StatusOK); tr.Configured {
		t.Error("configured with empty GitLab URL")
	}

	svc := addService(t, pool, "s", "g/s", false)
	// PostgreSQL keeps microseconds; Linux clocks have nanoseconds.
	now := time.Now().Truncate(time.Microsecond)
	for i, ago := range []time.Duration{time.Hour, 5 * time.Hour, 30 * time.Hour, 48 * time.Hour} {
		addScan(t, pool, scanSeed{svc: svc, kind: "image", artifact: "x", ref: "main", job: int64(i + 1), at: now.Add(-ago)})
		mustExec(t, pool, `UPDATE scans SET received_at = $1 WHERE gitlab_job_id = $2`, now.Add(-ago), i+1)
	}
	// 25 rejections: 22 within 24 h (one without a project path), 3 older.
	for i := 0; i < 25; i++ {
		at := now.Add(-time.Duration(i+1) * time.Minute)
		detail := fmt.Sprintf(`{"reason":"reason-%d","project_path":"g/p%d"}`, i, i)
		switch {
		case i == 0:
			detail = `{"reason":"invalid_token"}`
		case i >= 22:
			at = now.Add(-time.Duration(30+i) * time.Hour)
		}
		mustExec(t, pool, `INSERT INTO audit_log (at, actor, action, detail) VALUES ($1, 'ingest', 'ingest.rejected', $2)`, at, detail)
	}
	// Other actions are not rejections.
	mustExec(t, pool, `INSERT INTO audit_log (actor, action) VALUES ('local:root', 'service.created')`)

	tr = decode[trivyJSON](t, call(t, c, "getTrivyIntegration", "GET", "/", "", ""), http.StatusOK)
	if tr.Uploads24h != 2 || tr.Rejections24h != 22 {
		t.Errorf("counts = %d uploads, %d rejections; want 2 and 22", tr.Uploads24h, tr.Rejections24h)
	}
	sameTime(t, "last_upload_at", tr.LastUploadAt, now.Add(-time.Hour))
	if len(tr.RecentRejections) != 20 {
		t.Fatalf("recent = %d", len(tr.RecentRejections))
	}
	if r := tr.RecentRejections[0]; r.Reason != "invalid_token" || r.ProjectPath != nil {
		t.Errorf("newest = %+v", r)
	}
	if r := tr.RecentRejections[1]; r.Reason != "reason-1" || r.ProjectPath == nil || *r.ProjectPath != "g/p1" {
		t.Errorf("second = %+v", r)
	}
	if r := tr.RecentRejections[19]; r.Reason != "reason-19" {
		t.Errorf("oldest shown = %+v", r)
	}
}

func TestPortfolioPerformance(t *testing.T) {
	c, pool, _ := newCatalog(t, "")
	ctx := context.Background()
	mustExec(t, pool, `INSERT INTO services (name, gitlab_project_path, created_via)
		SELECT 'svc' || g, 'grp/svc' || g, 'manual' FROM generate_series(1, 500) g`)
	mustExec(t, pool, `INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref,
		ref_protected, commit_sha, schema_version, scanned_at, count_critical, count_high, count_medium, count_low,
		count_unknown, count_fixable, count_secrets, raw_report)
		SELECT s.id, CASE WHEN a = 3 THEN 'secret-fs' ELSE 'image' END, 'artifact' || a, j, j,
		       CASE WHEN j % 5 = 0 THEN 'feature-' || (j % 3) ELSE 'main' END, false, 'abc', 2,
		       now() - ((30 - j) * interval '1 hour') - (a * interval '1 minute'),
		       j % 3, j % 5, j % 7, j % 11, j % 2, j % 4, j % 6, '\x1f8b'
		FROM services s, generate_series(1, 3) a, generate_series(1, 30) j`)
	// A third of the services have a chosen dashboard branch, so the
	// measured statement includes both branches of the resolution rule.
	mustExec(t, pool, `UPDATE services SET default_ref = 'main' WHERE id % 3 = 0`)
	mustExec(t, pool, `INSERT INTO sonar_analyses (service_id, task_id, analysed_at, quality_gate_status,
		reliability_rating, security_rating, maintainability_rating, coverage)
		SELECT s.id, 't-' || s.id || '-' || k, now() - (k * interval '1 day'),
		       CASE WHEN s.id % 4 = 0 THEN 'ERROR' ELSE 'OK' END, 'A', 'B', 'C', 50 + k
		FROM services s, generate_series(1, 3) k`)
	mustExec(t, pool, `ANALYZE`)

	var scans int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM scans`).Scan(&scans); err != nil || scans != 45000 {
		t.Fatalf("scans = %d (%v)", scans, err)
	}

	// Check the numbers of one service against a direct computation.
	pf := decode[pfResponse](t, call(t, c, "getPortfolio", "GET", "/", "", ""), http.StatusOK)
	if pf.Totals.Services != 500 || len(pf.Items) != 500 {
		t.Fatalf("services = %d/%d", pf.Totals.Services, len(pf.Items))
	}
	// j = 30 is the newest (on ref feature-0, since 30 % 3 = 0); three
	// artifacts at j=30: crit 0, high 0, med 2, low 8, unk 0, fixable 2, secrets 0.
	it := pf.Items[0]
	if str(it.Ref) != "feature-0" || it.RefSource != "latest" || it.Counts != (counts{Critical: 0, High: 0, Medium: 6, Low: 24, Unknown: 0}) ||
		it.Fixable != 6 || it.Secrets != 0 || str(it.QualityGateStatus) == "<nil>" {
		t.Errorf("first item = %+v", it)
	}

	const runs = 50
	durs := make([]time.Duration, runs)
	for i := range durs {
		start := time.Now()
		rec := call(t, c, "getPortfolio", "GET", "/", "", "")
		durs[i] = time.Since(start)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
	}
	slices.Sort(durs)
	p50, p95 := durs[runs/2], durs[(runs*95+99)/100-1]
	t.Logf("portfolio, 500 services x 3 artifacts x 30 scans: p50 %v, p95 %v, max %v", p50, p95, durs[runs-1])
	if p95 >= 150*time.Millisecond {
		var plan strings.Builder
		rows, err := pool.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+portfolioSQL)
		if err == nil {
			for rows.Next() {
				var line string
				_ = rows.Scan(&line)
				plan.WriteString(line + "\n")
			}
			rows.Close()
		}
		t.Fatalf("p95 %v >= 150ms\n%s", p95, plan.String())
	}
}

func TestPortfolioUsesChosenRefPerService(t *testing.T) {
	c, pool, _ := newCatalog(t, "")
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	chosen := addService(t, pool, "a-chosen", "g/a", false)
	addScan(t, pool, scanSeed{svc: chosen, kind: "image", artifact: "x", ref: "main", job: 1, at: at, crit: 1})
	addScan(t, pool, scanSeed{svc: chosen, kind: "image", artifact: "x", ref: "feature/x", job: 2, at: at.Add(time.Hour), crit: 7})
	mustExec(t, pool, `UPDATE services SET default_ref = 'main' WHERE id = $1`, chosen)
	unchosen := addService(t, pool, "b-unchosen", "g/b", false)
	addScan(t, pool, scanSeed{svc: unchosen, kind: "image", artifact: "x", ref: "main", job: 3, at: at, crit: 1})
	addScan(t, pool, scanSeed{svc: unchosen, kind: "image", artifact: "x", ref: "feature/x", job: 4, at: at.Add(time.Hour), crit: 7})
	// A chosen branch without scans shows an empty state, not another branch.
	empty := addService(t, pool, "c-empty", "g/c", false)
	addScan(t, pool, scanSeed{svc: empty, kind: "image", artifact: "x", ref: "main", job: 5, at: at, crit: 4})
	mustExec(t, pool, `UPDATE services SET default_ref = 'release' WHERE id = $1`, empty)
	// A chosen branch on a service that was never scanned.
	fresh := addService(t, pool, "d-fresh", "g/d", false)
	mustExec(t, pool, `UPDATE services SET default_ref = 'main' WHERE id = $1`, fresh)

	pf := decode[pfResponse](t, call(t, c, "getPortfolio", "GET", "/", "", ""), http.StatusOK)
	if len(pf.Items) != 4 {
		t.Fatalf("items = %d", len(pf.Items))
	}
	for i, w := range []struct {
		ref, source string
		crit        int64
		scanned     bool
	}{
		{"main", "default", 1, true},
		{"feature/x", "latest", 7, true},
		{"release", "default", 0, false},
		{"main", "default", 0, false},
	} {
		it := pf.Items[i]
		if str(it.Ref) != w.ref || it.RefSource != w.source || it.Counts.Critical != w.crit || (it.LastScanAt != nil) != w.scanned {
			t.Errorf("item %d = ref %s source %s crit %d scanned %v, want %+v",
				i, str(it.Ref), it.RefSource, it.Counts.Critical, it.LastScanAt != nil, w)
		}
	}
	if it := pf.Items[0]; it.Service.ID != chosen {
		t.Errorf("order: %+v", it)
	}
	// An unscanned, unchosen service has no ref.
	bare := addService(t, pool, "e-bare", "g/e", false)
	pf = decode[pfResponse](t, call(t, c, "getPortfolio", "GET", "/", "", ""), http.StatusOK)
	if it := pf.Items[4]; it.Service.ID != bare || it.Ref != nil || it.RefSource != "latest" {
		t.Errorf("bare = %+v", it)
	}
}

func TestUpdateServiceDefaultRef(t *testing.T) {
	c, _, rec := newCatalog(t, "")
	s := decode[svcJSON](t, call(t, c, "createService", "POST", "/", `{"gitlab_project_path":"platform/api"}`, ""), http.StatusCreated)
	if s.DefaultRef != nil {
		t.Fatalf("new service has a default ref: %v", *s.DefaultRef)
	}
	id := fmt.Sprint(s.ID)

	// The ref is trimmed; it need not have scans.
	u := decode[svcJSON](t, call(t, c, "updateService", "PATCH", "/", `{"default_ref":"  main  "}`, id), http.StatusOK)
	if u.DefaultRef == nil || *u.DefaultRef != "main" {
		t.Fatalf("set = %+v", u)
	}
	g := decode[svcJSON](t, call(t, c, "getService", "GET", "/", "", id), http.StatusOK)
	if g.DefaultRef == nil || *g.DefaultRef != "main" {
		t.Errorf("get = %+v", g)
	}
	// Other updates leave it alone.
	u = decode[svcJSON](t, call(t, c, "updateService", "PATCH", "/", `{"name":"API"}`, id), http.StatusOK)
	if u.DefaultRef == nil || *u.DefaultRef != "main" {
		t.Errorf("rename dropped the default ref: %+v", u)
	}
	u = decode[svcJSON](t, call(t, c, "updateService", "PATCH", "/", `{"default_ref":"release/1.x"}`, id), http.StatusOK)
	if u.DefaultRef == nil || *u.DefaultRef != "release/1.x" {
		t.Errorf("change = %+v", u)
	}
	// null clears.
	u = decode[svcJSON](t, call(t, c, "updateService", "PATCH", "/", `{"default_ref":null}`, id), http.StatusOK)
	if u.DefaultRef != nil {
		t.Errorf("clear = %+v", u)
	}

	// Invalid values are 400 and change nothing.
	long := strings.Repeat("a", 256)
	for name, body := range map[string]string{
		"empty":      `{"default_ref":""}`,
		"blank":      `{"default_ref":"   "}`,
		"too long":   fmt.Sprintf(`{"default_ref":%q}`, long),
		"control":    `{"default_ref":"ma\u0000in"}`,
		"newline":    `{"default_ref":"ma\nin"}`,
		"wrong type": `{"default_ref":5}`,
	} {
		r := call(t, c, "updateService", "PATCH", "/", body, id)
		if r.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", name, r.Code, r.Body.String())
		}
	}
	if r := call(t, c, "updateService", "PATCH", "/", fmt.Sprintf(`{"default_ref":%q}`, long[:255]), id); r.Code != http.StatusOK {
		t.Errorf("255 characters: %d", r.Code)
	}
	if r := call(t, c, "updateService", "PATCH", "/", `{"default_ref":"x"}`, "99999"); r.Code != http.StatusNotFound {
		t.Errorf("unknown service: %d", r.Code)
	}

	// Audit: every successful change records the fields and the old and new value.
	type change struct{ From, To *string }
	var got []change
	for _, e := range rec.events {
		if e.action != audit.ActionServiceUpdated {
			continue
		}
		d, ok := e.detail["default_ref"].(map[string]any)
		if !ok {
			if slices.Contains(e.detail["fields"].([]string), "default_ref") {
				t.Errorf("default_ref listed without old and new value: %+v", e.detail)
			}
			continue
		}
		if !slices.Equal(e.detail["fields"].([]string), []string{"default_ref"}) {
			t.Errorf("fields = %v", e.detail["fields"])
		}
		got = append(got, change{d["from"].(*string), d["to"].(*string)})
	}
	sp := func(v string) *string { return &v }
	want := []change{{nil, sp("main")}, {sp("main"), sp("release/1.x")}, {sp("release/1.x"), nil}, {nil, sp(long[:255])}}
	if len(got) != len(want) {
		t.Fatalf("audit changes = %+v", got)
	}
	for i := range want {
		if (got[i].From == nil) != (want[i].From == nil) || (got[i].To == nil) != (want[i].To == nil) ||
			(want[i].From != nil && *got[i].From != *want[i].From) || (want[i].To != nil && *got[i].To != *want[i].To) {
			t.Errorf("change %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
