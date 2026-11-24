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

// claims returns the webhook fields of an analysis that names GitLab
// project id in its scanner properties.
func claims(id string) map[string]any {
	return map[string]any{"properties": map[string]string{"sonar.analysis.gitlabProjectId": id}}
}

// waitingHint is the detail of a delivery that names a GitLab project
// Scanopsis has no service for yet.
const waitingHint = `No service is linked to SonarQube project "demo:orders-api" yet. The analysis is linked when the first scan of GitLab project 7 reaches Scanopsis; you can also set the SonarQube key on the service.`

// An analysis that arrives before the first scan of its project waits, and is
// linked when the service of the project is created: the key is saved, the
// link is audited like a webhook link and the project is synced at once.
func TestLinkWaitingAfterFirstScan(t *testing.T) {
	e := newEnv(t)
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-w1", "demo:orders-api", claims("7")), 202)
	e.drain(t, 1)
	d := e.delivery(t, "task-w1")
	if d.Outcome != "unlinked" || d.ServiceID.Valid || d.Detail != waitingHint {
		t.Fatalf("before the first scan: %+v", d)
	}

	sid := e.service(t, "demo/orders-api", 7, "")
	if err := e.s.LinkWaiting(context.Background(), sid, 7); err != nil {
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
			"claimed_gitlab_project_id": "7", "pipeline_id": nil,
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
	if err := e.s.LinkWaiting(context.Background(), sid, 7); err != nil {
		t.Fatal(err)
	}
	if n := len(e.audits(t)); n != 1 {
		t.Errorf("%d audit events after a second call", n)
	}
}

// The newest waiting analysis decides the key; every waiting delivery of that
// key is linked, those of another key stay unlinked.
func TestLinkWaitingNewestFirst(t *testing.T) {
	e := newEnv(t)
	e.project("demo:old-name")
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-a", "demo:old-name", claims("7")), 202)
	wantStatus(t, e, hook("task-b", "demo:orders-api", claims("7")), 202)
	wantStatus(t, e, hook("task-c", "demo:orders-api", claims("7")), 202)
	e.drain(t, 3)

	sid := e.service(t, "demo/orders-api", 7, "")
	if err := e.s.LinkWaiting(context.Background(), sid, 7); err != nil {
		t.Fatal(err)
	}
	if k, _ := e.serviceKey(t, sid); k != "demo:orders-api" {
		t.Errorf("key %q", k)
	}
	for task, outcome := range map[string]string{"task-a": "unlinked", "task-b": "linked", "task-c": "linked"} {
		if d := e.delivery(t, task); d.Outcome != outcome {
			t.Errorf("%s: %+v, want %s", task, d, outcome)
		}
	}
	if got := e.audits(t); len(got) != 1 || got[0]["detail"].(map[string]any)["task_id"] != "task-c" {
		t.Errorf("audit events: %v", got)
	}
}

// A key that another service has taken in the meantime is never linked a
// second time; an older waiting key that is still free is linked instead.
func TestLinkWaitingKeyLinkedElsewhere(t *testing.T) {
	e := newEnv(t)
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-k", "demo:orders-api", claims("7")), 202)
	e.drain(t, 1)
	other := e.service(t, "demo/legacy-orders", 0, "demo:orders-api")

	sid := e.service(t, "demo/orders-api", 7, "")
	if err := e.s.LinkWaiting(context.Background(), sid, 7); err != nil {
		t.Fatal(err)
	}
	if k, ok := e.serviceKey(t, sid); ok {
		t.Errorf("key %q saved although service %d has it", k, other)
	}
	if d := e.delivery(t, "task-k"); d.Outcome != "unlinked" || d.ServiceID.Valid {
		t.Errorf("delivery: %+v", d)
	}
	if got := e.audits(t); len(got) != 0 {
		t.Errorf("audit events: %v", got)
	}
	select {
	case l := <-e.s.linked:
		t.Errorf("sync queued for %+v", l)
	default:
	}

	// An archived service still holds its key.
	e.archive(t, other)
	if err := e.s.LinkWaiting(context.Background(), sid, 7); err != nil {
		t.Fatal(err)
	}
	if k, ok := e.serviceKey(t, sid); ok {
		t.Errorf("key %q saved although an archived service has it", k)
	}
}

// A service that already has a key keeps it; a call for another service or
// project, or for an archived service, links nothing.
func TestLinkWaitingNeverReplacesKey(t *testing.T) {
	e := newEnv(t)
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-n", "demo:orders-api", claims("7")), 202)
	e.drain(t, 1)

	keyed := e.service(t, "demo/orders-api", 7, "demo:renamed")
	if err := e.s.LinkWaiting(context.Background(), keyed, 7); err != nil {
		t.Fatal(err)
	}
	if k, _ := e.serviceKey(t, keyed); k != "demo:renamed" {
		t.Errorf("key replaced by %q", k)
	}

	free := e.service(t, "demo/billing", 8, "")
	for _, c := range []struct{ sid, pid int64 }{{free, 7}, {free, 8}} {
		if err := e.s.LinkWaiting(context.Background(), c.sid, c.pid); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := e.serviceKey(t, free); ok {
		t.Error("a service of another GitLab project was linked")
	}
	if d := e.delivery(t, "task-n"); d.Outcome != "unlinked" {
		t.Errorf("delivery: %+v", d)
	}
	if got := e.audits(t); len(got) != 0 {
		t.Errorf("audit events: %v", got)
	}
}

// A delivery without a usable gitlabProjectId keeps the advice to pass it.
func TestUnlinkedHintWithoutClaim(t *testing.T) {
	e := newEnv(t)
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-u", "demo:orders-api", claims("not-a-number")), 202)
	e.drain(t, 1)
	want := `No service is linked to SonarQube project "demo:orders-api". Pass -Dsonar.analysis.gitlabProjectId=$CI_PROJECT_ID to the scanner, or set the SonarQube key on the service.`
	if d := e.delivery(t, "task-u"); d.Outcome != "unlinked" || d.Detail != want {
		t.Errorf("delivery: %+v", d)
	}
}

// A delivery processed while the first upload creates the service may find no
// service, and finish unlinked only after the upload looked for waiting
// deliveries. It then checks again for the service itself.
func TestLinkLateAfterRace(t *testing.T) {
	e := newEnv(t)
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-r", "demo:orders-api", claims("7")), 202)
	e.drain(t, 1)
	sid := e.service(t, "demo/orders-api", 7, "")
	// No service when the delivery was resolved, and the upload's call saw
	// the delivery still pending: only the check after it links it.
	e.s.linkLate(context.Background(), 7)
	if d := e.delivery(t, "task-r"); d.Outcome != "linked" || d.ServiceID.Int64 != sid {
		t.Errorf("delivery: %+v", d)
	}
	if k, _ := e.serviceKey(t, sid); k != "demo:orders-api" {
		t.Errorf("key %q", k)
	}
	// Without a service it does nothing.
	e.s.linkLate(context.Background(), 8)
}
