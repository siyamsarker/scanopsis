// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package store_test

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

// TestMigration00009SetsAutovacuum checks the storage parameters of the two
// large tables after migrating, and that rolling the migration back removes
// them and migrating again sets them again.
func TestMigration00009SetsAutovacuum(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	ctx := context.Background()
	options := func(table string) []string {
		t.Helper()
		var opts []string
		if err := pool.QueryRow(ctx, `SELECT coalesce(reloptions, '{}') FROM pg_class WHERE oid = $1::regclass`, table).Scan(&opts); err != nil {
			t.Fatal(err)
		}
		slices.Sort(opts)
		return opts
	}
	want := []string{
		"autovacuum_analyze_scale_factor=0.02",
		"autovacuum_vacuum_insert_scale_factor=0.05",
		"autovacuum_vacuum_scale_factor=0.05",
	}
	for _, table := range []string{"scans", "vulnerabilities"} {
		if got := options(table); !slices.Equal(got, want) {
			t.Errorf("%s options = %v, want %v", table, got, want)
		}
	}

	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	p, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 8); err != nil {
		t.Fatalf("down to 8: %v", err)
	}
	for _, table := range []string{"scans", "vulnerabilities"} {
		if got := options(table); len(got) != 0 {
			t.Errorf("%s options after the rollback = %v, want none", table, got)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if got := options("scans"); !slices.Equal(got, want) {
		t.Errorf("scans options after migrating again = %v", got)
	}
}
