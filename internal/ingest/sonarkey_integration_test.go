// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package ingest

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/store"
)

// sonarKeyOf returns the SonarQube key of a service, "" when it has none.
func (e *iEnv) sonarKeyOf(id int64) string {
	e.t.Helper()
	var k pgtype.Text
	if err := e.pool.QueryRow(context.Background(), `SELECT sonar_project_key FROM services WHERE id = $1`, id).Scan(&k); err != nil {
		e.t.Fatal(err)
	}
	return k.String
}

// auditOf returns the details of the audit events of an action, oldest first.
func (e *iEnv) auditOf(action string) []map[string]any {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT actor, detail FROM audit_log WHERE action = $1 ORDER BY id`, action)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var actor string
		var raw []byte
		if err := rows.Scan(&actor, &raw); err != nil {
			e.t.Fatal(err)
		}
		d := map[string]any{}
		if err := json.Unmarshal(raw, &d); err != nil {
			e.t.Fatal(err)
		}
		d["_actor"] = actor
		out = append(out, d)
	}
	return out
}

// uploadRaw uploads and returns the status and the decoded answer.
func (e *iEnv) uploadRaw(u upload) (int, map[string]any) {
	e.t.Helper()
	code, body := e.upload(u)
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		e.t.Fatalf("%d %s", code, body)
	}
	return code, m
}

var sonarKeyBody = []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)

// sonarDelivery records a SonarQube delivery of key that names a GitLab
// project, received age ago, as the webhook does.
func (e *iEnv) sonarDelivery(task, key string, project int64, age time.Duration) {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO sonar_deliveries
		(task_id, project_key, received_at, outcome, detail, claimed_project_id, claimed_pipeline_id)
		VALUES ($1, $2, now() - $3::interval, 'unlinked', '', $4, 1001)`, task, key, age.String(), project); err != nil {
		e.t.Fatal(err)
	}
}

// declaredOf returns the pending declared key of a service, "" for none.
func (e *iEnv) declaredOf(id int64) string {
	e.t.Helper()
	var k pgtype.Text
	if err := e.pool.QueryRow(context.Background(), `SELECT sonar_key_declared FROM services WHERE id = $1`, id).Scan(&k); err != nil {
		e.t.Fatal(err)
	}
	return k.String
}

// A declared key confirmed by a SonarQube delivery of that key naming the
// same GitLab project (here: the analysis ran before the first upload) is
// given to the service the upload creates, in the same transaction, with an
// audit record of the pipeline's job, and the analyses of the key are
// linked. The waiting analyses of the pipeline are not linked by their
// claims then: the declared key wins. Declaring the same key again changes
// nothing.
func TestIntegrationSonarKeyDeclaredLinksKeylessService(t *testing.T) {
	l := &fakeLinker{}
	e := newIEnv(t, func(o *Options) { o.SonarLinker = l })
	e.sonarDelivery("task-1", "platform:payments-api", 42, time.Minute)
	code, res := e.uploadRaw(upload{body: sonarKeyBody, query: "?sonar_key=platform:payments-api"})
	if code != http.StatusCreated || res["service_created"] != true {
		t.Fatalf("%d %v", code, res)
	}
	if v, ok := res["sonar_key_ignored"]; !ok || v != nil {
		t.Errorf("sonar_key_ignored = %v (present %v)", v, ok)
	}
	sid := int64(res["service_id"].(float64))
	if k := e.sonarKeyOf(sid); k != "platform:payments-api" {
		t.Errorf("key %q", k)
	}
	want := []map[string]any{{
		"_actor": "ci:platform/payments-api", "id": float64(sid), "gitlab_project_path": "platform/payments-api",
		"fields": []any{"sonar_project_key"}, "sonar_project_key": "platform:payments-api",
		"reason":      "declared by the pipeline's upload and confirmed by a SonarQube analysis of the same project",
		"pipeline_id": float64(1001), "job_id": float64(2002),
	}}
	if got := e.auditOf("service.updated"); !reflect.DeepEqual(got, want) {
		t.Errorf("audit = %v\nwant %v", got, want)
	}
	if got, want := l.gotDeclared(), []declaredCall{{sid, 42, "platform:payments-api"}}; !slices.Equal(got, want) {
		t.Errorf("LinkDeclared calls %v, want %v", got, want)
	}
	if got := l.got(); len(got) != 0 {
		t.Errorf("LinkWaiting called after a declaration: %v", got)
	}

	code, res = e.uploadRaw(upload{body: sonarKeyBody, query: "?sonar_key=platform:payments-api",
		claims: func(c map[string]any) { c["job_id"], c["pipeline_id"] = "3000", "1002" }})
	if code != http.StatusCreated || res["sonar_key_ignored"] != nil {
		t.Fatalf("again: %d %v", code, res)
	}
	if n := len(e.auditOf("service.updated")); n != 1 {
		t.Errorf("%d service.updated events after the same key again", n)
	}
	if n := len(l.gotDeclared()); n != 1 {
		t.Errorf("%d LinkDeclared calls after the same key again", n)
	}
}

// The CI token proves which project uploads, not that it owns the key. A
// declaration without a SonarQube delivery of the key that names the same
// GitLab project in the last 7 days is kept pending: the upload is stored,
// the answer says sonar_key_pending, the declaration is audited once, and
// neither the declaration nor the claims of the pipeline's waiting analyses
// link anything. Once a delivery confirms it, a later upload links it.
func TestIntegrationSonarKeyPending(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		setup func(e *iEnv)
	}{
		{"no delivery", func(*iEnv) {}},
		{"deliveries name another project", func(e *iEnv) { e.sonarDelivery("task-b", "platform:payments-api", 99, time.Minute) }},
		{"delivery older than 7 days", func(e *iEnv) { e.sonarDelivery("task-old", "platform:payments-api", 42, 8*24*time.Hour) }},
		{"delivery without a claim", func(e *iEnv) {
			e.sonarDelivery("task-n", "platform:payments-api", 42, time.Minute)
			if _, err := e.pool.Exec(ctx, `UPDATE sonar_deliveries SET claimed_project_id = NULL`); err != nil {
				e.t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &fakeLinker{}
			e := newIEnv(t, func(o *Options) { o.SonarLinker = l })
			tc.setup(e)
			code, res := e.uploadRaw(upload{body: sonarKeyBody, query: "?sonar_key=platform:payments-api"})
			if code != http.StatusCreated || res["sonar_key_ignored"] != "sonar_key_pending" {
				t.Fatalf("%d %v", code, res)
			}
			sid := int64(res["service_id"].(float64))
			if k, d := e.sonarKeyOf(sid), e.declaredOf(sid); k != "" || d != "platform:payments-api" {
				t.Errorf("key %q declared %q", k, d)
			}
			want := []map[string]any{{
				"_actor": "ci:platform/payments-api", "id": float64(sid), "gitlab_project_path": "platform/payments-api",
				"fields": []any{"sonar_key_declared"}, "sonar_key_declared": "platform:payments-api",
				"reason":      "declared by the pipeline's upload; set when a SonarQube analysis of the same project confirms it",
				"pipeline_id": float64(1001), "job_id": float64(2002),
			}}
			if got := e.auditOf("service.updated"); !reflect.DeepEqual(got, want) {
				t.Errorf("audit = %v\nwant %v", got, want)
			}
			if len(l.got()) != 0 || len(l.gotDeclared()) != 0 {
				t.Errorf("linker called: waiting %v declared %v", l.got(), l.gotDeclared())
			}
			if !strings.Contains(e.logs.String(), "sonar_key_ignored=sonar_key_pending") {
				t.Errorf("not logged:\n%s", e.logs.String())
			}
			// The same declaration again is not audited again.
			if code, res := e.uploadRaw(upload{body: sonarKeyBody, query: "?sonar_key=platform:payments-api",
				claims: func(c map[string]any) { c["job_id"] = "3000" }}); code != 201 || res["sonar_key_ignored"] != "sonar_key_pending" {
				t.Fatalf("again: %d %v", code, res)
			}
			if n := len(e.auditOf("service.updated")); n != 1 {
				t.Errorf("%d service.updated events", n)
			}
			// A delivery of the key naming the project confirms it for the
			// next upload.
			e.sonarDelivery("task-ok", "platform:payments-api", 42, 0)
			if code, res := e.uploadRaw(upload{body: sonarKeyBody, query: "?sonar_key=platform:payments-api",
				claims: func(c map[string]any) { c["job_id"] = "3001" }}); code != 201 || res["sonar_key_ignored"] != nil {
				t.Fatalf("confirmed: %d %v", code, res)
			}
			if k := e.sonarKeyOf(sid); k != "platform:payments-api" {
				t.Errorf("key %q after the confirmation", k)
			}
		})
	}
}

// A key another service has, archived or not, is never taken. A service that
// has another key keeps it. The upload is stored all the same, says why the
// key was ignored, and the event is audited and logged; the waiting analyses
// are not linked by their claims either.
func TestIntegrationSonarKeyIgnored(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, setup, reason string
	}{
		{"held by another service", `INSERT INTO services (name, gitlab_project_path, sonar_project_key, created_via)
			VALUES ('legacy', 'platform/legacy', 'platform:payments-api', 'manual')`, "key_linked_to_other_service"},
		{"held by an archived service", `INSERT INTO services (name, gitlab_project_path, sonar_project_key, created_via, archived)
			VALUES ('legacy', 'platform/legacy', 'platform:payments-api', 'manual', true)`, "key_linked_to_other_service"},
		{"service has another key", `INSERT INTO services (name, gitlab_project_id, gitlab_project_path, sonar_project_key, created_via)
			VALUES ('payments-api', 42, 'platform/payments-api', 'platform:renamed', 'manual')`, "service_has_other_key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := &fakeLinker{}
			e := newIEnv(t, func(o *Options) { o.SonarLinker = l })
			if _, err := e.pool.Exec(ctx, tc.setup); err != nil {
				t.Fatal(err)
			}
			code, res := e.uploadRaw(upload{body: sonarKeyBody, query: "?sonar_key=platform:payments-api"})
			if code != http.StatusCreated || res["sonar_key_ignored"] != tc.reason {
				t.Fatalf("%d %v, want 201 with %s", code, res, tc.reason)
			}
			sid := int64(res["service_id"].(float64))
			wantKey := ""
			if tc.reason == "service_has_other_key" {
				wantKey = "platform:renamed"
			}
			if k := e.sonarKeyOf(sid); k != wantKey {
				t.Errorf("key %q, want %q", k, wantKey)
			}
			if n := e.count("scans"); n != 1 {
				t.Errorf("%d scans stored", n)
			}
			if got := e.auditOf("service.updated"); len(got) != 0 {
				t.Errorf("service.updated: %v", got)
			}
			got := e.auditOf("ingest.sonar_key_ignored")
			want := map[string]any{
				"_actor": "ci:platform/payments-api", "id": float64(sid), "gitlab_project_path": "platform/payments-api",
				"sonar_project_key": "platform:payments-api", "reason": tc.reason,
				"pipeline_id": float64(1001), "job_id": float64(2002),
			}
			if wantKey != "" {
				want["current_sonar_project_key"] = wantKey
			}
			if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
				t.Errorf("audit = %v\nwant %v", got, want)
			}
			if !strings.Contains(e.logs.String(), "sonar_key_ignored="+tc.reason) {
				t.Errorf("not logged:\n%s", e.logs.String())
			}
			if len(l.got()) != 0 || len(l.gotDeclared()) != 0 {
				t.Errorf("linker called: waiting %v declared %v", l.got(), l.gotDeclared())
			}
		})
	}
}

// A pending declaration whose key another service has taken meanwhile is
// dropped by the project's next upload that declares it, so it stops
// blocking the links by the scanner properties; the event says so.
func TestIntegrationSonarKeyTakenClearsDeclaration(t *testing.T) {
	e := newIEnv(t, nil)
	code, res := e.uploadRaw(upload{body: sonarKeyBody, query: "?sonar_key=platform:payments-api"})
	if code != http.StatusCreated || res["sonar_key_ignored"] != "sonar_key_pending" {
		t.Fatalf("%d %v", code, res)
	}
	sid := int64(res["service_id"].(float64))
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO services (name, gitlab_project_path, sonar_project_key, created_via)
		VALUES ('legacy', 'platform/legacy', 'platform:payments-api', 'manual')`); err != nil {
		t.Fatal(err)
	}
	code, res = e.uploadRaw(upload{body: sonarKeyBody, query: "?sonar_key=platform:payments-api",
		claims: func(c map[string]any) { c["job_id"] = "3000" }})
	if code != http.StatusCreated || res["sonar_key_ignored"] != "key_linked_to_other_service" {
		t.Fatalf("%d %v", code, res)
	}
	if d := e.declaredOf(sid); d != "" {
		t.Errorf("declaration %q kept", d)
	}
	got := e.auditOf("ingest.sonar_key_ignored")
	if len(got) != 1 || got[0]["cleared_sonar_key_declared"] != "platform:payments-api" {
		t.Errorf("audit = %v", got)
	}
}

// An ignored key is audited once for each new reason or key, not on every
// upload; every upload still logs the warning.
func TestIntegrationSonarKeyIgnoredAuditedOnce(t *testing.T) {
	e := newIEnv(t, nil)
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO services (name, gitlab_project_id, gitlab_project_path, sonar_project_key, created_via)
		VALUES ('payments-api', 42, 'platform/payments-api', 'platform:renamed', 'manual')`); err != nil {
		t.Fatal(err)
	}
	for i, key := range []string{"platform:payments-api", "platform:payments-api", "platform:payments-api", "platform:other", "platform:payments-api"} {
		code, res := e.uploadRaw(upload{body: sonarKeyBody, query: "?sonar_key=" + key,
			claims: func(c map[string]any) { c["job_id"] = strconv.Itoa(3000 + i) }})
		if code != http.StatusCreated || res["sonar_key_ignored"] != "service_has_other_key" {
			t.Fatalf("upload %d: %d %v", i, code, res)
		}
	}
	var keys []any
	for _, d := range e.auditOf("ingest.sonar_key_ignored") {
		keys = append(keys, d["sonar_project_key"])
	}
	if want := []any{"platform:payments-api", "platform:other", "platform:payments-api"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("audited keys %v, want %v", keys, want)
	}
	if n := strings.Count(e.logs.String(), "sonar_key_ignored=service_has_other_key"); n != 5 {
		t.Errorf("%d warnings, want 5", n)
	}
}

// A webhook that links the service to another key while the upload runs is
// either seen by the upload, which then answers service_has_other_key, or
// refused by the declaration: the upload decides on the service row it has
// locked.
func TestIntegrationSonarKeyRaceWithWebhookPin(t *testing.T) {
	ctx := context.Background()
	e := newIEnv(t, nil)
	e.mustUpload(upload{body: sonarKeyBody})
	var sid int64
	if err := e.pool.QueryRow(ctx, `SELECT id FROM services WHERE gitlab_project_id = 42`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	e.sonarDelivery("task-1", "platform:payments-api", 42, time.Minute)

	// The webhook's pin is in flight and holds the row.
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE services SET sonar_project_key = 'evil:project' WHERE id = $1`, sid); err != nil {
		t.Fatal(err)
	}
	type answer struct {
		code int
		res  map[string]any
	}
	done := make(chan answer, 1)
	go func() {
		code, res := e.uploadRaw(upload{body: sonarKeyBody, query: "?sonar_key=platform:payments-api",
			claims: func(c map[string]any) { c["job_id"] = "3000" }})
		done <- answer{code, res}
	}()
	// Wait until the upload waits for the row lock, then commit the pin.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		if err := e.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND query LIKE '%FOR NO KEY UPDATE%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the upload never waited for the service row")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	a := <-done
	if a.code != http.StatusCreated || a.res["sonar_key_ignored"] != "service_has_other_key" {
		t.Fatalf("upload = %d %v", a.code, a.res)
	}
	if k, d := e.sonarKeyOf(sid), e.declaredOf(sid); k != "evil:project" || d != "" {
		t.Errorf("key %q declared %q", k, d)
	}
	got := e.auditOf("ingest.sonar_key_ignored")
	if len(got) != 1 || got[0]["current_sonar_project_key"] != "evil:project" {
		t.Errorf("audit = %v", got)
	}
}

// holdRow runs sql on the service row in a transaction that stays open,
// starts an upload that declares a key, waits until the upload waits for the
// row, commits, and returns the upload's answer.
func (e *iEnv) holdRowDuringDeclaration(sql string, args ...any) (int, map[string]any) {
	e.t.Helper()
	ctx := context.Background()
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		e.t.Fatal(err)
	}
	type answer struct {
		code int
		res  map[string]any
	}
	done := make(chan answer, 1)
	go func() {
		code, res := e.uploadRaw(upload{body: sonarKeyBody, query: "?sonar_key=platform:payments-api",
			claims: func(c map[string]any) { c["job_id"] = "3000" }})
		done <- answer{code, res}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		if err := e.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND query LIKE '%FOR NO KEY UPDATE%')`).Scan(&waiting); err != nil {
			e.t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			e.t.Fatal("the upload never waited for the service row")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		e.t.Fatal(err)
	}
	a := <-done
	return a.code, a.res
}

// A service archived while the upload runs, after the upload found it but
// before it locked it for the declared key, is answered as an archived
// service is (409 service_archived), not with an internal error, and
// nothing is stored.
func TestIntegrationSonarKeyServiceArchivedMeanwhile(t *testing.T) {
	e := newIEnv(t, nil)
	e.mustUpload(upload{body: sonarKeyBody})
	scans := e.count("scans")
	var sid int64
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM services WHERE gitlab_project_id = 42`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	code, res := e.holdRowDuringDeclaration(`UPDATE services SET archived = true WHERE id = $1`, sid)
	if errObj, _ := res["error"].(map[string]any); code != http.StatusConflict || errObj["code"] != "service_archived" {
		t.Fatalf("upload = %d %v, want 409 service_archived", code, res)
	}
	if n := e.count("scans"); n != scans {
		t.Errorf("%d scans, want %d", n, scans)
	}
	if k, d := e.sonarKeyOf(sid), e.declaredOf(sid); k != "" || d != "" {
		t.Errorf("key %q declared %q", k, d)
	}
}

// A service that is gone when the upload locks it leaves the declared key
// alone; the rest of the upload goes on as without one.
func TestIntegrationSonarKeyServiceGone(t *testing.T) {
	ctx := context.Background()
	e := newIEnv(t, nil)
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	c := &Claims{ProjectID: 42, ProjectPath: "platform/payments-api", PipelineID: 1001, JobID: 2002}
	set, notSet, err := declareSonarKey(ctx, tx, store.Service{ID: 999}, c, "platform:payments-api")
	if set || notSet != nil || err != nil {
		t.Errorf("declareSonarKey on a missing service = %v %v %v", set, notSet, err)
	}
}

// adminChangedKey records an administrator's change of the service's key,
// as the service API does.
func (e *iEnv) adminChangedKey(sid int64, key string) {
	e.t.Helper()
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `UPDATE services SET sonar_project_key = $2 WHERE id = $1`, sid, key); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO audit_log (actor, action, detail)
		VALUES ('local:root', 'service.updated', jsonb_build_object('id', $1::bigint, 'fields', jsonb_build_array('sonar_project_key')))`, sid); err != nil {
		e.t.Fatal(err)
	}
}

// The de-duplication of ingest.sonar_key_ignored lasts only while the
// service does not change: after an administrator changed its key, even
// back to the same one, the same ignored key is a new situation and is
// audited again.
func TestIntegrationSonarKeyIgnoredAuditedAgainAfterChange(t *testing.T) {
	e := newIEnv(t, nil)
	if _, err := e.pool.Exec(context.Background(), `INSERT INTO services (name, gitlab_project_id, gitlab_project_path, sonar_project_key, created_via)
		VALUES ('payments-api', 42, 'platform/payments-api', 'platform:renamed', 'manual')`); err != nil {
		t.Fatal(err)
	}
	var sid int64
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM services WHERE gitlab_project_id = 42`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	up := func(job int) {
		t.Helper()
		code, res := e.uploadRaw(upload{body: sonarKeyBody, query: "?sonar_key=platform:payments-api",
			claims: func(c map[string]any) { c["job_id"] = strconv.Itoa(job) }})
		if code != http.StatusCreated || res["sonar_key_ignored"] != "service_has_other_key" {
			t.Fatalf("upload %d: %d %v", job, code, res)
		}
	}
	up(3000)
	up(3001)
	if n := len(e.auditOf("ingest.sonar_key_ignored")); n != 1 {
		t.Fatalf("%d events before the change, want 1", n)
	}
	e.adminChangedKey(sid, "platform:other")
	e.adminChangedKey(sid, "platform:renamed")
	up(3002)
	up(3003)
	if n := len(e.auditOf("ingest.sonar_key_ignored")); n != 2 {
		t.Errorf("%d events after the change, want 2", n)
	}
}

// The lock an upload takes on its service for the declared key does not
// block rows that reference the service, such as the scan of a concurrent
// upload of another job.
func TestIntegrationSonarKeyLockAllowsScans(t *testing.T) {
	ctx := context.Background()
	e := newIEnv(t, nil)
	e.mustUpload(upload{body: sonarKeyBody})
	var sid int64
	if err := e.pool.QueryRow(ctx, `SELECT id FROM services WHERE gitlab_project_id = 42`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := store.New(tx).LockServiceSonarKey(ctx, sid); err != nil {
		t.Fatal(err)
	}
	other, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Rollback(ctx) }()
	if _, err := other.Exec(ctx, `SET LOCAL lock_timeout = '1s'`); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Exec(ctx, `INSERT INTO scans (service_id, kind, artifact_name, artifact_key, gitlab_pipeline_id, gitlab_job_id,
		ref, ref_protected, commit_sha, schema_version, scanned_at, raw_report)
		VALUES ($1, 'image', 'b', 'b', 1002, 4000, 'main', true, 'c0ffee', 2, now(), '\x1f8b')`, sid); err != nil {
		t.Errorf("a scan of the service waited for the lock: %v", err)
	}
}
