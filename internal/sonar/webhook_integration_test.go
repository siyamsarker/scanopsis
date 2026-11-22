// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package sonar

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/testutil/fakesonar"
)

func wantStatus(t *testing.T, e *env, body []byte, status int) {
	t.Helper()
	if rec := post(e.h, testSecret, body); rec.Code != status {
		t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body)
	}
}

func TestWebhookLinkedByKeyMQR(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "platform/payments-api", 0, "platform:payments-api")
	e.project("platform:payments-api")

	wantStatus(t, e, hook("task-1", "platform:payments-api", nil), 202)
	e.drain(t, 1)

	d := e.delivery(t, "task-1")
	if d.Outcome != "linked" || d.Detail != "" || !d.ServiceID.Valid || d.ServiceID.Int64 != sid || d.ProjectKey != "platform:payments-api" {
		t.Errorf("delivery: %+v", d)
	}
	rows := e.analyses(t, sid)
	if len(rows) != 1 {
		t.Fatalf("%d analyses", len(rows))
	}
	a := rows[0]
	if a.TaskID != "task-1" || a.AnalysisKey != "AY1" || !a.AnalysedAt.Time.Equal(analysisTime) || a.Revision != "abc123" ||
		a.QualityGateStatus != "ERROR" || a.Mode != "mqr" {
		t.Errorf("analysis: %+v", a)
	}
	if a.ReliabilityRating != "A" || a.SecurityRating != "B" || a.MaintainabilityRating != "A" || a.SecurityReviewRating != "A" {
		t.Errorf("ratings: %+v", a)
	}
	if !a.Coverage.Valid || a.Coverage.Float64 != 81.3 || !a.DuplicatedLinesDensity.Valid || a.DuplicatedLinesDensity.Float64 != 2.4 {
		t.Errorf("percentages: %+v %+v", a.Coverage, a.DuplicatedLinesDensity)
	}
	if a.GitlabPipelineID.Valid {
		t.Errorf("pipeline id must be null: %+v", a.GitlabPipelineID)
	}
	wantConds := `[{"metric":"new_coverage","operator":"LESS_THAN","threshold":"80","value":"70.5","status":"ERROR"},` +
		`{"metric":"new_violations","operator":"GREATER_THAN","threshold":"0","value":null,"status":"NO_VALUE"}]`
	var gotC, wantC any
	if err := json.Unmarshal(a.GateConditions, &gotC); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(wantConds), &wantC)
	if !reflect.DeepEqual(gotC, wantC) {
		t.Errorf("conditions: %s", a.GateConditions)
	}
	var m map[string]string
	if err := json.Unmarshal(a.Measures, &m); err != nil || !reflect.DeepEqual(m, fakesonar.MQRMeasures()) {
		t.Errorf("measures: %v %v", m, err)
	}
}

func TestWebhookLinkedByPropertyStandardUnreported(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "platform/payments-api", 42, "")
	m := fakesonar.StandardMeasures()
	delete(m, "coverage")
	delete(m, "sqale_rating")
	e.fake.Set("any-key", fakesonar.Project{
		Measures: m, GateStatus: "OK",
		Analyses: []fakesonar.Analysis{{Key: "AY9", Date: analysisTime}},
	})
	props := map[string]string{"sonar.analysis.gitlabProjectId": "42", "sonar.analysis.pipelineId": "9001"}

	wantStatus(t, e, hook("task-2", "any-key", map[string]any{"properties": props, "revision": ""}), 202)
	e.drain(t, 1)

	if d := e.delivery(t, "task-2"); d.Outcome != "linked" || d.ServiceID.Int64 != sid {
		t.Errorf("delivery: %+v", d)
	}
	a := e.analyses(t, sid)[0]
	if a.Mode != "standard" || a.ReliabilityRating != "C" || a.SecurityRating != "A" || a.MaintainabilityRating != "" {
		t.Errorf("standard mode: %+v", a)
	}
	if a.Coverage.Valid {
		t.Errorf("coverage must be NULL when not reported: %+v", a.Coverage)
	}
	if !a.DuplicatedLinesDensity.Valid || a.DuplicatedLinesDensity.Float64 != 2.4 {
		t.Errorf("duplication: %+v", a.DuplicatedLinesDensity)
	}
	if !a.GitlabPipelineID.Valid || a.GitlabPipelineID.Int64 != 9001 {
		t.Errorf("pipeline: %+v", a.GitlabPipelineID)
	}
	if k, ok := e.serviceKey(t, sid); !ok || k != "any-key" {
		t.Errorf("service key %q %v, want it pinned", k, ok)
	}
	if a.Revision != "" {
		t.Errorf("revision: %q (the analysis search has none)", a.Revision)
	}
	if a.QualityGateStatus != "ERROR" { // from the signed payload, not from the API
		t.Errorf("gate: %q", a.QualityGateStatus)
	}
}

func TestWebhookPropertyFallsBackToKey(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "platform/payments-api", 0, "platform:payments-api")
	e.project("platform:payments-api")
	for i, prop := range []string{"not-a-number", "999"} {
		task := "task-fb-" + string(rune('a'+i))
		props := map[string]string{"sonar.analysis.gitlabProjectId": prop, "sonar.analysis.pipelineId": "abc"}
		wantStatus(t, e, hook(task, "platform:payments-api", map[string]any{"properties": props}), 202)
		e.drain(t, 1)
		if d := e.delivery(t, task); d.Outcome != "linked" || d.ServiceID.Int64 != sid {
			t.Errorf("%s: %+v", prop, d)
		}
	}
	for _, a := range e.analyses(t, sid) {
		if a.GitlabPipelineID.Valid {
			t.Errorf("non-numeric pipelineId must be ignored: %+v", a.GitlabPipelineID)
		}
	}
}

func TestWebhookUnlinked(t *testing.T) {
	e := newEnv(t)
	e.project("other:project")
	wantStatus(t, e, hook("task-3", "other:project", nil), 202)
	e.drain(t, 1)
	d := e.delivery(t, "task-3")
	want := `No service is linked to SonarQube project "other:project". Pass -Dsonar.analysis.gitlabProjectId=$CI_PROJECT_ID to the scanner, or set the SonarQube key on the service.`
	if d.Outcome != "unlinked" || d.ServiceID.Valid || d.Detail != want {
		t.Errorf("delivery: %+v", d)
	}
	if n := e.fake.Count("api/measures/component"); n != 0 {
		t.Errorf("an unlinked delivery must not call the Web API (%d calls)", n)
	}
}

func TestWebhookFailedTask(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "platform/payments-api", 0, "platform:payments-api")
	e.project("platform:payments-api")
	wantStatus(t, e, hook("task-4", "platform:payments-api", map[string]any{"status": "FAILED"}), 202)
	e.drain(t, 1)
	d := e.delivery(t, "task-4")
	if d.Outcome != "failed" || d.Detail != `The analysis task ended with status "FAILED"; nothing was stored.` || d.ServiceID.Int64 != sid {
		t.Errorf("delivery: %+v", d)
	}
	if n := len(e.analyses(t, sid)); n != 0 {
		t.Errorf("%d analyses stored", n)
	}
}

func TestWebhookWebAPIErrors(t *testing.T) {
	cases := []struct {
		name, endpoint string
		status         int
		detail         string
	}{
		{"measures 500", "api/measures/component", 500, "Reading measures failed: SonarQube answered 500 on api/measures/component. The next sync will retry."},
		{"measures 404", "api/measures/component", 404, "Reading measures failed: SonarQube answered 404 on api/measures/component. The next sync will retry."},
		{"search 403", "api/project_analyses/search", 403, "Reading the latest analysis failed: SonarQube answered 403 on api/project_analyses/search. The next sync will retry."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			sid := e.service(t, "platform/payments-api", 0, "platform:payments-api")
			e.project("platform:payments-api")
			e.fake.Fail(c.endpoint, c.status)
			wantStatus(t, e, hook("task-5", "platform:payments-api", nil), 202)
			e.drain(t, 1)
			d := e.delivery(t, "task-5")
			if d.Outcome != "failed" || d.Detail != c.detail || strings.Contains(d.Detail, testToken) {
				t.Errorf("delivery: %+v", d)
			}
			if n := len(e.analyses(t, sid)); n != 0 {
				t.Errorf("%d analyses stored", n)
			}
		})
	}
}

func TestWebhookWrongToken(t *testing.T) {
	e := newEnv(t)
	e.service(t, "platform/payments-api", 0, "platform:payments-api")
	e.project("platform:payments-api")
	e.cfg.set(func(s *settings.Snapshot) { s.SonarToken = "squ_wrong" })
	wantStatus(t, e, hook("task-6", "platform:payments-api", nil), 202)
	e.drain(t, 1)
	d := e.delivery(t, "task-6")
	if d.Outcome != "failed" || !strings.Contains(d.Detail, "answered 401") || strings.Contains(d.Detail, "squ_wrong") {
		t.Errorf("delivery: %+v", d)
	}
}

func TestWebhookDuplicate(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "platform/payments-api", 0, "platform:payments-api")
	e.project("platform:payments-api")
	body := hook("task-7", "platform:payments-api", nil)
	wantStatus(t, e, body, 202)
	wantStatus(t, e, body, 202)
	e.drain(t, 1)
	select {
	case <-e.s.queue:
		t.Fatal("the duplicate was queued")
	default:
	}
	if n := len(e.analyses(t, sid)); n != 1 {
		t.Errorf("%d analyses", n)
	}
	if n := e.fake.Count("api/measures/component"); n != 1 {
		t.Errorf("measures read %d times", n)
	}
	rows, _ := e.q.ListSonarDeliveries(context.Background())
	if len(rows) != 1 {
		t.Errorf("%d deliveries", len(rows))
	}
}

func TestWebhookAnalysisCases(t *testing.T) {
	newer := analysisTime.Add(time.Hour)
	cases := []struct {
		name     string
		analyses []fakesonar.Analysis
		over     map[string]any
		outcome  string
		detail   string
		key      string
	}{
		{"newer exists", []fakesonar.Analysis{{Key: "AY2", Date: newer}},
			nil, "linked", "A newer analysis (2026-11-19T11:00:00Z) already exists in SonarQube; the measures are from it. The next sync will add it.", ""},
		{"older latest", []fakesonar.Analysis{{Key: "AY0", Date: analysisTime.Add(-time.Hour)}},
			nil, "linked", "The latest analysis in SonarQube is older than the one in the webhook; the measures may not belong to it.", ""},
		{"no analyses", nil,
			nil, "linked", "SonarQube reports no analysis for the project; the measures may not belong to this analysis.", ""},
		{"bad analysedAt", []fakesonar.Analysis{{Key: "AY1", Date: analysisTime}},
			map[string]any{"analysedAt": "soon"}, "failed", `The webhook has no readable analysedAt.`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			sid := e.service(t, "platform/payments-api", 0, "platform:payments-api")
			e.fake.Set("platform:payments-api", fakesonar.Project{Measures: fakesonar.MQRMeasures(), GateStatus: "OK", Analyses: c.analyses})
			wantStatus(t, e, hook("task-8", "platform:payments-api", c.over), 202)
			e.drain(t, 1)
			d := e.delivery(t, "task-8")
			if d.Outcome != c.outcome || d.Detail != c.detail {
				t.Errorf("delivery: %+v", d)
			}
			rows := e.analyses(t, sid)
			if c.outcome == "failed" {
				if len(rows) != 0 {
					t.Errorf("%d analyses", len(rows))
				}
				return
			}
			if len(rows) != 1 || rows[0].AnalysisKey != c.key || !rows[0].AnalysedAt.Time.Equal(analysisTime) || rows[0].QualityGateStatus != "ERROR" {
				t.Errorf("rows: %+v", rows)
			}
		})
	}
}

func TestWebhookAlreadyStoredBySync(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "platform/payments-api", 0, "platform:payments-api")
	e.project("platform:payments-api")
	if err := e.s.save(context.Background(), snapshot{
		ServiceID: sid, TaskID: "sync:AY1", AnalysisKey: "AY1", AnalysedAt: analysisTime,
		Measures: map[string]string{}, Conditions: []Condition{},
	}, ""); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, e, hook("task-9", "platform:payments-api", nil), 202)
	e.drain(t, 1)
	if d := e.delivery(t, "task-9"); d.Outcome != "linked" || d.Detail != "The analysis was already stored by a sync." {
		t.Errorf("delivery: %+v", d)
	}
	if n := len(e.analyses(t, sid)); n != 1 {
		t.Errorf("%d analyses", n)
	}
}

func TestWebhookNotConfiguredAtProcessing(t *testing.T) {
	e := newEnv(t)
	e.service(t, "platform/payments-api", 0, "platform:payments-api")
	wantStatus(t, e, hook("task-10", "platform:payments-api", nil), 202)
	e.cfg.set(func(s *settings.Snapshot) { s.SonarToken = "" })
	e.drain(t, 1)
	d := e.delivery(t, "task-10")
	if d.Outcome != "failed" || d.Detail != "The SonarQube URL and token are not configured, so the analysis cannot be read." {
		t.Errorf("delivery: %+v", d)
	}
}

func TestWebhookQueueFull(t *testing.T) {
	e := newEnv(t)
	e.s.queue = make(chan delivery, 1)
	wantStatus(t, e, hook("task-11", "k", nil), 202)
	wantStatus(t, e, hook("task-12", "k", nil), 202)
	// The delivery that did not fit is not lost: it stays pending, marked as
	// waiting for the sync.
	d := e.delivery(t, "task-12")
	if d.Outcome != "pending" || d.Detail != queuedDetail {
		t.Errorf("delivery: %+v", d)
	}
	if d := e.delivery(t, "task-11"); d.Outcome != "pending" {
		t.Errorf("the queued delivery must stay pending: %+v", d)
	}
}

func TestRunProcessesDeliveries(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "platform/payments-api", 0, "platform:payments-api")
	e.project("platform:payments-api")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.s.Run(ctx); close(done) }()

	wantStatus(t, e, hook("task-13", "platform:payments-api", nil), 202)
	if d := e.waitDelivery(t, "task-13"); d.Outcome != "linked" {
		t.Errorf("delivery: %+v", d)
	}
	cancel()
	<-done
	if n := len(e.analyses(t, sid)); n != 1 {
		t.Errorf("%d analyses", n)
	}
}
