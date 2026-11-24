// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package sonar

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/testutil/fakesonar"
)

// latestPipeline returns gitlab_pipeline_id of the latest analysis on the
// service's SonarQube card (GET /services/{id}/sonar), nil when it is null.
func (e *env) latestPipeline(t *testing.T, sid int64) *int64 {
	t.Helper()
	code, body, _ := e.get(t, "getServiceSonar", "/api/v1/services/"+strconv.FormatInt(sid, 10)+"/sonar")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var card struct {
		Latest *struct {
			GitlabPipelineID *int64 `json:"gitlab_pipeline_id"`
		} `json:"latest"`
	}
	if err := json.Unmarshal([]byte(body), &card); err != nil || card.Latest == nil {
		t.Fatalf("card %s: %v", body, err)
	}
	return card.Latest.GitlabPipelineID
}

// The analysis linked because its pipeline's first scan created the service
// keeps the pipeline its delivery named, as one linked by its webhook does,
// so the card says in which pipeline it ran.
func TestLinkWaitingKeepsPipeline(t *testing.T) {
	e := newEnv(t)
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-p", "demo:orders-api", claims("7", "1001")), 202)
	e.drain(t, 1)
	sid := e.service(t, "demo/orders-api", 7, "")
	if err := e.s.LinkWaiting(context.Background(), sid, 7, 1001); err != nil {
		t.Fatal(err)
	}
	e.s.tick = time.Hour
	e.runUntil(t, func() bool { return len(e.analyses(t, sid)) == 1 })
	if a := e.analyses(t, sid)[0]; a.AnalysisKey != "AY1" || !a.GitlabPipelineID.Valid || a.GitlabPipelineID.Int64 != 1001 {
		t.Errorf("analysis: key %q pipeline %+v, want AY1 in pipeline 1001", a.AnalysisKey, a.GitlabPipelineID)
	}
	if p := e.latestPipeline(t, sid); p == nil || *p != 1001 {
		t.Errorf("card pipeline %v, want 1001", p)
	}
}

// When SonarQube's newest analysis started after the waiting delivery
// arrived, it is a later analysis, not the one of the pipeline: it is stored
// without a pipeline rather than with a wrong one.
func TestLinkWaitingNewerAnalysisHasNoPipeline(t *testing.T) {
	e := newEnv(t)
	e.s.now = func() time.Time { return analysisTime.Add(5 * time.Minute) }
	e.fake.Set("demo:orders-api", fakesonar.Project{
		Measures: fakesonar.MQRMeasures(), GateStatus: "OK",
		Analyses: []fakesonar.Analysis{{Key: "AY2", Date: analysisTime.Add(time.Hour)}},
	})
	wantStatus(t, e, hook("task-p", "demo:orders-api", claims("7", "1001")), 202)
	e.drain(t, 1)
	sid := e.service(t, "demo/orders-api", 7, "")
	if err := e.s.LinkWaiting(context.Background(), sid, 7, 1001); err != nil {
		t.Fatal(err)
	}
	e.s.tick = time.Hour
	e.runUntil(t, func() bool { return len(e.analyses(t, sid)) == 1 })
	if a := e.analyses(t, sid)[0]; a.AnalysisKey != "AY2" || a.GitlabPipelineID.Valid {
		t.Errorf("analysis: key %q pipeline %+v, want AY2 without a pipeline", a.AnalysisKey, a.GitlabPipelineID)
	}
	if p := e.latestPipeline(t, sid); p != nil {
		t.Errorf("card pipeline %d, want null", *p)
	}
}
