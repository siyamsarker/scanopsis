// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package sonar

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// claims returns the webhook fields of an analysis that names a GitLab
// project and pipeline in its scanner properties; "" leaves one out.
func claims(project, pipeline string) map[string]any {
	props := map[string]string{}
	if project != "" {
		props["sonar.analysis.gitlabProjectId"] = project
	}
	if pipeline != "" {
		props["sonar.analysis.pipelineId"] = pipeline
	}
	return map[string]any{"properties": props}
}

// waitingHint is the detail of a delivery of pipeline 1001 that names a
// GitLab project Scanopsis has no service for yet.
const waitingHint = `No service is linked to SonarQube project "demo:orders-api" yet. The analysis is linked when the scan of the same pipeline (1001) reaches Scanopsis and creates the service of GitLab project 7; you can also set the SonarQube key on the service.`

// noneLinked asserts that the service has no key and nothing was audited or
// queued for a sync.
func (e *env) noneLinked(t *testing.T, sid int64) {
	t.Helper()
	if k, ok := e.serviceKey(t, sid); ok {
		t.Errorf("key %q saved", k)
	}
	if got := e.audits(t); len(got) != 0 {
		t.Errorf("audit events: %v", got)
	}
	select {
	case l := <-e.s.linked:
		t.Errorf("sync queued for %+v", l)
	default:
	}
}

// An analysis that arrives before the first scan of its project waits, and is
// linked when the upload of the same pipeline creates the service: the key is
// saved, the link is audited like a webhook link and the project is synced at
// once.
func TestLinkWaitingSamePipeline(t *testing.T) {
	e := newEnv(t)
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-w1", "demo:orders-api", claims("7", "1001")), 202)
	e.drain(t, 1)
	d := e.delivery(t, "task-w1")
	if d.Outcome != "unlinked" || d.ServiceID.Valid || d.Detail != waitingHint {
		t.Fatalf("before the first scan: %+v", d)
	}

	sid := e.service(t, "demo/orders-api", 7, "")
	if err := e.s.LinkWaiting(context.Background(), sid, 7, 1001); err != nil {
		t.Fatal(err)
	}
	if k, ok := e.serviceKey(t, sid); !ok || k != "demo:orders-api" {
		t.Errorf("key %q %v, want demo:orders-api", k, ok)
	}
	d = e.delivery(t, "task-w1")
	if d.Outcome != "linked" || d.ServiceID.Int64 != sid || d.Detail != waitingLinkedDetail {
		t.Errorf("after the first scan: %+v", d)
	}
	want := []map[string]any{{
		"actor": "sonarqube", "action": "service.updated",
		"detail": map[string]any{
			"id": float64(sid), "gitlab_project_path": "demo/orders-api", "fields": []any{"sonar_project_key"},
			"sonar_project_key": "demo:orders-api", "task_id": "task-w1",
			"reason":                    "linked by the sonar.analysis.gitlabProjectId scanner property",
			"claimed_gitlab_project_id": "7", "pipeline_id": float64(1001),
		},
	}}
	if got := e.audits(t); !reflect.DeepEqual(got, want) {
		t.Errorf("audit events: %v\nwant %v", got, want)
	}

	// The project is synced at once, not at the next interval: the periodic
	// sync cannot run within the test.
	e.s.tick = time.Hour
	e.runUntil(t, func() bool { return len(e.analyses(t, sid)) == 1 })
	if a := e.analyses(t, sid)[0]; a.AnalysisKey != "AY1" || a.QualityGateStatus != "ERROR" {
		t.Errorf("analysis: %+v", a)
	}

	// Linking again changes nothing and records nothing.
	if err := e.s.LinkWaiting(context.Background(), sid, 7, 1001); err != nil {
		t.Fatal(err)
	}
	if n := len(e.audits(t)); n != 1 {
		t.Errorf("%d audit events after a second call", n)
	}
}

// Any SonarQube user can name a GitLab project, so an analysis of another
// pipeline is never linked automatically. Once the analysis of the same
// pipeline is linked, the others say so.
func TestLinkWaitingOtherPipelineNeverLinked(t *testing.T) {
	e := newEnv(t)
	e.project("evil:project")
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-planted", "evil:project", claims("7", "999")), 202)
	e.drain(t, 1)

	sid := e.service(t, "demo/orders-api", 7, "")
	if err := e.s.LinkWaiting(context.Background(), sid, 7, 1001); err != nil {
		t.Fatal(err)
	}
	e.noneLinked(t, sid)
	if d := e.delivery(t, "task-planted"); d.Outcome != "unlinked" {
		t.Errorf("planted: %+v", d)
	}

	// With the analysis of the creating pipeline waiting too, only that one
	// is linked, and the planted one says why it was not.
	e2 := newEnv(t)
	e2.project("evil:project")
	e2.project("demo:orders-api")
	wantStatus(t, e2, hook("task-planted", "evil:project", claims("7", "999")), 202)
	wantStatus(t, e2, hook("task-real", "demo:orders-api", claims("7", "1001")), 202)
	e2.drain(t, 2)
	sid = e2.service(t, "demo/orders-api", 7, "")
	if err := e2.s.LinkWaiting(context.Background(), sid, 7, 1001); err != nil {
		t.Fatal(err)
	}
	if k, _ := e2.serviceKey(t, sid); k != "demo:orders-api" {
		t.Errorf("key %q, want demo:orders-api", k)
	}
	if d := e2.delivery(t, "task-real"); d.Outcome != "linked" || d.ServiceID.Int64 != sid {
		t.Errorf("same pipeline: %+v", d)
	}
	want := `Not linked: the service of GitLab project 7 was linked to SonarQube project "demo:orders-api" by the analysis of the pipeline of its first scan. An administrator can change the SonarQube key on the service.`
	if d := e2.delivery(t, "task-planted"); d.Outcome != "unlinked" || d.ServiceID.Valid || d.Detail != want {
		t.Errorf("planted after the link: %+v", d)
	}
}

// Without a pipeline ID the analysis cannot be tied to the upload, so it is
// never linked automatically, and the hint says what to pass.
func TestLinkWaitingWithoutPipeline(t *testing.T) {
	e := newEnv(t)
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-np", "demo:orders-api", claims("7", "")), 202)
	e.drain(t, 1)
	want := `No service is linked to SonarQube project "demo:orders-api", and no service has GitLab project 7 yet. Pass -Dsonar.analysis.pipelineId="$CI_PIPELINE_ID" to the scanner as well, so that the analysis is linked when the scan of the same pipeline arrives, or set the SonarQube key on the service.`
	if d := e.delivery(t, "task-np"); d.Outcome != "unlinked" || d.Detail != want {
		t.Errorf("delivery: %+v", d)
	}
	sid := e.service(t, "demo/orders-api", 7, "")
	if err := e.s.LinkWaiting(context.Background(), sid, 7, 1001); err != nil {
		t.Fatal(err)
	}
	e.noneLinked(t, sid)
}

// Analyses of more than one SonarQube project in the same pipeline cannot be
// told apart: none is linked, and an administrator must choose.
func TestLinkWaitingTwoKeysInPipeline(t *testing.T) {
	e := newEnv(t)
	e.project("demo:orders-api")
	e.project("demo:orders-ui")
	wantStatus(t, e, hook("task-k1", "demo:orders-api", claims("7", "1001")), 202)
	wantStatus(t, e, hook("task-k2", "demo:orders-ui", claims("7", "1001")), 202)
	e.drain(t, 2)
	sid := e.service(t, "demo/orders-api", 7, "")
	if err := e.s.LinkWaiting(context.Background(), sid, 7, 1001); err != nil {
		t.Fatal(err)
	}
	e.noneLinked(t, sid)
	want := `More than one SonarQube project names GitLab project 7 in pipeline 1001, so none was linked automatically. An administrator must set the SonarQube key on the service.`
	for _, task := range []string{"task-k1", "task-k2"} {
		if d := e.delivery(t, task); d.Outcome != "unlinked" || d.Detail != want {
			t.Errorf("%s: %+v", task, d)
		}
	}
}

// Only analyses of the last 7 days wait.
func TestLinkWaitingOlderThanAWeek(t *testing.T) {
	e := newEnv(t)
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-old", "demo:orders-api", claims("7", "1001")), 202)
	e.drain(t, 1)
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE sonar_deliveries SET received_at = now() - interval '8 days' WHERE task_id = 'task-old'`); err != nil {
		t.Fatal(err)
	}
	sid := e.service(t, "demo/orders-api", 7, "")
	if err := e.s.LinkWaiting(context.Background(), sid, 7, 1001); err != nil {
		t.Fatal(err)
	}
	e.noneLinked(t, sid)
	if d := e.delivery(t, "task-old"); d.Outcome != "unlinked" {
		t.Errorf("delivery: %+v", d)
	}
}

// A key that another service has taken in the meantime is never linked a
// second time, not even from an archived service.
func TestLinkWaitingKeyLinkedElsewhere(t *testing.T) {
	e := newEnv(t)
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-k", "demo:orders-api", claims("7", "1001")), 202)
	e.drain(t, 1)
	other := e.service(t, "demo/legacy-orders", 0, "demo:orders-api")

	sid := e.service(t, "demo/orders-api", 7, "")
	if err := e.s.LinkWaiting(context.Background(), sid, 7, 1001); err != nil {
		t.Fatal(err)
	}
	e.noneLinked(t, sid)
	want := `SonarQube project "demo:orders-api" is already linked to another service, so it was not linked to the service of GitLab project 7.`
	if d := e.delivery(t, "task-k"); d.Outcome != "unlinked" || d.ServiceID.Valid || d.Detail != want {
		t.Errorf("delivery: %+v", d)
	}

	e.archive(t, other)
	if err := e.s.LinkWaiting(context.Background(), sid, 7, 1001); err != nil {
		t.Fatal(err)
	}
	e.noneLinked(t, sid)
}

// A service that already has a key keeps it; a call for another service or
// project links nothing.
func TestLinkWaitingNeverReplacesKey(t *testing.T) {
	e := newEnv(t)
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-n", "demo:orders-api", claims("7", "1001")), 202)
	e.drain(t, 1)

	keyed := e.service(t, "demo/orders-api", 7, "demo:renamed")
	if err := e.s.LinkWaiting(context.Background(), keyed, 7, 1001); err != nil {
		t.Fatal(err)
	}
	if k, _ := e.serviceKey(t, keyed); k != "demo:renamed" {
		t.Errorf("key replaced by %q", k)
	}

	free := e.service(t, "demo/billing", 8, "")
	for _, c := range []struct{ sid, pid int64 }{{free, 7}, {free, 8}} {
		if err := e.s.LinkWaiting(context.Background(), c.sid, c.pid, 1001); err != nil {
			t.Fatal(err)
		}
	}
	e.noneLinked(t, free)
	if d := e.delivery(t, "task-n"); d.Outcome != "unlinked" {
		t.Errorf("delivery: %+v", d)
	}
}

// The analysis of an archived service's project is never linked, and the
// hint says an administrator must set the key.
func TestUnlinkedHintArchived(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "demo/orders-api", 7, "")
	e.archive(t, sid)
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-a", "demo:orders-api", claims("7", "1001")), 202)
	e.drain(t, 1)
	want := `The service of GitLab project 7 is archived, so SonarQube project "demo:orders-api" was not linked. An administrator must set the SonarQube key on the service after unarchiving it.`
	if d := e.delivery(t, "task-a"); d.Outcome != "unlinked" || d.Detail != want {
		t.Errorf("delivery: %+v", d)
	}
}

// A delivery without a usable gitlabProjectId gets the advice to pass both
// properties.
func TestUnlinkedHintWithoutClaim(t *testing.T) {
	e := newEnv(t)
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-u", "demo:orders-api", claims("not-a-number", "1001")), 202)
	e.drain(t, 1)
	if d := e.delivery(t, "task-u"); d.Outcome != "unlinked" || d.Detail != noClaimHint("demo:orders-api") {
		t.Errorf("delivery: %+v", d)
	}
}

// noClaimHint is the detail of a delivery without a usable gitlabProjectId.
func noClaimHint(key string) string {
	return `No service is linked to SonarQube project "` + key + `". Pass -Dsonar.analysis.gitlabProjectId="$CI_PROJECT_ID" and -Dsonar.analysis.pipelineId="$CI_PIPELINE_ID" to the scanner, or set the SonarQube key on the service.`
}

// A delivery processed while the first upload creates the service may find no
// service, and finish unlinked only after the upload looked for waiting
// deliveries. It then checks again for the service itself.
func TestLinkLateAfterRace(t *testing.T) {
	e := newEnv(t)
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-r", "demo:orders-api", claims("7", "1001")), 202)
	e.drain(t, 1)
	sid := e.service(t, "demo/orders-api", 7, "")
	// No service when the delivery was resolved, and the upload's call saw
	// the delivery still pending: only the check after it links it.
	e.s.linkLate(context.Background(), 7, 1001)
	if d := e.delivery(t, "task-r"); d.Outcome != "linked" || d.ServiceID.Int64 != sid {
		t.Errorf("delivery: %+v", d)
	}
	if k, _ := e.serviceKey(t, sid); k != "demo:orders-api" {
		t.Errorf("key %q", k)
	}
	// Without a service it does nothing.
	e.s.linkLate(context.Background(), 8, 1001)
}
