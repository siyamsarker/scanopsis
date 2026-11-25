// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package ingest

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/store"
)

// uploadAnswer is the status and decoded body of an upload.
type uploadAnswer struct {
	code int
	res  map[string]any
}

// startUpload runs the upload u in the background.
func (e *iEnv) startUpload(u upload) <-chan uploadAnswer {
	done := make(chan uploadAnswer, 1)
	go func() {
		code, body := e.upload(u)
		a := uploadAnswer{code: code}
		_ = json.Unmarshal(body, &a.res)
		done <- a
	}()
	return done
}

// backendPID returns the server process of a transaction.
func (e *iEnv) backendPID(tx pgx.Tx) int {
	e.t.Helper()
	var pid int
	if err := tx.QueryRow(context.Background(), `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		e.t.Fatal(err)
	}
	return pid
}

// waitBlockedBy waits until a server process waits for a lock that the
// process holder holds, and returns the waiting one. It fails the test when
// done delivers first: the work it waits for finished without waiting.
func (e *iEnv) waitBlockedBy(holder int, done <-chan uploadAnswer, what string) int {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case a := <-done:
			e.t.Fatalf("%s answered %d %v without waiting", what, a.code, a.res)
		default:
		}
		var pid int
		err := e.pool.QueryRow(context.Background(), `SELECT pid FROM pg_stat_activity
			WHERE $1 = ANY(pg_blocking_pids(pid)) LIMIT 1`, holder).Scan(&pid)
		if err == nil {
			return pid
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("%s never waited", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// holdRowDuringUpload runs sql in a transaction that stays open, starts the
// upload u, waits until the upload waits for that transaction, commits, and
// returns the upload's answer. An upload that does not wait fails the test.
func (e *iEnv) holdRowDuringUpload(u upload, sql string, args ...any) (int, map[string]any) {
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
	done := e.startUpload(u)
	e.waitBlockedBy(e.backendPID(tx), done, "the upload")
	if err := tx.Commit(ctx); err != nil {
		e.t.Fatal(err)
	}
	a := <-done
	return a.code, a.res
}

// serviceOf returns the ID of the service of GitLab project 42.
func (e *iEnv) serviceOf() int64 {
	e.t.Helper()
	var sid int64
	if err := e.pool.QueryRow(context.Background(), `SELECT id FROM services WHERE gitlab_project_id = 42`).Scan(&sid); err != nil {
		e.t.Fatal(err)
	}
	return sid
}

// A service deleted after the upload found it and before the upload locked
// it is created again, as by the first upload of the project, and the scan
// is stored: the upload is answered 201, not with an internal error.
func TestIntegrationUploadServiceDeletedMeanwhile(t *testing.T) {
	e := newIEnv(t, nil)
	e.mustUpload(upload{body: sonarKeyBody})
	sid := e.serviceOf()
	code, res := e.holdRowDuringUpload(upload{body: sonarKeyBody, claims: func(c map[string]any) { c["job_id"] = "3000" }},
		`DELETE FROM services WHERE id = $1`, sid)
	if code != http.StatusCreated || res["service_created"] != true {
		t.Fatalf("upload = %d %v, want 201 with a new service", code, res)
	}
	nid := int64(res["service_id"].(float64))
	if nid == sid || e.serviceOf() != nid || e.count("services") != 1 {
		t.Errorf("service %d (was %d), %d services", nid, sid, e.count("services"))
	}
	var scans int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM scans WHERE service_id = $1 AND gitlab_job_id = 3000`, nid).Scan(&scans); err != nil || scans != 1 {
		t.Errorf("%d scans of the new service (%v)", scans, err)
	}
	rows := e.auditRows("service.created")
	if len(rows) != 2 {
		t.Fatalf("%d service.created records: %+v", len(rows), rows)
	}
	if r := rows[1]; r.actor != "ci:platform/payments-api" || r.detail["id"] != float64(nid) ||
		r.detail["reason"] != "created by the first verified upload" || r.detail["gitlab_project_id"] != float64(42) {
		t.Errorf("record = %+v", r)
	}
}

// An upload that declares a key for a service deleted meanwhile declares it
// on the service it creates again.
func TestIntegrationSonarKeyServiceDeletedMeanwhile(t *testing.T) {
	e := newIEnv(t, nil)
	e.mustUpload(upload{body: sonarKeyBody})
	sid := e.serviceOf()
	code, res := e.holdRowDuringDeclaration(`DELETE FROM services WHERE id = $1`, sid)
	if code != http.StatusCreated || res["service_created"] != true || res["sonar_key_ignored"] != "sonar_key_pending" {
		t.Fatalf("upload = %d %v", code, res)
	}
	if d := e.declaredOf(int64(res["service_id"].(float64))); d != "platform:payments-api" {
		t.Errorf("declared %q", d)
	}
}

// A service archived after the upload found it and before the upload locked
// it is answered as an archived service is (409 service_archived), with or
// without a declared key, and nothing is stored.
func TestIntegrationUploadServiceArchivedMeanwhile(t *testing.T) {
	e := newIEnv(t, nil)
	e.mustUpload(upload{body: sonarKeyBody})
	scans := e.count("scans")
	sid := e.serviceOf()
	code, res := e.holdRowDuringUpload(upload{body: sonarKeyBody, claims: func(c map[string]any) { c["job_id"] = "3000" }},
		`UPDATE services SET archived = true WHERE id = $1`, sid)
	if errObj, _ := res["error"].(map[string]any); code != http.StatusConflict || errObj["code"] != "service_archived" {
		t.Fatalf("upload = %d %v, want 409 service_archived", code, res)
	}
	if n := e.count("scans"); n != scans {
		t.Errorf("%d scans, want %d", n, scans)
	}
}

// An administrator's archive of a service while an upload of it runs waits
// for the upload: the scan is kept and the service is archived afterwards.
func TestIntegrationArchiveWaitsForUpload(t *testing.T) {
	ctx := context.Background()
	e := newIEnv(t, nil)
	first := e.mustUpload(upload{body: sonarKeyBody})
	sid := first.ServiceID

	// Hold the scan the next upload replaces, so the upload stops after it
	// locked its service and before it stores anything.
	hold, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM scans WHERE id = $1 FOR UPDATE`, first.ScanID); err != nil {
		t.Fatal(err)
	}
	done := e.startUpload(upload{body: sonarKeyBody})
	uploader := e.waitBlockedBy(e.backendPID(hold), done, "the upload")

	// The administrator archives the service, as the service API does.
	archive, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Rollback(ctx) }()
	archiver := e.backendPID(archive)
	archived := make(chan error, 1)
	go func() {
		_, err := store.New(archive).UpdateService(ctx, store.UpdateServiceParams{ID: sid, Archived: pgtype.Bool{Bool: true, Valid: true}})
		if err == nil {
			err = archive.Commit(ctx)
		}
		archived <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-archived:
			t.Fatalf("the archive did not wait for the upload (%v)", err)
		default:
		}
		var waits bool
		if err := e.pool.QueryRow(ctx, `SELECT $1 = ANY(pg_blocking_pids($2))`, uploader, archiver).Scan(&waits); err != nil {
			t.Fatal(err)
		}
		if waits {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the archive never waited for the upload")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	a := <-done
	if a.code != http.StatusCreated || a.res["replaced"] != true {
		t.Fatalf("upload = %d %v", a.code, a.res)
	}
	if err := <-archived; err != nil {
		t.Fatalf("archive: %v", err)
	}
	var isArchived bool
	var scans int
	if err := e.pool.QueryRow(ctx, `SELECT archived, (SELECT count(*) FROM scans WHERE service_id = $1)
		FROM services WHERE id = $1`, sid).Scan(&isArchived, &scans); err != nil {
		t.Fatal(err)
	}
	if !isArchived || scans != 1 {
		t.Errorf("archived %v with %d scans, want archived with 1", isArchived, scans)
	}
}

// An administrator's delete of a service while an upload of it runs waits
// for the upload, then removes the service with the scan the upload stored:
// the upload is answered 201 and the delete succeeds, without a reference
// error on either side.
func TestIntegrationDeleteWaitsForUpload(t *testing.T) {
	ctx := context.Background()
	e := newIEnv(t, nil)
	first := e.mustUpload(upload{body: sonarKeyBody})
	sid := first.ServiceID

	// Hold the scan the next upload replaces, so the upload stops after it
	// locked its service and before it stores anything.
	hold, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hold.Rollback(ctx) }()
	if _, err := hold.Exec(ctx, `SELECT 1 FROM scans WHERE id = $1 FOR UPDATE`, first.ScanID); err != nil {
		t.Fatal(err)
	}
	done := e.startUpload(upload{body: sonarKeyBody})
	uploader := e.waitBlockedBy(e.backendPID(hold), done, "the upload")

	// The administrator deletes the service, as the service API does.
	conn, err := e.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var deleter int
	if err := conn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&deleter); err != nil {
		t.Fatal(err)
	}
	deleted := make(chan error, 1)
	go func() {
		_, err := store.New(conn).DeleteService(ctx, sid)
		deleted <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case err := <-deleted:
			t.Fatalf("the delete did not wait for the upload (%v)", err)
		default:
		}
		var waits bool
		if err := e.pool.QueryRow(ctx, `SELECT $1 = ANY(pg_blocking_pids($2))`, uploader, deleter).Scan(&waits); err != nil {
			t.Fatal(err)
		}
		if waits {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the delete never waited for the upload")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if a := <-done; a.code != http.StatusCreated {
		t.Fatalf("upload = %d %v", a.code, a.res)
	}
	if err := <-deleted; err != nil {
		t.Fatalf("delete: %v", err)
	}
	var services, scans int
	if err := e.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM services WHERE id = $1),
		(SELECT count(*) FROM scans WHERE service_id = $1)`, sid).Scan(&services, &scans); err != nil {
		t.Fatal(err)
	}
	if services != 0 || scans != 0 {
		t.Errorf("%d services and %d scans left, want none", services, scans)
	}
}
