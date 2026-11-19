// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package sonar

import (
	"context"
	"testing"
)

func (e *env) serviceKey(t *testing.T, id int64) (string, bool) {
	t.Helper()
	k, err := e.q.SonarServiceKey(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return k.String, k.Valid
}

func (e *env) archive(t *testing.T, id int64) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `UPDATE services SET archived = true WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookPropertyCannotHijackLinkedService(t *testing.T) {
	e := newEnv(t)
	victim := e.service(t, "team-a/svc", 1, "team-a:svc")
	e.project("evil:project")
	props := map[string]string{"sonar.analysis.gitlabProjectId": "1"}
	wantStatus(t, e, hook("task-h", "evil:project", map[string]any{"properties": props}), 202)
	e.drain(t, 1)
	d := e.delivery(t, "task-h")
	want := `The service named by sonar.analysis.gitlabProjectId is linked to a different SonarQube project, so project "evil:project" was not linked to it. Change the SonarQube key on the service if the project was renamed.`
	if d.Outcome != "unlinked" || d.ServiceID.Valid || d.Detail != want {
		t.Errorf("delivery: %+v", d)
	}
	if n := len(e.analyses(t, victim)); n != 0 {
		t.Errorf("%d analyses stored on another team's service", n)
	}
	if k, _ := e.serviceKey(t, victim); k != "team-a:svc" {
		t.Errorf("key changed to %q", k)
	}
	if n := e.fake.Count("api/measures/component"); n != 0 {
		t.Errorf("%d Web API calls for an unlinked delivery", n)
	}
}

func TestWebhookPropertyPinsUnlinkedService(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "platform/new", 2, "")
	e.project("new:key")
	props := map[string]string{"sonar.analysis.gitlabProjectId": "2"}
	wantStatus(t, e, hook("task-p1", "new:key", map[string]any{"properties": props}), 202)
	e.drain(t, 1)
	if d := e.delivery(t, "task-p1"); d.Outcome != "linked" || d.ServiceID.Int64 != sid {
		t.Errorf("delivery: %+v", d)
	}
	if k, ok := e.serviceKey(t, sid); !ok || k != "new:key" {
		t.Errorf("key %q %v, want it pinned", k, ok)
	}
	// Later deliveries link by key alone; another project can no longer take over.
	wantStatus(t, e, hook("task-p2", "new:key", nil), 202)
	e.drain(t, 1)
	if d := e.delivery(t, "task-p2"); d.Outcome != "linked" || d.ServiceID.Int64 != sid {
		t.Errorf("by key: %+v", d)
	}
	e.project("other:key")
	wantStatus(t, e, hook("task-p3", "other:key", map[string]any{"properties": props}), 202)
	e.drain(t, 1)
	if d := e.delivery(t, "task-p3"); d.Outcome != "unlinked" {
		t.Errorf("takeover: %+v", d)
	}
}

func TestWebhookPinConflict(t *testing.T) {
	e := newEnv(t)
	old := e.service(t, "platform/old", 0, "shared:key")
	e.archive(t, old) // archived services are not looked up, but still own the key
	sid := e.service(t, "platform/new", 3, "")
	e.project("shared:key")
	props := map[string]string{"sonar.analysis.gitlabProjectId": "3"}
	wantStatus(t, e, hook("task-c", "shared:key", map[string]any{"properties": props}), 202)
	e.drain(t, 1)
	d := e.delivery(t, "task-c")
	want := `The service named by sonar.analysis.gitlabProjectId cannot be linked to SonarQube project "shared:key" because the service or the key is already linked.`
	if d.Outcome != "unlinked" || d.Detail != want {
		t.Errorf("delivery: %+v", d)
	}
	if _, ok := e.serviceKey(t, sid); ok {
		t.Error("the key must stay unset")
	}
	if n := len(e.analyses(t, sid)); n != 0 {
		t.Errorf("%d analyses", n)
	}
}

func TestWebhookIgnoresArchivedServices(t *testing.T) {
	e := newEnv(t)
	byKey := e.service(t, "platform/a", 0, "a:key")
	byID := e.service(t, "platform/b", 7, "")
	e.archive(t, byKey)
	e.archive(t, byID)
	e.project("a:key")
	e.project("b:key")
	wantStatus(t, e, hook("task-a1", "a:key", nil), 202)
	wantStatus(t, e, hook("task-a2", "b:key", map[string]any{"properties": map[string]string{"sonar.analysis.gitlabProjectId": "7"}}), 202)
	e.drain(t, 2)
	for _, task := range []string{"task-a1", "task-a2"} {
		if d := e.delivery(t, task); d.Outcome != "unlinked" {
			t.Errorf("%s: %+v", task, d)
		}
	}
	if _, ok := e.serviceKey(t, byID); ok {
		t.Error("an archived service must not be pinned")
	}
}

func TestWebhookFailedTaskDoesNotPin(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "platform/new", 4, "")
	e.project("new:key")
	props := map[string]string{"sonar.analysis.gitlabProjectId": "4"}
	wantStatus(t, e, hook("task-f", "new:key", map[string]any{"properties": props, "status": "FAILED"}), 202)
	e.drain(t, 1)
	if _, ok := e.serviceKey(t, sid); ok {
		t.Error("a failed task must not link the service")
	}
}

func TestDeliveryDetailHasNoInternals(t *testing.T) {
	e := newEnv(t)
	e.service(t, "platform/payments-api", 0, "platform:payments-api")
	e.project("platform:payments-api")
	e.fake.Close() // connection refused: the raw error names the address
	wantStatus(t, e, hook("task-n", "platform:payments-api", nil), 202)
	e.drain(t, 1)
	d := e.delivery(t, "task-n")
	want := "Reading measures failed: the request failed or the answer was unreadable; see the server log. The next sync will retry."
	if d.Outcome != "failed" || d.Detail != want {
		t.Errorf("delivery: %+v", d)
	}
}

func TestSyncErrorHasNoInternals(t *testing.T) {
	e := newEnv(t)
	e.service(t, "platform/a", 0, "platform:a")
	e.fake.Close()
	e.runSync(t)
	want := "platform:a: the request failed or the answer was unreadable; see the server log"
	if _, _, lastErr := e.state(); lastErr != want {
		t.Errorf("lastErr %q", lastErr)
	}
}
