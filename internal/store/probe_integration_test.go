// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

// The probe has a connection of its own: it answers while every pooled
// connection is taken, which is when requests wait for the pool.
func TestProbeAnswersWhilePoolIsSaturated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := storetest.New(t).Config().ConnString()
	pool, err := store.Open(ctx, base+"&pool_max_conns=2")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	probe := store.NewProbe(pool)
	defer probe.Close(ctx)

	var held []interface{ Release() }
	for range 2 {
		c, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, c)
	}
	defer func() {
		for _, c := range held {
			c.Release()
		}
	}()

	// The pool is exhausted: a pooled ping cannot get a connection.
	short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if err := pool.Ping(short); err == nil {
		t.Fatal("pool.Ping succeeded although every connection is taken")
	}
	for range 3 {
		start := time.Now()
		if err := probe.Ping(ctx); err != nil {
			t.Fatalf("probe.Ping with a saturated pool: %v", err)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("probe.Ping took %v", d)
		}
	}
}

// After the server closes its connection, one check fails and the next
// connects again.
func TestProbeReconnects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := storetest.New(t)
	probe := store.NewProbe(pool)
	defer probe.Close(ctx)

	if err := probe.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	// Terminate every other connection of this database, the probe's included.
	if _, err := pool.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname = current_database() AND pid <> pg_backend_pid()`); err != nil {
		t.Fatal(err)
	}
	var failed bool
	for range 5 {
		if err := probe.Ping(ctx); err != nil {
			failed = true
			continue
		}
		if !failed {
			t.Skip("the probe's connection was not terminated") // cannot happen unless the terminate raced
		}
		return
	}
	t.Fatal("probe did not recover after its connection was terminated")
}

// A check that cannot get its turn or its answer before ctx ends returns an
// error rather than blocking.
func TestProbeHonoursContext(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	probe := store.NewProbe(pool)
	defer probe.Close(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := probe.Ping(ctx); err == nil {
		t.Fatal("Ping with a cancelled context succeeded")
	}
	if err := probe.Ping(context.Background()); err != nil {
		t.Fatalf("Ping after a cancelled one: %v", err)
	}
}
