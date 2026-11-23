// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/backup"
	"github.com/siyamsarker/scanopsis/internal/config"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

var restoreTestKey = bytes.Repeat([]byte{7}, 32)

// runServer runs serveApp against the database at dbURL until the returned
// stop function is called; stop returns what serveApp returned.
func runServer(t *testing.T, dbURL, backupDir string) (base string, logs *syncBuffer, stop func() error) {
	t.Helper()
	addr := freeAddr(t)
	cfg, err := config.Load(env(map[string]string{
		"SCANOPSIS_DATABASE_URL": dbURL,
		"SCANOPSIS_PUBLIC_URL":   "http://" + addr,
		"SCANOPSIS_LISTEN_ADDR":  addr,
		"SCANOPSIS_SECRET_KEY":   base64.StdEncoding.EncodeToString(restoreTestKey),
		"SCANOPSIS_BACKUP_DIR":   backupDir,
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	logs = &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveApp(ctx, cfg, slog.New(slog.NewJSONHandler(logs, nil))) }()
	stopped := false
	stop = func() error {
		if stopped {
			return nil
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(30 * time.Second):
			t.Fatal("the server did not stop")
			return nil
		}
	}
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Errorf("serveApp returned %v", err)
		}
	})
	base = "http://" + addr
	deadline := time.Now().Add(20 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(t.Context(), "GET", base+"/readyz", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return base, logs, stop
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("server not ready; log:\n%s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// backupOf takes a backup of pool with the test key and returns its bytes.
func backupOf(t *testing.T, pool *pgxpool.Pool) []byte {
	t.Helper()
	fp, err := backup.KeyFingerprint(restoreTestKey)
	if err != nil {
		t.Fatal(err)
	}
	key, err := backup.SigningKey(restoreTestKey)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := backup.OpenDir(t.TempDir(), fp)
	if err != nil {
		t.Fatal(err)
	}
	info, err := (&backup.Dumper{Pool: pool, Dir: dir, App: "0.4.0", KeyFingerprint: fp, SigningKey: key}).Dump(context.Background(), backup.KindManual, "local:alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir.Root(), info.Name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// uploadForSetup uploads data on the setup screen and returns the id of
// the completed upload.
func uploadForSetup(t *testing.T, c *client, token string, data []byte) string {
	t.Helper()
	code, body := c.do("POST", "/api/v1/setup/restore/uploads", map[string]any{"token": token, "file_name": "old.tar.gz", "size": len(data)})
	if code != http.StatusCreated {
		t.Fatalf("start upload = %d %v", code, body)
	}
	id, _ := body["id"].(string)
	req, err := http.NewRequestWithContext(t.Context(), "PUT", fmt.Sprintf("%s/api/v1/setup/restore/uploads/%s?offset=0", c.base, id), bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Setup-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload = %d", resp.StatusCode)
	}
	if code, body := c.do("POST", "/api/v1/setup/restore/uploads/"+id+"/complete", map[string]any{"token": token}); code != http.StatusCreated {
		t.Fatalf("complete = %d %v", code, body)
	}
	return id
}

func countRows(t *testing.T, pool *pgxpool.Pool, q string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A server stopped in the middle of a restore leaves the data as it was,
// and the next start serves normally and can restore.
func TestShutdownDuringRestore(t *testing.T) {
	ctx := context.Background()
	src := storetest.New(t)
	if _, err := src.Exec(ctx, `INSERT INTO users (source, username, role, password_hash) VALUES ('local', 'alice', 'admin', 'x');
INSERT INTO services (name, gitlab_project_path, created_via) VALUES ('payments-api', 'example/payments-api', 'manual')`); err != nil {
		t.Fatal(err)
	}
	data := backupOf(t, src)

	pool := storetest.New(t)
	if _, err := pool.Exec(ctx, `INSERT INTO services (name, gitlab_project_path, created_via) VALUES ('web-shop', 'example/web-shop', 'manual')`); err != nil {
		t.Fatal(err)
	}
	folder := t.TempDir()
	base, logs, stop := runServer(t, dbURL(t, pool), folder)
	c := &client{t: t, base: base}
	token := setupToken(t, logs.String())
	id := uploadForSetup(t, c, token, data)

	// A reader of the services table holds the restore at its first
	// change, inside its transaction.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `LOCK TABLE services IN ACCESS SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	if code, body := c.do("POST", "/api/v1/setup/restore", map[string]any{"token": token, "upload_id": id, "confirm": "RESTORE"}); code != http.StatusAccepted {
		t.Fatalf("restore = %d %v", code, body)
	}
	// Restore mode begins once the restore has its locks; the API then
	// turns requests away, while the status and the probes still answer.
	deadline := time.Now().Add(20 * time.Second)
	for {
		code, body := c.do("GET", "/api/v1/setup", nil)
		if code == http.StatusServiceUnavailable && errCode(body) == "restoring" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("restore mode did not begin: %d %v", code, body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code, st := c.do("GET", "/api/v1/restore/status", nil); code != http.StatusOK || st["state"] != "restoring" {
		t.Errorf("restore status = %d %v", code, st)
	}
	if code, _ := c.do("GET", "/readyz", nil); code != http.StatusOK {
		t.Errorf("readyz during the restore = %d", code)
	}

	if err := stop(); err != nil {
		t.Fatalf("serveApp returned %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM users`); n != 0 {
		t.Errorf("%d users after the stopped restore, want none", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM services WHERE name = 'web-shop'`); n != 1 {
		t.Errorf("the data before the restore is gone")
	}

	base, logs, _ = runServer(t, dbURL(t, pool), folder)
	c = &client{t: t, base: base}
	if code, st := c.do("GET", "/api/v1/restore/status", nil); code != http.StatusOK || st["state"] != "idle" {
		t.Errorf("restore status after the restart = %d %v", code, st)
	}
	if code, body := c.do("GET", "/api/v1/setup", nil); code != http.StatusOK || body["required"] != true {
		t.Fatalf("setup status after the restart = %d %v", code, body)
	}
	token = setupToken(t, logs.String())
	id = uploadForSetup(t, c, token, data)
	if code, body := c.do("POST", "/api/v1/setup/restore", map[string]any{"token": token, "upload_id": id, "confirm": "RESTORE"}); code != http.StatusAccepted {
		t.Fatalf("restore after the restart = %d %v", code, body)
	}
	deadline = time.Now().Add(30 * time.Second)
	for {
		_, st := c.do("GET", "/api/v1/restore/status", nil)
		if st["state"] == "done" {
			break
		}
		if st["state"] == "failed" || time.Now().After(deadline) {
			t.Fatalf("restore after the restart: %v\n%s", st, logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM users WHERE username = 'alice'`); n != 1 {
		t.Errorf("%d users alice after the restore", n)
	}
	if code, body := c.do("GET", "/api/v1/setup", nil); code != http.StatusOK || body["required"] != false {
		t.Errorf("setup status after the restore = %d %v", code, body)
	}
}
