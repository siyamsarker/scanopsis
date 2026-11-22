// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package ingest

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
)

type auditRow struct {
	actor, action string
	detail        map[string]any
}

func (e *iEnv) auditRows(action string) []auditRow {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT actor, action, detail FROM audit_log WHERE action = $1 ORDER BY id`, action)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		var raw []byte
		if err := rows.Scan(&r.actor, &r.action, &raw); err != nil {
			e.t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &r.detail); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// A service created by an upload is recorded in the audit log, once, with the
// project as the actor; later uploads and other creations add nothing.
func TestIntegrationServiceCreatedIsAudited(t *testing.T) {
	e := newIEnv(t, nil)
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	res := e.mustUpload(upload{body: body})
	rows := e.auditRows("service.created")
	if len(rows) != 1 {
		t.Fatalf("%d records: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.actor != "ci:platform/payments-api" || r.detail["id"] != float64(res.ServiceID) ||
		r.detail["gitlab_project_path"] != "platform/payments-api" || r.detail["gitlab_project_id"] != float64(42) {
		t.Errorf("record = %+v", r)
	}
	// A second upload finds the service and records nothing.
	e.mustUpload(upload{body: body, claims: func(c map[string]any) { c["job_id"] = "3000" }})
	if n := len(e.auditRows("service.created")); n != 1 {
		t.Errorf("%d records after a second upload", n)
	}
	// Another project creates another service and another record.
	e.mustUpload(upload{body: body, claims: func(c map[string]any) { c["project_id"] = "43"; c["project_path"] = "platform/ledger" }})
	rows = e.auditRows("service.created")
	if len(rows) != 2 || rows[1].actor != "ci:platform/ledger" {
		t.Errorf("records = %+v", rows)
	}
}

// Many first uploads at once create one service and one record.
func TestIntegrationConcurrentFirstUploadsAuditOnce(t *testing.T) {
	e := newIEnv(t, nil)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, b := e.upload(upload{body: []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`), claims: func(c map[string]any) { c["job_id"] = string(rune('1' + i)) }})
			if code != http.StatusCreated {
				t.Errorf("%d %s", code, b)
			}
		}()
	}
	wg.Wait()
	if n := len(e.auditRows("service.created")); n != 1 || e.count("services") != 1 {
		t.Errorf("%d records for %d services", n, e.count("services"))
	}
}

// The record is part of the transaction: an upload that fails after the
// service was created leaves neither the service nor the record.
func TestIntegrationFailedUploadLeavesNoServiceRecord(t *testing.T) {
	e := newIEnv(t, nil)
	if _, err := e.pool.Exec(context.Background(), `ALTER TABLE vulnerabilities RENAME TO vulnerabilities_gone`); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a","Results":[{"Target":"t","Vulnerabilities":[{"VulnerabilityID":"CVE-1"}]}]}`)
	if code, b := e.upload(upload{body: body}); code != http.StatusInternalServerError {
		t.Fatalf("%d %s", code, b)
	}
	if n := e.count("services"); n != 0 {
		t.Errorf("%d services stayed", n)
	}
	if n := len(e.auditRows("service.created")); n != 0 {
		t.Errorf("%d records stayed", n)
	}
}
