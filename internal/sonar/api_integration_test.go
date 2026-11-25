// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package sonar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/testutil/fakesonar"
)

// get calls a read handler the way the server mux does, so path values work.
func (e *env) get(t *testing.T, op, path string) (int, string, http.Header) {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/services/{id}/sonar", e.s.Handlers()["getServiceSonar"])
	mux.Handle("GET /api/v1/admin/integrations/sonarqube", e.s.Handlers()["getSonarIntegration"])
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	return rec.Code, rec.Body.String(), rec.Header()
}

// sameJSON compares a body with an expected JSON document, ignoring key order.
func sameJSON(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("response is not JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// The full path: webhook, queue, Web API, PostgreSQL, read endpoint.
func TestWebhookToReadEndpoint(t *testing.T) {
	e := newEnv(t)
	e.s.now = func() time.Time { return time.Date(2026, 11, 19, 10, 5, 0, 0, time.UTC) }
	sid := e.service(t, "platform/payments-api", 0, "platform:payments-api")
	e.project("platform:payments-api")
	props := map[string]string{"sonar.analysis.pipelineId": "9001"}
	wantStatus(t, e, hook("task-1", "platform:payments-api", map[string]any{"properties": props}), 202)
	e.drain(t, 1)

	code, body, hdr := e.get(t, "getServiceSonar", "/api/v1/services/"+strconv.FormatInt(sid, 10)+"/sonar")
	if code != 200 || hdr.Get("Content-Type") != "application/json" || hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", code, hdr)
	}
	analysis := func(id int64) string {
		return `{"id":` + strconv.FormatInt(id, 10) + `,"analysed_at":"2026-11-19T10:00:00Z","revision":"abc123","quality_gate_status":"ERROR",
		"conditions":[
		 {"metric":"new_coverage","operator":"LESS_THAN","threshold":"80","value":"70.5","status":"ERROR"},
		 {"metric":"new_violations","operator":"GREATER_THAN","threshold":"0","value":null,"status":"NO_VALUE"}],
		"reliability_rating":"A","security_rating":"B","maintainability_rating":"A","security_review_rating":"A",
		"coverage":81.3,"duplicated_lines_density":2.4,
		"measures":{"alert_status":"OK","coverage":"81.3","duplicated_lines_density":"2.4","ncloc":"12345","new_coverage":"90.5",
		 "security_hotspots":"4","security_hotspots_reviewed":"75.0","security_review_rating":"1.0",
		 "software_quality_maintainability_issues":"42","software_quality_maintainability_rating":"1.0",
		 "software_quality_reliability_issues":"0","software_quality_reliability_rating":"1.0",
		 "software_quality_security_issues":"3","software_quality_security_rating":"2.0"},
		"mode":"mqr","gitlab_pipeline_id":9001,"project_url":"` + e.fake.URL + `/dashboard?id=platform%3Apayments-api"}`
	}
	rows := e.analyses(t, sid)
	a := analysis(rows[0].ID)
	sameJSON(t, body, `{"sonar_project_key":"platform:payments-api","latest":`+a+`,"history":[`+a+`]}`)

	// The integration page shows the delivery.
	code, body, _ = e.get(t, "getSonarIntegration", "/api/v1/admin/integrations/sonarqube")
	if code != 200 {
		t.Fatal(code)
	}
	sameJSON(t, body, `{"configured":true,"server_version":null,"last_webhook_at":"2026-11-19T10:05:00Z","last_sync_at":null,"last_sync_error":null,
	 "recent_deliveries":[{"task_id":"task-1","project_key":"platform:payments-api","received_at":"2026-11-19T10:05:00Z","outcome":"linked","service_id":`+
		strconv.FormatInt(sid, 10)+`,"service_name":"platform/payments-api","detail":""}]}`)
}

func TestServiceSonarUnreportedAndEmpty(t *testing.T) {
	e := newEnv(t)
	url := func(id int64) string { return "/api/v1/services/" + strconv.FormatInt(id, 10) + "/sonar" }

	// No key, no analyses.
	plain := e.service(t, "platform/plain", 0, "")
	code, body, _ := e.get(t, "", url(plain))
	if code != 200 {
		t.Fatal(code)
	}
	sameJSON(t, body, `{"sonar_project_key":null,"latest":null,"history":[]}`)

	// Linked, not analysed yet, SonarQube URL not configured: no link.
	sid := e.service(t, "platform/payments-api", 0, "platform:payments-api")
	e.cfg.set(func(s *settings.Snapshot) { s.SonarURL = "" })
	_, body, _ = e.get(t, "", url(sid))
	sameJSON(t, body, `{"sonar_project_key":"platform:payments-api","latest":null,"history":[]}`)

	// Standard mode with coverage, sqale and security review not reported.
	m := fakesonar.StandardMeasures()
	delete(m, "coverage")
	delete(m, "sqale_rating")
	delete(m, "security_review_rating")
	delete(m, "duplicated_lines_density")
	if _, err := e.s.save(t.Context(), snapshot{
		ServiceID: sid, TaskID: "sync:X1", AnalysedAt: analysisTime,
		Measures: m, Conditions: nil,
	}, ""); err != nil {
		t.Fatal(err)
	}
	_, body, _ = e.get(t, "", url(sid))
	var got struct {
		Latest map[string]any `json:"latest"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	l := got.Latest
	for _, k := range []string{"coverage", "duplicated_lines_density", "maintainability_rating", "security_review_rating", "gitlab_pipeline_id", "project_url"} {
		if v, ok := l[k]; !ok || v != nil {
			t.Errorf("%s = %v (present %v), want null", k, v, ok)
		}
	}
	if l["mode"] != "standard" || l["reliability_rating"] != "C" || l["security_rating"] != "A" || l["quality_gate_status"] != "" || l["revision"] != "" {
		t.Errorf("latest: %v", l)
	}
	if c, ok := l["conditions"].([]any); !ok || len(c) != 0 {
		t.Errorf("conditions must be an empty array: %v", l["conditions"])
	}
}

func TestServiceSonarHistoryAndErrors(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "platform/payments-api", 0, "platform:payments-api")
	for i := range 3 {
		_, err := e.s.save(t.Context(), snapshot{
			ServiceID: sid, TaskID: "sync:H" + strconv.Itoa(i), AnalysedAt: analysisTime.Add(time.Duration(i) * time.Hour),
			Measures: map[string]string{}, Revision: "r" + strconv.Itoa(i),
		}, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	revisions := func(query string) []string {
		code, body, _ := e.get(t, "", "/api/v1/services/"+strconv.FormatInt(sid, 10)+"/sonar"+query)
		if code != 200 {
			t.Fatalf("%s: %d %s", query, code, body)
		}
		var got struct {
			Latest  struct{ Revision string }
			History []struct{ Revision string }
		}
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, h := range got.History {
			out = append(out, h.Revision)
		}
		if got.Latest.Revision != out[0] {
			t.Errorf("latest %q is not the first history entry %q", got.Latest.Revision, out[0])
		}
		return out
	}
	if got := revisions(""); !reflect.DeepEqual(got, []string{"r2", "r1", "r0"}) {
		t.Errorf("default: %v", got)
	}
	if got := revisions("?limit=2"); !reflect.DeepEqual(got, []string{"r2", "r1"}) {
		t.Errorf("limit 2: %v", got)
	}
	if got := revisions("?limit=0"); len(got) != 3 {
		t.Errorf("invalid limit falls back to the default: %v", got)
	}
	if got := revisions("?limit=99999"); len(got) != 3 {
		t.Errorf("large limit: %v", got)
	}

	for _, p := range []string{"/api/v1/services/9999/sonar", "/api/v1/services/abc/sonar"} {
		code, body, _ := e.get(t, "", p)
		if code != 404 {
			t.Errorf("%s: %d", p, code)
		}
		sameJSON(t, body, `{"error":{"code":"not_found","message":"No such service."}}`)
	}
}

func TestSonarIntegrationHealth(t *testing.T) {
	e := newEnv(t)
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }

	_, body, _ := e.get(t, "", "/api/v1/admin/integrations/sonarqube")
	sameJSON(t, body, `{"configured":true,"server_version":null,"last_webhook_at":null,"last_sync_at":null,"last_sync_error":null,"recent_deliveries":[]}`)

	e.cfg.set(func(s *settings.Snapshot) { s.SonarToken = "" })
	_, body, _ = e.get(t, "", "/api/v1/admin/integrations/sonarqube")
	sameJSON(t, body, `{"configured":false,"server_version":null,"last_webhook_at":null,"last_sync_at":null,"last_sync_error":null,"recent_deliveries":[]}`)
	e.cfg.set(func(s *settings.Snapshot) { s.SonarToken = testToken })

	e.service(t, "platform/a", 0, "platform:a")
	e.fake.Fail("api/project_analyses/search", 500)
	e.runSync(t)
	_, body, _ = e.get(t, "", "/api/v1/admin/integrations/sonarqube")
	sameJSON(t, body, `{"configured":true,"server_version":"26.9.0.12345","last_webhook_at":null,"last_sync_at":"2026-11-19T12:00:00Z",
	 "last_sync_error":"platform:a: SonarQube answered 500 on api/project_analyses/search","recent_deliveries":[]}`)

	// Only the 50 newest deliveries are listed, newest first.
	for i := range 52 {
		now = now.Add(time.Second)
		wantStatus(t, e, hook("d-"+strconv.Itoa(i), "unknown:key", nil), 202)
	}
	_, body, _ = e.get(t, "", "/api/v1/admin/integrations/sonarqube")
	var got struct {
		LastWebhookAt    string `json:"last_webhook_at"`
		RecentDeliveries []struct {
			TaskID string `json:"task_id"`
		} `json:"recent_deliveries"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.RecentDeliveries) != 50 || got.RecentDeliveries[0].TaskID != "d-51" || got.RecentDeliveries[49].TaskID != "d-2" ||
		got.LastWebhookAt != "2026-11-19T12:00:52Z" {
		t.Errorf("%d deliveries, first %q, last webhook %s", len(got.RecentDeliveries), got.RecentDeliveries[0].TaskID, got.LastWebhookAt)
	}
}
