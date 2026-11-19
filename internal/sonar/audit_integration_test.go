// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package sonar

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func (e *env) audits(t *testing.T) []map[string]any {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT actor, action, detail FROM audit_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var actor, action string
		var detail []byte
		if err := rows.Scan(&actor, &action, &detail); err != nil {
			t.Fatal(err)
		}
		d := map[string]any{}
		if err := json.Unmarshal(detail, &d); err != nil {
			t.Fatal(err)
		}
		out = append(out, map[string]any{"actor": actor, "action": action, "detail": d})
	}
	return out
}

func TestPinWritesAuditEvent(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "platform/new", 5, "")
	e.project("new:key")
	props := map[string]string{"sonar.analysis.gitlabProjectId": "5"}
	wantStatus(t, e, hook("task-au", "new:key", map[string]any{"properties": props}), 202)
	e.drain(t, 1)
	// A later delivery by key links nothing new and writes no event.
	wantStatus(t, e, hook("task-au2", "new:key", nil), 202)
	e.drain(t, 1)

	got := e.audits(t)
	want := []map[string]any{{
		"actor": "sonarqube", "action": "service.updated",
		"detail": map[string]any{
			"id": float64(sid), "gitlab_project_path": "platform/new", "fields": []any{"sonar_project_key"},
			"sonar_project_key": "new:key", "task_id": "task-au",
			"reason": "linked by the sonar.analysis.gitlabProjectId scanner property",
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("audit events: %v\nwant %v", got, want)
	}
}

func TestNoAuditEventWithoutLink(t *testing.T) {
	e := newEnv(t)
	old := e.service(t, "platform/old", 0, "shared:key")
	e.archive(t, old)
	e.service(t, "platform/new", 6, "")
	e.service(t, "team-a/svc", 7, "team-a:svc")
	e.project("shared:key")
	e.project("evil:project")
	wantStatus(t, e, hook("c1", "shared:key", map[string]any{"properties": map[string]string{"sonar.analysis.gitlabProjectId": "6"}}), 202)
	wantStatus(t, e, hook("c2", "evil:project", map[string]any{"properties": map[string]string{"sonar.analysis.gitlabProjectId": "7"}}), 202)
	e.drain(t, 2)
	if got := e.audits(t); len(got) != 0 {
		t.Errorf("unexpected audit events: %v", got)
	}
}
