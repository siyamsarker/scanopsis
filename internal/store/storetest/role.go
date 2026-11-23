// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package storetest

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NarrowRole creates a database and a login role, both called name: the
// database belongs to the administrator of the server, and the role only
// gets CREATE on its public schema, as "Use an existing PostgreSQL" in
// docs/deployment.md allows. Nothing is migrated. It returns pools on the
// database as the administrator and as the role; both, the database and the
// role are removed when the test ends.
func NarrowRole(t testing.TB, name string) (owner, role *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	admin := New(t)
	for _, q := range []string{
		`CREATE ROLE ` + name + ` LOGIN PASSWORD 'role-secret'`,
		`CREATE DATABASE ` + name,
	} {
		if _, err := admin.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, q := range []string{`DROP DATABASE ` + name + ` WITH (FORCE)`, `DROP ROLE ` + name} {
			if _, err := admin.Exec(context.Background(), q); err != nil {
				t.Errorf("clean up: %v", err)
			}
		}
	})
	cfg := admin.Config().Copy()
	cfg.ConnConfig.Database = name
	owner, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	if _, err := owner.Exec(ctx, `GRANT CREATE ON SCHEMA public TO `+name); err != nil {
		t.Fatal(err)
	}
	cfg = cfg.Copy()
	cfg.ConnConfig.User = name
	cfg.ConnConfig.Password = "role-secret"
	role, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(role.Close)
	return owner, role
}
