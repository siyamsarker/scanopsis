// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package store_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

// TestMigration00008KeepsExistingData rolls a database back to before
// migration 00008, stores data the way version 0.1.0 did, and migrates up
// again: existing sessions get numbers, existing recovery codes become
// version 1, GitLab users get the configured GitLab URL as issuer, and
// nobody is disabled. It then checks that the migration can be rolled back.
func TestMigration00008KeepsExistingData(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	ctx := context.Background()
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	p, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 6); err != nil {
		t.Fatalf("down to 6: %v", err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`INSERT INTO settings (key, value, source, updated_by) VALUES ('gitlab.url', '"https://gitlab.example.com"', 'env', 'system')`)
	exec(`INSERT INTO users (source, gitlab_user_id, username, role) VALUES ('gitlab', 42, 'alice', 'admin')`)
	exec(`INSERT INTO users (source, username, role, password_hash) VALUES ('local', 'root', 'admin', 'x')`)
	exec(`INSERT INTO sessions (id_hash, user_id, expires_at) SELECT decode(repeat('ab', 32), 'hex'), id, now() + interval '1 hour' FROM users WHERE username = 'alice'`)
	exec(`INSERT INTO sessions (id_hash, user_id, expires_at) SELECT decode(repeat('cd', 32), 'hex'), id, now() + interval '1 hour' FROM users WHERE username = 'root'`)
	exec(`INSERT INTO recovery_codes (user_id, code_hash) SELECT id, decode(repeat('ef', 32), 'hex') FROM users WHERE username = 'root'`)

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	for query, want := range map[string]int{
		`SELECT count(*) FROM sessions WHERE id IS NOT NULL AND client_ip = '' AND user_agent = ''`:            2,
		`SELECT count(DISTINCT id) FROM sessions`:                                                              2,
		`SELECT count(*) FROM recovery_codes WHERE version = 1`:                                                1,
		`SELECT count(*) FROM users WHERE disabled_at IS NULL`:                                                 2,
		`SELECT count(*) FROM users WHERE username = 'alice' AND gitlab_issuer = 'https://gitlab.example.com'`: 1,
		`SELECT count(*) FROM users WHERE username = 'root' AND gitlab_issuer IS NULL`:                         1,
	} {
		var n int
		if err := pool.QueryRow(ctx, query).Scan(&n); err != nil || n != want {
			t.Errorf("%s = %d (%v), want %d", query, n, err, want)
		}
	}
	// New rows get the next session number and must name a code version.
	exec(`INSERT INTO sessions (id_hash, user_id, expires_at) SELECT decode(repeat('12', 32), 'hex'), id, now() FROM users WHERE username = 'root'`)
	if _, err := pool.Exec(ctx, `INSERT INTO recovery_codes (user_id, code_hash) SELECT id, decode(repeat('34', 32), 'hex') FROM users WHERE username = 'root'`); err == nil {
		t.Error("a recovery code without a version was accepted")
	}

	if _, err := p.DownTo(ctx, 6); err != nil {
		t.Fatalf("down again: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

// TestMigration00008WithoutGitLabURL gives existing GitLab users an empty
// issuer when no GitLab URL is stored, so that no sign-in matches them.
func TestMigration00008WithoutGitLabURL(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	ctx := context.Background()
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	p, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (source, gitlab_user_id, username, role) VALUES ('gitlab', 42, 'alice', 'viewer')`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var issuer string
	if err := pool.QueryRow(ctx, `SELECT gitlab_issuer FROM users WHERE username = 'alice'`).Scan(&issuer); err != nil || issuer != "" {
		t.Fatalf("issuer %q, %v", issuer, err)
	}
}
