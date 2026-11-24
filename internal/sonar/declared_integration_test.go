// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package sonar

import (
	"context"
	"testing"
	"time"
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
	e.drain(t, 3)

	// The upload created the service with the declared key.
	sid := e.service(t, "demo/orders-api", 7, "demo:orders-api")
	if err := e.s.LinkDeclared(context.Background(), sid, 7, "demo:orders-api"); err != nil {
		t.Fatal(err)
	}
	for _, task := range []string{"task-real", "task-plain"} {
		if d := e.delivery(t, task); d.Outcome != "linked" || d.ServiceID.Int64 != sid || d.Detail != declaredLinkedDetail {
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
	wantStatus(t, e, hook("task-r", "demo:orders-api", nil), 202)
	e.drain(t, 1)
	sid := e.service(t, "demo/orders-api", 7, "demo:orders-api")
	e.s.linkKeyLate(context.Background(), "demo:orders-api")
	if d := e.delivery(t, "task-r"); d.Outcome != "linked" || d.ServiceID.Int64 != sid || d.Detail != keyLinkedDetail {
		t.Errorf("delivery: %+v", d)
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
