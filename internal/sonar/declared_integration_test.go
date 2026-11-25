// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package sonar

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/store"
)

// After the pipeline's upload declared the key of its service, the
// deliveries of that key that waited are linked, the project is read at once
// with the pipeline of its delivery, and an analysis of another key that
// named the same GitLab project and pipeline is never linked, then or later.
func TestLinkDeclaredKeyWins(t *testing.T) {
	e := newEnv(t)
	e.project("evil:project")
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-planted", "evil:project", claims("7", "1001")), 202)
	wantStatus(t, e, hook("task-real", "demo:orders-api", claims("7", "1001")), 202)
	wantStatus(t, e, hook("task-plain", "demo:orders-api", nil), 202)
	wantStatus(t, e, hook("task-elsewhere", "demo:orders-api", claims("8", "2001")), 202)
	e.drain(t, 4)

	// The upload created the service with the declared key.
	sid := e.service(t, "demo/orders-api", 7, "demo:orders-api")
	if err := e.s.LinkDeclared(context.Background(), sid, 7, "demo:orders-api"); err != nil {
		t.Fatal(err)
	}
	if d := e.delivery(t, "task-real"); d.Outcome != "linked" || d.ServiceID.Int64 != sid || d.Detail != declaredLinkedDetail {
		t.Errorf("task-real: %+v", d)
	}
	// Only the deliveries that name the service's GitLab project are linked.
	for _, task := range []string{"task-plain", "task-elsewhere"} {
		if d := e.delivery(t, task); d.Outcome != "unlinked" || d.ServiceID.Valid {
			t.Errorf("%s: %+v", task, d)
		}
	}
	want := `Not linked: the service of GitLab project 7 is linked to SonarQube project "demo:orders-api", the key its pipeline declared in its upload. An administrator can change the SonarQube key on the service.`
	if d := e.delivery(t, "task-planted"); d.Outcome != "unlinked" || d.ServiceID.Valid || d.Detail != want {
		t.Errorf("planted: %+v", d)
	}
	if k, _ := e.serviceKey(t, sid); k != "demo:orders-api" {
		t.Errorf("key %q", k)
	}
	if got := e.audits(t); len(got) != 0 {
		t.Errorf("audit events: %v (the upload records the link)", got)
	}

	e.s.tick = time.Hour
	e.runUntil(t, func() bool { return len(e.analyses(t, sid)) == 1 })
	if a := e.analyses(t, sid)[0]; a.AnalysisKey != "AY1" || a.GitlabPipelineID.Int64 != 1001 {
		t.Errorf("analysis: %+v", a)
	}

	// A later analysis of another key that names the project is not linked.
	wantStatus(t, e, hook("task-later", "evil:project", claims("7", "1002")), 202)
	e.drain(t, 1)
	if d := e.delivery(t, "task-later"); d.Outcome != "unlinked" {
		t.Errorf("later planted: %+v", d)
	}
	if k, _ := e.serviceKey(t, sid); k != "demo:orders-api" {
		t.Errorf("key %q after a planted analysis", k)
	}
}

// LinkDeclared links only the key the service has: a call with another key,
// or for a service without a key, changes nothing.
func TestLinkDeclaredOnlyServiceKey(t *testing.T) {
	e := newEnv(t)
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-real", "demo:orders-api", claims("7", "1001")), 202)
	e.drain(t, 1)
	keyed := e.service(t, "demo/orders-api", 7, "demo:renamed")
	free := e.service(t, "demo/billing", 8, "")
	for _, c := range []struct{ sid, pid int64 }{{keyed, 7}, {free, 8}} {
		if err := e.s.LinkDeclared(context.Background(), c.sid, c.pid, "demo:orders-api"); err != nil {
			t.Fatal(err)
		}
	}
	if d := e.delivery(t, "task-real"); d.Outcome != "unlinked" {
		t.Errorf("delivery: %+v", d)
	}
	e.noneLinked(t, free)
}

// A delivery processed while the upload declared its key may find no service
// for the key and finish unlinked after the upload linked the waiting
// deliveries. It then checks again for a service with its key.
func TestLinkKeyLateAfterRace(t *testing.T) {
	e := newEnv(t)
	e.project("demo:orders-api")
	wantStatus(t, e, hook("task-r", "demo:orders-api", claims("7", "1001")), 202)
	wantStatus(t, e, hook("task-8", "demo:orders-api", claims("8", "1001")), 202)
	e.drain(t, 2)
	sid := e.service(t, "demo/orders-api", 7, "demo:orders-api")
	e.s.linkKeyLate(context.Background(), "demo:orders-api")
	if d := e.delivery(t, "task-r"); d.Outcome != "linked" || d.ServiceID.Int64 != sid || d.Detail != keyLinkedDetail {
		t.Errorf("delivery: %+v", d)
	}
	if d := e.delivery(t, "task-8"); d.Outcome != "unlinked" {
		t.Errorf("delivery naming another project: %+v", d)
	}
	select {
	case l := <-e.s.linked:
		if l.ID != sid {
			t.Errorf("sync queued for %+v", l)
		}
	default:
		t.Error("no sync queued")
	}
	// Without a service for the key it does nothing.
	e.s.linkKeyLate(context.Background(), "demo:none")
}

// declare records a key the service's pipeline declared in an upload, still
// pending, as the upload does.
func (e *env) declare(t *testing.T, sid int64, key string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE services SET sonar_key_declared = $2, sonar_key_declared_at = now() WHERE id = $1`, sid, key); err != nil {
		t.Fatal(err)
	}
}

// The upload came first and its declaration is pending. An analysis of
// another key that names the project is not linked; one of the declared key
// that names the project confirms it and links it, with the audit reason of
// a confirmed declaration and the pipeline of the delivery. An analysis of
// the declared key that names another project confirms nothing.
func TestWebhookConfirmsPendingDeclaration(t *testing.T) {
	e := newEnv(t)
	e.project("evil:project")
	e.project("demo:orders-api")
	sid := e.service(t, "demo/orders-api", 7, "")
	e.declare(t, sid, "demo:orders-api")

	wantStatus(t, e, hook("task-planted", "evil:project", claims("7", "1001")), 202)
	wantStatus(t, e, hook("task-elsewhere", "demo:orders-api", claims("8", "1001")), 202)
	e.drain(t, 2)
	want := `The service of GitLab project 7 waits for SonarQube project "demo:orders-api", the key its pipeline declared in an upload, so project "evil:project" was not linked to it. An administrator can set the SonarQube key on the service.`
	if d := e.delivery(t, "task-planted"); d.Outcome != "unlinked" || d.Detail != want {
		t.Errorf("planted: %+v", d)
	}
	if d := e.delivery(t, "task-elsewhere"); d.Outcome != "unlinked" || d.ServiceID.Valid {
		t.Errorf("naming another project: %+v", d)
	}
	e.noneLinked(t, sid)

	wantStatus(t, e, hook("task-real", "demo:orders-api", claims("7", "1001")), 202)
	e.drain(t, 1)
	if d := e.delivery(t, "task-real"); d.Outcome != "linked" || d.ServiceID.Int64 != sid {
		t.Errorf("confirming delivery: %+v", d)
	}
	if k, _ := e.serviceKey(t, sid); k != "demo:orders-api" {
		t.Errorf("key %q", k)
	}
	got := e.audits(t)
	if len(got) != 1 || got[0]["detail"].(map[string]any)["reason"] != "declared by the pipeline's upload and confirmed by a SonarQube analysis of the same project" {
		t.Errorf("audit events: %v", got)
	}
	if a := e.analyses(t, sid); len(a) != 1 || a[0].GitlabPipelineID.Int64 != 1001 {
		t.Errorf("analyses: %+v", a)
	}
}

// A pending declaration blocks the link of the same pipeline's waiting
// analysis of another key; an analysis of the declared key is linked even
// beside one of another key.
func TestPendingDeclarationBlocksLinkWaiting(t *testing.T) {
	e := newEnv(t)
	e.project("evil:project")
	wantStatus(t, e, hook("task-planted", "evil:project", claims("7", "1001")), 202)
	e.drain(t, 1)
	sid := e.service(t, "demo/orders-api", 7, "")
	e.declare(t, sid, "demo:orders-api")
	if err := e.s.LinkWaiting(context.Background(), sid, 7, 1001); err != nil {
		t.Fatal(err)
	}
	e.noneLinked(t, sid)
	want := `The service of GitLab project 7 waits for SonarQube project "demo:orders-api", the key its pipeline declared in an upload, so this analysis was not linked to it. An administrator can set the SonarQube key on the service.`
	if d := e.delivery(t, "task-planted"); d.Outcome != "unlinked" || d.Detail != want {
		t.Errorf("planted: %+v", d)
	}

	e2 := newEnv(t)
	e2.project("evil:project")
	e2.project("demo:orders-api")
	wantStatus(t, e2, hook("task-planted", "evil:project", claims("7", "1001")), 202)
	wantStatus(t, e2, hook("task-real", "demo:orders-api", claims("7", "1001")), 202)
	e2.drain(t, 2)
	sid = e2.service(t, "demo/orders-api", 7, "")
	e2.declare(t, sid, "demo:orders-api")
	if err := e2.s.LinkWaiting(context.Background(), sid, 7, 1001); err != nil {
		t.Fatal(err)
	}
	if k, _ := e2.serviceKey(t, sid); k != "demo:orders-api" {
		t.Errorf("key %q", k)
	}
	if d := e2.delivery(t, "task-planted"); d.Outcome != "unlinked" {
		t.Errorf("planted: %+v", d)
	}
}

// adminSetKey sets or, with "", clears the SonarQube key as an administrator
// does through the service API.
func (e *env) adminSetKey(t *testing.T, sid int64, key string) {
	t.Helper()
	arg := store.UpdateServiceParams{ID: sid, SetSonarKey: true}
	if key != "" {
		arg.SonarProjectKey = pgtype.Text{String: key, Valid: true}
	}
	if _, err := e.q.UpdateService(context.Background(), arg); err != nil {
		t.Fatal(err)
	}
}

// An administrator's key wins over a pending declaration: after the
// administrator set a key and cleared it again, the declaration no longer
// blocks the link by the scanner property, and a later link of the once
// declared key is audited as a plain property link, not as a confirmed
// declaration.
func TestAdminKeyClearsDeclaration(t *testing.T) {
	for _, key := range []string{"other:project", "demo:orders-api"} {
		t.Run(key, func(t *testing.T) {
			e := newEnv(t)
			e.project(key)
			sid := e.service(t, "demo/orders-api", 7, "")
			e.declare(t, sid, "demo:orders-api")
			e.adminSetKey(t, sid, "demo:orders-y")
			e.adminSetKey(t, sid, "")

			wantStatus(t, e, hook("task-x", key, claims("7", "1001")), 202)
			e.drain(t, 1)
			if d := e.delivery(t, "task-x"); d.Outcome != "linked" || d.ServiceID.Int64 != sid {
				t.Errorf("delivery: %+v", d)
			}
			if k, _ := e.serviceKey(t, sid); k != key {
				t.Errorf("key %q, want %q", k, key)
			}
			got := e.audits(t)
			if len(got) != 1 || got[0]["detail"].(map[string]any)["reason"] != "linked by the sonar.analysis.gitlabProjectId scanner property" {
				t.Errorf("audit events: %v", got)
			}
		})
	}
}

// A declaration committed after a webhook read the service, but before it
// pinned the key, still wins: the pin of another key is refused under the
// row lock, and the delivery says so.
func TestDeclarationWinsRaceWithWebhookPin(t *testing.T) {
	e := newEnv(t)
	e.project("evil:project")
	sid := e.service(t, "demo/orders-api", 7, "")
	p := webhookPayload{TaskID: "task-x", Status: "SUCCESS"}
	p.Project.Key = "evil:project"
	p.Properties = map[string]string{"sonar.analysis.gitlabProjectId": "7"}
	l, err := e.s.linkService(context.Background(), p)
	if err != nil || !l.Pin || l.ServiceID != sid {
		t.Fatalf("link %+v %v", l, err)
	}
	e.declare(t, sid, "demo:orders-api") // the upload commits now
	_, err = e.s.save(context.Background(), snapshot{ServiceID: sid, TaskID: "task-x", AnalysedAt: analysisTime, Conditions: []Condition{}}, "evil:project")
	if !errors.Is(err, errPinConflict) {
		t.Fatalf("save = %v, want the pin conflict", err)
	}
	e.noneLinked(t, sid)
	if a := e.analyses(t, sid); len(a) != 0 {
		t.Errorf("analysis stored: %+v", a)
	}

	// The same for the link of a waiting analysis.
	wantStatus(t, e, hook("task-w", "evil:project", claims("8", "1001")), 202)
	e.drain(t, 1)
	other := e.service(t, "demo/billing", 8, "")
	w := waiting{serviceID: other, project: pgtype.Int8{Int64: 8, Valid: true}, pipeline: pgtype.Int8{Int64: 1001, Valid: true},
		since: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}}
	e.declare(t, other, "demo:billing")
	if err := e.s.pinWaiting(context.Background(), w, store.WaitingSonarKeysRow{ProjectKey: "evil:project", TaskID: "task-w"}); !errors.Is(err, errServiceTaken) {
		t.Fatalf("pinWaiting = %v, want refused", err)
	}
	e.noneLinked(t, other)
}

// The declared key is found among any number of waiting keys of the
// pipeline, not only among the first two.
func TestLinkWaitingFindsDeclaredAmongMany(t *testing.T) {
	e := newEnv(t)
	for _, k := range []string{"a:one", "b:two", "z:declared"} {
		e.project(k)
		wantStatus(t, e, hook("task-"+k, k, claims("7", "1001")), 202)
	}
	e.drain(t, 3)
	sid := e.service(t, "demo/orders-api", 7, "")
	e.declare(t, sid, "z:declared")
	if err := e.s.LinkWaiting(context.Background(), sid, 7, 1001); err != nil {
		t.Fatal(err)
	}
	if k, _ := e.serviceKey(t, sid); k != "z:declared" {
		t.Errorf("key %q", k)
	}
	for _, k := range []string{"a:one", "b:two"} {
		if d := e.delivery(t, "task-"+k); d.Outcome != "unlinked" {
			t.Errorf("%s: %+v", k, d)
		}
	}
}
