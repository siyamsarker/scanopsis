// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// A SonarQube key the pipeline declared and that waits for confirmation is
// shown on the service, read only, so an administrator sees why the scanner
// properties link nothing else. Setting the key, or clearing it, also clears
// the declaration: the administrator's choice wins. Other changes keep it.
func TestUpdateServiceClearsDeclaredSonarKey(t *testing.T) {
	c, pool, rec := newCatalog(t, "")
	sid := addService(t, pool, "orders-api", "demo/orders-api", false)
	id := fmt.Sprint(sid)
	mustExec(t, pool, `UPDATE services SET sonar_key_declared = 'demo:orders-api', sonar_key_declared_at = now() WHERE id = $1`, sid)

	type declared struct {
		Key    *string `json:"sonar_project_key"`
		Decl   *string `json:"sonar_key_declared"`
		DeclAt *string `json:"sonar_key_declared_at"`
	}
	g := decode[declared](t, call(t, c, "getService", "GET", "/", "", id), http.StatusOK)
	if g.Decl == nil || *g.Decl != "demo:orders-api" || g.DeclAt == nil {
		t.Fatalf("get = %+v", g)
	}
	// A rename keeps the declaration, and only null may be written to it.
	for _, v := range []string{`"x:y"`, `""`, `5`, `false`, `{}`} {
		r := call(t, c, "updateService", "PATCH", "/", `{"sonar_key_declared":`+v+`}`, id)
		if e := decode[errJSON](t, r, http.StatusBadRequest); e.Error.Code != "invalid_parameter" {
			t.Errorf("writing sonar_key_declared %s = %+v", v, e)
		}
	}
	u := decode[declared](t, call(t, c, "updateService", "PATCH", "/", `{"name":"Orders"}`, id), http.StatusOK)
	if u.Decl == nil {
		t.Errorf("rename dropped the declaration: %+v", u)
	}

	u = decode[declared](t, call(t, c, "updateService", "PATCH", "/", `{"sonar_project_key":"demo:orders-y"}`, id), http.StatusOK)
	if u.Key == nil || *u.Key != "demo:orders-y" || u.Decl != nil || u.DeclAt != nil {
		t.Errorf("set = %+v", u)
	}
	var decl *string
	if err := pool.QueryRow(context.Background(), `SELECT sonar_key_declared FROM services WHERE id = $1`, sid).Scan(&decl); err != nil || decl != nil {
		t.Errorf("stored declaration %v (%v)", decl, err)
	}
	last := rec.events[len(rec.events)-1]
	if last.action != "service.updated" || last.detail["cleared_sonar_key_declared"] != "demo:orders-api" {
		t.Errorf("audit = %+v", last)
	}

	// Clearing the key clears a declaration made since, too.
	mustExec(t, pool, `UPDATE services SET sonar_key_declared = 'demo:orders-api', sonar_key_declared_at = now() WHERE id = $1`, sid)
	u = decode[declared](t, call(t, c, "updateService", "PATCH", "/", `{"sonar_project_key":null}`, id), http.StatusOK)
	if u.Key != nil || u.Decl != nil {
		t.Errorf("clear = %+v", u)
	}
	if err := pool.QueryRow(context.Background(), `SELECT sonar_key_declared FROM services WHERE id = $1`, sid).Scan(&decl); err != nil || decl != nil {
		t.Errorf("stored declaration after clearing %v (%v)", decl, err)
	}
}

// keysJSON is the SonarQube key and waiting declaration of a service.
type keysJSON struct {
	Key    *string `json:"sonar_project_key"`
	Decl   *string `json:"sonar_key_declared"`
	DeclAt *string `json:"sonar_key_declared_at"`
}

// declaredRow is the stored key and declaration of a service.
func declaredRow(t *testing.T, c *Catalog, sid int64) (key, declared *string) {
	t.Helper()
	if err := c.pool.QueryRow(context.Background(), `SELECT sonar_project_key, sonar_key_declared FROM services WHERE id = $1`, sid).Scan(&key, &declared); err != nil {
		t.Fatal(err)
	}
	return key, declared
}

// Cancelling a waiting key (sonar_key_declared null) clears the declaration
// of a service without a key, and is audited with the key it cleared.
func TestCancelWaitingSonarKey(t *testing.T) {
	c, pool, rec := newCatalog(t, "")
	sid := addService(t, pool, "orders-api", "demo/orders-api", false)
	id := fmt.Sprint(sid)
	mustExec(t, pool, `UPDATE services SET sonar_key_declared = 'demo:orders-api', sonar_key_declared_at = now() WHERE id = $1`, sid)

	u := decode[keysJSON](t, call(t, c, "updateService", "PATCH", "/", `{"sonar_key_declared":null}`, id), http.StatusOK)
	if u.Key != nil || u.Decl != nil || u.DeclAt != nil {
		t.Errorf("answer = %+v", u)
	}
	if key, decl := declaredRow(t, c, sid); key != nil || decl != nil {
		t.Errorf("stored key %v declared %v", key, decl)
	}
	if len(rec.events) != 1 {
		t.Fatalf("events = %+v", rec.events)
	}
	e := rec.events[0]
	if e.actor != "local:root" || e.action != "service.updated" || e.detail["cleared_sonar_key_declared"] != "demo:orders-api" ||
		fmt.Sprint(e.detail["fields"]) != "[sonar_key_declared]" || e.detail["id"] != sid {
		t.Errorf("event = %+v", e)
	}

	// Nothing waits any more: nothing changes and nothing is recorded.
	decode[keysJSON](t, call(t, c, "updateService", "PATCH", "/", `{"sonar_key_declared":null}`, id), http.StatusOK)
	if len(rec.events) != 1 {
		t.Errorf("events = %+v", rec.events)
	}
	// A cancel with a rename records the rename only.
	mustExec(t, pool, `UPDATE services SET sonar_key_declared = NULL WHERE id = $1`, sid)
	decode[keysJSON](t, call(t, c, "updateService", "PATCH", "/", `{"name":"Orders","sonar_key_declared":null}`, id), http.StatusOK)
	if len(rec.events) != 2 || fmt.Sprint(rec.events[1].detail["fields"]) != "[name]" || rec.events[1].detail["cleared_sonar_key_declared"] != nil {
		t.Errorf("events = %+v", rec.events)
	}
	if r := call(t, c, "updateService", "PATCH", "/", `{"sonar_key_declared":null}`, "999999"); r.Code != http.StatusNotFound {
		t.Errorf("unknown service = %d %s", r.Code, r.Body)
	}
}

// A cancel never unlinks: when SonarQube confirmed the waiting key and the
// webhook linked it before the cancel, the key and the declaration stay,
// nothing is recorded, and the answer is the linked service.
func TestCancelWaitingSonarKeyAfterLink(t *testing.T) {
	c, pool, rec := newCatalog(t, "")
	sid := addService(t, pool, "orders-api", "demo/orders-api", false)
	mustExec(t, pool, `UPDATE services SET sonar_key_declared = 'demo:orders-api', sonar_key_declared_at = now() WHERE id = $1`, sid)
	// The webhook links the declared key and keeps the declaration.
	mustExec(t, pool, `UPDATE services SET sonar_project_key = 'demo:orders-api' WHERE id = $1`, sid)

	u := decode[keysJSON](t, call(t, c, "updateService", "PATCH", "/", `{"sonar_key_declared":null}`, fmt.Sprint(sid)), http.StatusOK)
	if u.Key == nil || *u.Key != "demo:orders-api" || u.Decl != nil {
		t.Errorf("answer = %+v", u)
	}
	if key, decl := declaredRow(t, c, sid); key == nil || *key != "demo:orders-api" || decl == nil || *decl != "demo:orders-api" {
		t.Errorf("stored key %v declared %v", key, decl)
	}
	if len(rec.events) != 0 {
		t.Errorf("events = %+v", rec.events)
	}
}

// A cancel that arrives while the webhook links the waiting key waits for
// the link and then keeps it.
func TestCancelWaitingSonarKeyRaceWithLink(t *testing.T) {
	ctx := context.Background()
	c, pool, rec := newCatalog(t, "")
	sid := addService(t, pool, "orders-api", "demo/orders-api", false)
	mustExec(t, pool, `UPDATE services SET sonar_key_declared = 'demo:orders-api', sonar_key_declared_at = now() WHERE id = $1`, sid)

	pin, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pin.Rollback(ctx) }()
	if _, err := pin.Exec(ctx, `UPDATE services SET sonar_project_key = 'demo:orders-api' WHERE id = $1`, sid); err != nil {
		t.Fatal(err)
	}
	var holder int
	if err := pin.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holder); err != nil {
		t.Fatal(err)
	}
	done := make(chan keysJSON, 1)
	go func() {
		r := call(t, c, "updateService", "PATCH", "/", `{"sonar_key_declared":null}`, fmt.Sprint(sid))
		var u keysJSON
		if r.Code != http.StatusOK || json.Unmarshal(r.Body.Bytes(), &u) != nil {
			t.Errorf("cancel = %d %s", r.Code, r.Body)
		}
		done <- u
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)))`, holder).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the cancel never waited for the link")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := pin.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	u := <-done
	if u.Key == nil || *u.Key != "demo:orders-api" {
		t.Errorf("answer = %+v", u)
	}
	if key, decl := declaredRow(t, c, sid); key == nil || *key != "demo:orders-api" || decl == nil {
		t.Errorf("stored key %v declared %v", key, decl)
	}
	if len(rec.events) != 0 {
		t.Errorf("events = %+v", rec.events)
	}
}
