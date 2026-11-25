// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

// Package storetest gives integration tests a migrated PostgreSQL database.
// One container is started per test binary; every call to New creates a
// fresh, empty database in it, so tests are isolated and can run in parallel.
package storetest

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/siyamsarker/scanopsis/internal/store"
)

// Image is the PostgreSQL image used by integration tests; keep it in step
// with the version documented for production.
const Image = "postgres:18.7-alpine@sha256:0115267ab5a509022df9319c77be365cef335adb71556c75f60f5360ee247e71"

var (
	once    sync.Once
	baseURL string
	admin   *pgxpool.Pool
	initErr error
	counter atomic.Int64
)

// New returns a pool connected to a new, migrated database. The pool is
// closed when the test ends.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	once.Do(func() {
		// The container lives until the test binary exits; testcontainers'
		// reaper removes it afterwards.
		c, err := postgres.Run(ctx, Image,
			postgres.WithDatabase("postgres"),
			postgres.WithUsername("scanopsis"),
			postgres.WithPassword("scanopsis"),
			postgres.BasicWaitStrategies(),
		)
		if err != nil {
			initErr = fmt.Errorf("start PostgreSQL container: %w", err)
			return
		}
		baseURL, initErr = c.ConnectionString(ctx, "sslmode=disable")
		if initErr == nil {
			admin, initErr = store.Open(ctx, baseURL)
		}
	})
	if initErr != nil {
		t.Fatalf("storetest: %v", initErr)
	}

	name := fmt.Sprintf("test_%d", counter.Add(1))
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("storetest: create database: %v", err)
	}
	cfg, err := pgxpool.ParseConfig(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	cfg.ConnConfig.RuntimeParams["jit"] = "off" // as store.Open sets it
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatalf("storetest: migrate: %v", err)
	}
	return pool
}
