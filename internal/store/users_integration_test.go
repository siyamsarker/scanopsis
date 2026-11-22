// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

func count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestDisableEnableDeleteUser(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	ctx := context.Background()
	q := store.New(pool)
	admin := mustLocal(t, q, "root", "admin")
	v := upsert(t, q, 77, "vic")
	far := time.Now().Add(time.Hour)
	if err := q.CreateSession(ctx, store.CreateSessionParams{IDHash: hash(1), UserID: v.ID, ExpiresAt: ts(far)}); err != nil {
		t.Fatal(err)
	}
	if err := q.InsertRecoveryCode(ctx, store.InsertRecoveryCodeParams{UserID: v.ID, CodeHash: hash(2), Version: 2}); err != nil {
		t.Fatal(err)
	}
	if err := q.InsertAudit(ctx, store.InsertAuditParams{Actor: "gitlab:vic", Action: "login.succeeded", Detail: []byte("{}")}); err != nil {
		t.Fatal(err)
	}

	// Disable: the session ends at once and no new one is valid.
	d, err := store.SetUserDisabled(ctx, pool, v.ID, true)
	if err != nil || !d.DisabledAt.Valid {
		t.Fatalf("disable: %+v, %v", d, err)
	}
	if n := count(t, pool, "SELECT count(*) FROM sessions WHERE user_id = $1", v.ID); n != 0 {
		t.Fatalf("%d sessions left after disabling", n)
	}
	if err := q.CreateSession(ctx, store.CreateSessionParams{IDHash: hash(3), UserID: v.ID, ExpiresAt: ts(far)}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetSession(ctx, store.GetSessionParams{IDHash: hash(3), Now: ts(time.Now()), IdleCutoff: ts(time.Now().Add(-time.Hour))}); err == nil {
		t.Fatal("a disabled user's session is valid")
	}
	// Disabling again keeps the first time.
	again, err := store.SetUserDisabled(ctx, pool, v.ID, true)
	if err != nil || !again.DisabledAt.Time.Equal(d.DisabledAt.Time) {
		t.Fatalf("disable twice: %v -> %v, %v", d.DisabledAt.Time, again.DisabledAt.Time, err)
	}
	// A GitLab sign-in does not enable the user or record a sign-in time.
	before := again.LastLoginAt
	after := upsert(t, q, 77, "vic")
	if !after.DisabledAt.Valid || after.LastLoginAt != before {
		t.Fatalf("sign-in of a disabled user changed it: %+v", after)
	}

	// Enable: sessions work again.
	e, err := store.SetUserDisabled(ctx, pool, v.ID, false)
	if err != nil || e.DisabledAt.Valid {
		t.Fatalf("enable: %+v, %v", e, err)
	}
	if err := q.CreateSession(ctx, store.CreateSessionParams{IDHash: hash(4), UserID: v.ID, ExpiresAt: ts(far)}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetSession(ctx, store.GetSessionParams{IDHash: hash(4), Now: ts(time.Now()), IdleCutoff: ts(time.Now().Add(-time.Hour))}); err != nil {
		t.Fatalf("session after enabling: %v", err)
	}

	// Delete: sessions and recovery codes go, the audit log stays.
	del, err := store.DeleteUser(ctx, pool, v.ID)
	if err != nil || del.ID != v.ID || del.Username != "vic" {
		t.Fatalf("delete: %+v, %v", del, err)
	}
	for query, want := range map[string]int{
		"SELECT count(*) FROM users WHERE id = $1":                                     0,
		"SELECT count(*) FROM sessions WHERE user_id = $1":                             0,
		"SELECT count(*) FROM recovery_codes WHERE user_id = $1":                       0,
		"SELECT count(*) FROM audit_log WHERE actor = 'gitlab:vic' AND $1::bigint > 0": 1,
	} {
		if n := count(t, pool, query, v.ID); n != want {
			t.Errorf("%s = %d, want %d", query, n, want)
		}
	}
	if _, err := store.DeleteUser(ctx, pool, v.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("delete again: %v", err)
	}
	if _, err := store.SetUserDisabled(ctx, pool, v.ID, true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("disable unknown: %v", err)
	}
	if n := count(t, pool, "SELECT count(*) FROM users WHERE id = $1 AND disabled_at IS NULL", admin.ID); n != 1 {
		t.Fatal("the administrator was touched")
	}
}

func TestLastEnabledAdministratorIsKept(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	ctx := context.Background()
	q := store.New(pool)
	a := mustLocal(t, q, "a", "admin")
	b := mustLocal(t, q, "b", "admin")

	if _, err := store.SetUserDisabled(ctx, pool, a.ID, true); err != nil {
		t.Fatal(err)
	}
	// b is now the only enabled administrator.
	if _, err := store.SetUserDisabled(ctx, pool, b.ID, true); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("disable the last enabled admin: %v", err)
	}
	if _, err := store.SetUserRole(ctx, pool, b.ID, store.RoleViewer); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("demote the last enabled admin: %v", err)
	}
	if _, err := store.DeleteUser(ctx, pool, b.ID); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("delete the last enabled admin: %v", err)
	}
	// A disabled administrator can be demoted or deleted.
	if _, err := store.SetUserRole(ctx, pool, a.ID, store.RoleViewer); err != nil {
		t.Fatalf("demote a disabled admin: %v", err)
	}
	if _, err := store.SetUserRole(ctx, pool, a.ID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetUserDisabled(ctx, pool, a.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteUser(ctx, pool, b.ID); err != nil {
		t.Fatalf("delete with another enabled admin: %v", err)
	}
}

func TestConcurrentDisablingKeepsOneAdministrator(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	ctx := context.Background()
	q := store.New(pool)
	a := mustLocal(t, q, "ca", "admin")
	b := mustLocal(t, q, "cb", "admin")

	for round := range 20 {
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make([]error, 2)
		for i, id := range []int64{a.ID, b.ID} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if round%2 == 0 {
					_, errs[i] = store.SetUserDisabled(ctx, pool, id, true)
				} else {
					// One disables, the other is demoted.
					if i == 0 {
						_, errs[i] = store.SetUserDisabled(ctx, pool, id, true)
					} else {
						_, errs[i] = store.SetUserRole(ctx, pool, id, store.RoleViewer)
					}
				}
			}()
		}
		close(start)
		wg.Wait()
		ok := 0
		for _, err := range errs {
			if err == nil {
				ok++
			} else if !errors.Is(err, store.ErrLastAdmin) {
				t.Fatalf("round %d: %v", round, err)
			}
		}
		if ok != 1 {
			t.Fatalf("round %d: %d changes succeeded (%v), want 1", round, ok, errs)
		}
		if n := count(t, pool, "SELECT count(*) FROM users WHERE role = 'admin' AND disabled_at IS NULL"); n != 1 {
			t.Fatalf("round %d: %d enabled admins, want 1", round, n)
		}
		for _, id := range []int64{a.ID, b.ID} {
			if _, err := store.SetUserRole(ctx, pool, id, store.RoleAdmin); err != nil {
				t.Fatal(err)
			}
			if _, err := store.SetUserDisabled(ctx, pool, id, false); err != nil {
				t.Fatal(err)
			}
		}
	}
}
