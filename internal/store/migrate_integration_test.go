// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package store_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

func TestOpenAppliesPoolDefaults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := storetest.New(t).Config().ConnString()
	pool, err := store.Open(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if got := pool.Config().MaxConns; got != store.DefaultMaxConns {
		t.Errorf("MaxConns = %d, want %d", got, store.DefaultMaxConns)
	}
	var timeout string
	if err := pool.QueryRow(ctx, `SHOW statement_timeout`).Scan(&timeout); err != nil || timeout != "30s" {
		t.Errorf("statement_timeout = %q (%v), want 30s", timeout, err)
	}
	// A statement over the limit is cancelled by the server.
	if _, err := pool.Exec(ctx, `SET statement_timeout = '50ms'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT pg_sleep(5)`); err == nil || !strings.Contains(err.Error(), "statement timeout") {
		t.Errorf("a slow statement was not cancelled: %v", err)
	}

	custom, err := store.Open(ctx, base+"&pool_max_conns=3&statement_timeout=90000")
	if err != nil {
		t.Fatal(err)
	}
	defer custom.Close()
	if got := custom.Config().MaxConns; got != 3 {
		t.Errorf("MaxConns with pool_max_conns=3 = %d", got)
	}
	if err := custom.QueryRow(ctx, `SHOW statement_timeout`).Scan(&timeout); err != nil || timeout != "90s" {
		t.Errorf("statement_timeout with the URL parameter = %q (%v), want 90s", timeout, err)
	}
}

// emptyDatabase creates a database without tables and returns a pool on it.
func emptyDatabase(t *testing.T, name string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	admin := storetest.New(t)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg := admin.Config().Copy()
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func provider(t *testing.T, pool *pgxpool.Pool) *goose.Provider {
	t.Helper()
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	p, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func indexes(t *testing.T, pool *pgxpool.Pool) map[string]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT c.relname, i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
		JOIN pg_class t ON t.oid = i.indrelid WHERE t.relname IN ('scans', 'vulnerabilities')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		var valid bool
		if err := rows.Scan(&name, &valid); err != nil {
			t.Fatal(err)
		}
		out[name] = valid
	}
	return out
}

// TestMigrateQueryIndexes upgrades a populated database from the schema
// before the query indexes: the new indexes are built although every session
// of the database has a 1 ms statement timeout, the replaced ones are gone,
// each migration is logged with its duration, and an index left invalid by an
// interrupted build is rebuilt by the next start.
func TestMigrateQueryIndexes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := emptyDatabase(t, "migrate_indexes")
	if _, err := provider(t, pool).UpTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO services (name, gitlab_project_path, created_via) SELECT 's' || g, 'g/s' || g, 'manual' FROM generate_series(1, 20) g`,
		`INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected,
		                    commit_sha, schema_version, scanned_at, raw_report)
		 SELECT s.id, 'image', 'app', j, j, 'main', false, 'abc', 2, now() - j * interval '1 hour', '\x1f8b'
		 FROM services s, generate_series(1, 50) j`,
		`INSERT INTO vulnerabilities (scan_id, target, class, pkg_name, vuln_id, severity)
		 SELECT s.id, 't', 'os-pkgs', 'p' || (g % 5), 'CVE-2024-' || g, 'HIGH' FROM scans s, generate_series(1, 30) g`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	before := indexes(t, pool)
	if _, ok := before["scans_current_idx"]; !ok {
		t.Fatalf("schema before the migration has no scans_current_idx: %v", before)
	}

	// Every new session of the database is now limited to 1 ms.
	if _, err := pool.Exec(ctx, `ALTER DATABASE migrate_indexes SET statement_timeout = '1ms'`); err != nil {
		t.Fatal(err)
	}
	cfg := pool.Config().Copy()
	strict, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer strict.Close()
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	if err := store.MigrateLogged(ctx, strict, log); err != nil {
		t.Fatalf("migrate under a 1 ms statement timeout: %v", err)
	}
	for _, want := range []string{
		"applying migration", "applied migration", "migration=00007_query_indexes.sql", "duration=",
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("migration log lacks %q:\n%s", want, logs.String())
		}
	}

	after := indexes(t, pool)
	for _, name := range []string{"vulnerabilities_scan_key_idx", "scans_current_cover_idx", "scans_latest_ref_idx", "vulnerabilities_vuln_id_idx"} {
		if valid, ok := after[name]; !ok || !valid {
			t.Errorf("index %s: present %v, valid %v", name, ok, valid)
		}
	}
	for _, name := range []string{"vulnerabilities_scan_idx", "scans_current_idx"} {
		if _, ok := after[name]; ok {
			t.Errorf("index %s still exists", name)
		}
	}

	// Simulate a build that was interrupted: the index exists but is invalid
	// and the migration is not recorded. The next start must rebuild it.
	for _, q := range []string{
		`UPDATE pg_index SET indisvalid = false WHERE indexrelid = 'scans_current_cover_idx'::regclass`,
		`DELETE FROM goose_db_version WHERE version_id = 7`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MigrateLogged(ctx, strict, log); err != nil {
		t.Fatalf("migrate after an interrupted build: %v", err)
	}
	if valid, ok := indexes(t, pool)["scans_current_cover_idx"]; !ok || !valid {
		t.Errorf("interrupted index was not rebuilt: present %v, valid %v", ok, valid)
	}
}
