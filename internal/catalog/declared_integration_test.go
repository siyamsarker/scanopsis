// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package catalog

import (
	"context"
	"fmt"
	"net/http"
	"testing"
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
	// A rename keeps the declaration, and an unknown field cannot set it.
	if r := call(t, c, "updateService", "PATCH", "/", `{"sonar_key_declared":"x:y"}`, id); r.Code != http.StatusBadRequest {
		t.Errorf("writing sonar_key_declared = %d %s", r.Code, r.Body)
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
