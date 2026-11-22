// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

var baseTime = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }

func hash(b byte) []byte {
	h := make([]byte, 32)
	for i := range h {
		h[i] = b
	}
	return h
}

func mustLocal(t *testing.T, q *store.Queries, name, role string) store.User {
	t.Helper()
	u, err := q.CreateLocalUser(context.Background(), store.CreateLocalUserParams{
		Username: name, Role: role, PasswordHash: text("$argon2id$x"),
	})
	if err != nil {
		t.Fatalf("CreateLocalUser(%q): %v", name, err)
	}
	return u
}

func upsert(t *testing.T, q *store.Queries, id int64, username string) store.User {
	t.Helper()
	u, err := q.UpsertGitLabUser(context.Background(), store.UpsertGitLabUserParams{
		GitlabIssuer: text(testIssuer),
		GitlabUserID: pgtype.Int8{Int64: id, Valid: true},
		Username:     username,
		Name:         "Name " + username,
		Email:        username + "@example.com",
	})
	if err != nil {
		t.Fatalf("UpsertGitLabUser: %v", err)
	}
	return u
}

// testIssuer is the GitLab instance of the users that upsert creates.
const testIssuer = "https://gitlab.example.com"

func assertPgCode(t *testing.T, err error, code string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("want PgError %s, got %v", code, err)
	}
	if pgErr.Code != code {
		t.Fatalf("want SQLSTATE %s, got %s (%s)", code, pgErr.Code, pgErr.Message)
	}
}

const (
	pgCheck  = "23514"
	pgUnique = "23505"
	pgFK     = "23503"
)

func TestMigrateIdempotent(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	var versions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM goose_db_version WHERE version_id > 0`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob("migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if versions != len(files) || len(files) < 2 {
		t.Errorf("applied migrations = %d, want one per embedded file (%d)", versions, len(files))
	}
	for _, tbl := range []string{"users", "sessions", "audit_log", "recovery_codes", "settings"} {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, tbl).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Errorf("table %s missing", tbl)
		}
	}
}

func TestSchemaConstraints(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	ctx := context.Background()
	q := store.New(pool)

	t.Run("local without password", func(t *testing.T) {
		_, err := pool.Exec(ctx, `INSERT INTO users (source, username, role) VALUES ('local','a','viewer')`)
		assertPgCode(t, err, pgCheck)
	})
	t.Run("gitlab without gitlab id", func(t *testing.T) {
		_, err := pool.Exec(ctx, `INSERT INTO users (source, username, role) VALUES ('gitlab','a','viewer')`)
		assertPgCode(t, err, pgCheck)
	})
	t.Run("invalid role", func(t *testing.T) {
		_, err := q.CreateLocalUser(ctx, store.CreateLocalUserParams{Username: "r", Role: "root", PasswordHash: text("h")})
		assertPgCode(t, err, pgCheck)
	})
	t.Run("invalid source", func(t *testing.T) {
		_, err := pool.Exec(ctx, `INSERT INTO users (source, username, role, password_hash) VALUES ('ldap','a','viewer','h')`)
		assertPgCode(t, err, pgCheck)
	})
	t.Run("empty username", func(t *testing.T) {
		_, err := q.CreateLocalUser(ctx, store.CreateLocalUserParams{Username: "", Role: "viewer", PasswordHash: text("h")})
		assertPgCode(t, err, pgCheck)
	})
	t.Run("duplicate local username", func(t *testing.T) {
		mustLocal(t, q, "dup", "viewer")
		_, err := q.CreateLocalUser(ctx, store.CreateLocalUserParams{Username: "dup", Role: "admin", PasswordHash: text("h")})
		assertPgCode(t, err, pgUnique)
	})
	t.Run("gitlab shares local username", func(t *testing.T) {
		mustLocal(t, q, "shared", "viewer")
		g := upsert(t, q, 500, "shared")
		if g.Source != "gitlab" || g.Username != "shared" {
			t.Errorf("unexpected user %+v", g)
		}
		l, err := q.GetLocalUserByUsername(ctx, "shared")
		if err != nil {
			t.Fatal(err)
		}
		if l.Source != "local" || l.ID == g.ID {
			t.Errorf("GetLocalUserByUsername returned %+v", l)
		}
	})
	t.Run("duplicate gitlab id", func(t *testing.T) {
		_, err := pool.Exec(ctx, `INSERT INTO users (source, gitlab_issuer, gitlab_user_id, username, role) VALUES ('gitlab', 'https://a', 777, 'x', 'viewer'), ('gitlab', 'https://a', 777, 'y', 'viewer')`)
		assertPgCode(t, err, pgUnique)
	})
	t.Run("same gitlab id on another instance", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `INSERT INTO users (source, gitlab_issuer, gitlab_user_id, username, role) VALUES ('gitlab', 'https://a', 778, 'x', 'viewer'), ('gitlab', 'https://b', 778, 'x', 'viewer')`); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("gitlab user without issuer", func(t *testing.T) {
		_, err := pool.Exec(ctx, `INSERT INTO users (source, gitlab_user_id, username, role) VALUES ('gitlab', 779, 'x', 'viewer')`)
		assertPgCode(t, err, pgCheck)
	})
	t.Run("local user with issuer", func(t *testing.T) {
		_, err := pool.Exec(ctx, `INSERT INTO users (source, gitlab_issuer, username, role, password_hash) VALUES ('local', 'https://a', 'issuer-local', 'viewer', 'x')`)
		assertPgCode(t, err, pgCheck)
	})
	t.Run("session hash length", func(t *testing.T) {
		u := mustLocal(t, q, "sh", "viewer")
		for _, n := range []int{0, 31, 33} {
			err := q.CreateSession(ctx, store.CreateSessionParams{IDHash: make([]byte, n), UserID: u.ID, ExpiresAt: ts(baseTime)})
			assertPgCode(t, err, pgCheck)
		}
		if err := q.CreateSession(ctx, store.CreateSessionParams{IDHash: hash(1), UserID: u.ID, ExpiresAt: ts(baseTime)}); err != nil {
			t.Fatalf("32-byte hash rejected: %v", err)
		}
	})
	t.Run("session unknown user", func(t *testing.T) {
		err := q.CreateSession(ctx, store.CreateSessionParams{IDHash: hash(9), UserID: 999999, ExpiresAt: ts(baseTime)})
		assertPgCode(t, err, pgFK)
	})
	t.Run("cascade delete", func(t *testing.T) {
		u := mustLocal(t, q, "casc", "viewer")
		other := mustLocal(t, q, "casc2", "viewer")
		for i, uid := range []int64{u.ID, u.ID, other.ID} {
			if err := q.CreateSession(ctx, store.CreateSessionParams{IDHash: hash(byte(100 + i)), UserID: uid, ExpiresAt: ts(baseTime)}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1`, u.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("sessions for deleted user = %d, want 0", n)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1`, other.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("sessions for other user = %d, want 1", n)
		}
	})
}

func TestLocalUserRoundTrip(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	ctx := context.Background()
	q := store.New(pool)

	created, err := q.CreateLocalUser(ctx, store.CreateLocalUserParams{Username: "alice", Role: store.RoleAdmin, PasswordHash: text("$argon2id$hash")})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.Source != "local" || created.Username != "alice" || created.Role != "admin" ||
		created.PasswordHash != text("$argon2id$hash") || created.GitlabUserID.Valid ||
		created.Name != "" || created.Email != "" || !created.CreatedAt.Valid || created.LastLoginAt.Valid {
		t.Errorf("unexpected created user: %+v", created)
	}
	byName, err := q.GetLocalUserByUsername(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	byID, err := q.GetUser(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []store.User{byName, byID} {
		if got.ID != created.ID || got.Username != created.Username || got.Role != created.Role ||
			got.Source != created.Source || got.PasswordHash != created.PasswordHash ||
			!got.CreatedAt.Time.Equal(created.CreatedAt.Time) {
			t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, created)
		}
	}
	if _, err := q.GetUser(ctx, created.ID+100); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("GetUser unknown: %v", err)
	}
	if _, err := q.GetLocalUserByUsername(ctx, "nobody"); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("GetLocalUserByUsername unknown: %v", err)
	}
	// A GitLab user is not returned as a local user.
	upsert(t, q, 1, "gl-only")
	if _, err := q.GetLocalUserByUsername(ctx, "gl-only"); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("gitlab user returned as local: %v", err)
	}
}

func TestCountAdmins(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	ctx := context.Background()
	q := store.New(pool)

	count := func(want int64) {
		t.Helper()
		n, err := q.CountAdmins(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Fatalf("CountAdmins = %d, want %d", n, want)
		}
	}
	count(0)
	mustLocal(t, q, "v", "viewer")
	count(0)
	mustLocal(t, q, "a1", "admin")
	count(1)
	g := upsert(t, q, 1, "g")
	count(1)
	if _, err := q.SetUserRole(context.Background(), store.SetUserRoleParams{ID: g.ID, Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	count(2)
}

func TestUpsertGitLabUser(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	ctx := context.Background()
	q := store.New(pool)

	t.Run("new viewer", func(t *testing.T) {
		before := time.Now().Add(-time.Minute)
		u := upsert(t, q, 10, "newbie")
		if u.Role != "viewer" || u.Source != "gitlab" || u.GitlabUserID != (pgtype.Int8{Int64: 10, Valid: true}) ||
			u.Username != "newbie" || u.Name != "Name newbie" || u.Email != "newbie@example.com" || u.PasswordHash.Valid {
			t.Errorf("unexpected user %+v", u)
		}
		if !u.LastLoginAt.Valid || u.LastLoginAt.Time.Before(before) {
			t.Errorf("last_login_at not set: %+v", u.LastLoginAt)
		}
	})
	t.Run("existing viewer updated", func(t *testing.T) {
		first := upsert(t, q, 12, "old")
		// Push last_login_at into the past so the update is observable.
		if _, err := pool.Exec(ctx, `UPDATE users SET last_login_at = $2 WHERE id = $1`, first.ID, ts(baseTime)); err != nil {
			t.Fatal(err)
		}
		got, err := q.UpsertGitLabUser(ctx, store.UpsertGitLabUserParams{
			GitlabIssuer: text(testIssuer),
			GitlabUserID: pgtype.Int8{Int64: 12, Valid: true}, Username: "renamed", Name: "New Name", Email: "new@example.com",
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.ID != first.ID || got.Role != "viewer" || got.Username != "renamed" || got.Name != "New Name" || got.Email != "new@example.com" {
			t.Errorf("unexpected user %+v", got)
		}
		if !got.LastLoginAt.Time.After(baseTime) {
			t.Errorf("last_login_at not advanced: %v", got.LastLoginAt.Time)
		}
		if !got.CreatedAt.Time.Equal(first.CreatedAt.Time) {
			t.Errorf("created_at changed")
		}
	})
	t.Run("existing admin stays admin", func(t *testing.T) {
		u := upsert(t, q, 13, "adm")
		if _, err := q.SetUserRole(ctx, store.SetUserRoleParams{ID: u.ID, Role: "admin"}); err != nil {
			t.Fatal(err)
		}
		if got := upsert(t, q, 13, "adm"); got.Role != "admin" {
			t.Errorf("role = %s, want admin", got.Role)
		}
	})
	t.Run("another instance is another user", func(t *testing.T) {
		admin := upsert(t, q, 16, "same")
		if _, err := q.SetUserRole(ctx, store.SetUserRoleParams{ID: admin.ID, Role: "admin"}); err != nil {
			t.Fatal(err)
		}
		other, err := q.UpsertGitLabUser(ctx, store.UpsertGitLabUserParams{
			GitlabIssuer: text("https://gitlab.other.example"),
			GitlabUserID: pgtype.Int8{Int64: 16, Valid: true}, Username: "same", Name: "Impostor", Email: "x@example.com",
		})
		if err != nil {
			t.Fatal(err)
		}
		if other.ID == admin.ID || other.Role != "viewer" || other.GitlabIssuer.String != "https://gitlab.other.example" {
			t.Errorf("a user of another GitLab instance matched the existing row: %+v", other)
		}
		if again := upsert(t, q, 16, "same"); again.ID != admin.ID || again.Role != "admin" {
			t.Errorf("the original user changed: %+v", again)
		}
		byName, err := q.ListGitLabUsersByUsername(ctx, "same")
		if err != nil || len(byName) != 2 {
			t.Errorf("by name: %d users, %v", len(byName), err)
		}
		byID, err := q.ListGitLabUsersByGitLabID(ctx, pgtype.Int8{Int64: 16, Valid: true})
		if err != nil || len(byID) != 2 || byID[0].ID != admin.ID {
			t.Errorf("by GitLab id: %+v, %v", byID, err)
		}
	})
	t.Run("demoted admin stays viewer", func(t *testing.T) {
		u := upsert(t, q, 15, "dem")
		if _, err := q.SetUserRole(ctx, store.SetUserRoleParams{ID: u.ID, Role: "admin"}); err != nil {
			t.Fatal(err)
		}
		if _, err := q.SetUserRole(ctx, store.SetUserRoleParams{ID: u.ID, Role: "viewer"}); err != nil {
			t.Fatal(err)
		}
		if got := upsert(t, q, 15, "dem"); got.Role != "viewer" {
			t.Errorf("role = %s, want viewer", got.Role)
		}
	})
}

func TestTouchLastLogin(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	ctx := context.Background()
	q := store.New(pool)

	u := mustLocal(t, q, "tl", "viewer")
	if u.LastLoginAt.Valid {
		t.Fatal("new local user has last_login_at")
	}
	before := time.Now().Add(-time.Minute)
	if err := q.TouchLastLogin(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	got, err := q.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastLoginAt.Valid || got.LastLoginAt.Time.Before(before) {
		t.Errorf("last_login_at = %+v", got.LastLoginAt)
	}
	// Reset to a fixed past value and touch again.
	if _, err := pool.Exec(ctx, `UPDATE users SET last_login_at = $2 WHERE id = $1`, u.ID, ts(baseTime)); err != nil {
		t.Fatal(err)
	}
	if err := q.TouchLastLogin(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	got, err = q.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastLoginAt.Time.After(baseTime) {
		t.Errorf("last_login_at not advanced: %v", got.LastLoginAt.Time)
	}
	// Unknown id is not an error for :exec.
	if err := q.TouchLastLogin(ctx, u.ID+1000); err != nil {
		t.Errorf("unknown id: %v", err)
	}
}

func TestListUsersPaging(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	ctx := context.Background()
	q := store.New(pool)

	empty, err := q.ListUsers(ctx, store.ListUsersParams{AfterID: 0, MaxRows: 10})
	if err != nil {
		t.Fatal(err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty table: got %#v, want non-nil empty slice", empty)
	}

	var ids []int64
	for _, n := range []string{"u1", "u2", "u3", "u4", "u5"} {
		ids = append(ids, mustLocal(t, q, n, "viewer").ID)
	}

	var seen []int64
	var after int64
	var pages int
	for {
		page, err := q.ListUsers(ctx, store.ListUsersParams{AfterID: after, MaxRows: 2})
		if err != nil {
			t.Fatal(err)
		}
		if page == nil {
			t.Fatal("nil page")
		}
		if len(page) == 0 {
			break
		}
		pages++
		if len(page) > 2 {
			t.Fatalf("page too large: %d", len(page))
		}
		for _, u := range page {
			seen = append(seen, u.ID)
		}
		after = page[len(page)-1].ID
	}
	if pages != 3 {
		t.Errorf("pages = %d, want 3", pages)
	}
	if len(seen) != len(ids) {
		t.Fatalf("seen %v, want %v", seen, ids)
	}
	for i := range ids {
		if seen[i] != ids[i] {
			t.Fatalf("seen %v, want %v", seen, ids)
		}
	}
	// after_id is exclusive.
	page, err := q.ListUsers(ctx, store.ListUsersParams{AfterID: ids[3], MaxRows: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].ID != ids[4] || page[0].Username != "u5" {
		t.Errorf("after ids[3]: %+v", page)
	}
	page, err = q.ListUsers(ctx, store.ListUsersParams{AfterID: ids[4], MaxRows: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page == nil || len(page) != 0 {
		t.Errorf("past end: %#v", page)
	}
}

func TestSetUserRole(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	ctx := context.Background()
	q := store.New(pool)

	admin := mustLocal(t, q, "admin1", "admin")
	viewer := mustLocal(t, q, "viewer1", "viewer")

	role := func(id int64) string {
		t.Helper()
		u, err := q.GetUser(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return u.Role
	}

	// Demoting the only admin fails and leaves the role unchanged.
	if _, err := store.SetUserRole(ctx, pool, admin.ID, store.RoleViewer); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("demote last admin: err = %v, want ErrLastAdmin", err)
	}
	if got := role(admin.ID); got != "admin" {
		t.Errorf("role after refused demotion = %s", got)
	}

	// Promote.
	u, err := store.SetUserRole(ctx, pool, viewer.ID, store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != viewer.ID || u.Role != "admin" || u.Username != "viewer1" {
		t.Errorf("promoted user %+v", u)
	}
	if n, _ := q.CountAdmins(ctx); n != 2 {
		t.Errorf("CountAdmins = %d, want 2", n)
	}

	// Demote with another admin present.
	u, err = store.SetUserRole(ctx, pool, admin.ID, store.RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != admin.ID || u.Role != "viewer" {
		t.Errorf("demoted user %+v", u)
	}
	if got := role(viewer.ID); got != "admin" {
		t.Errorf("remaining admin role = %s", got)
	}

	// Now viewer1 is the last admin.
	if _, err := store.SetUserRole(ctx, pool, viewer.ID, store.RoleViewer); !errors.Is(err, store.ErrLastAdmin) {
		t.Fatalf("err = %v, want ErrLastAdmin", err)
	}

	// Setting the role of the last admin to admin is fine.
	if _, err := store.SetUserRole(ctx, pool, viewer.ID, store.RoleAdmin); err != nil {
		t.Errorf("admin->admin: %v", err)
	}

	// Demoting a non-admin when only one admin exists is fine.
	if _, err := store.SetUserRole(ctx, pool, admin.ID, store.RoleViewer); err != nil {
		t.Errorf("viewer->viewer: %v", err)
	}

	// Unknown id.
	if _, err := store.SetUserRole(ctx, pool, 987654, store.RoleAdmin); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown id promote: %v, want ErrNotFound", err)
	}
	if _, err := store.SetUserRole(ctx, pool, 987654, store.RoleViewer); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown id demote: %v, want ErrNotFound", err)
	}

	// Invalid role is rejected by the schema.
	_, err = store.SetUserRole(ctx, pool, admin.ID, "root")
	assertPgCode(t, err, pgCheck)
}

func TestSetUserRoleConcurrentDemotion(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	ctx := context.Background()
	q := store.New(pool)

	a := mustLocal(t, q, "ca", "admin")
	b := mustLocal(t, q, "cb", "admin")

	for round := 0; round < 20; round++ {
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make([]error, 2)
		for i, id := range []int64{a.ID, b.ID} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, errs[i] = store.SetUserRole(ctx, pool, id, store.RoleViewer)
			}()
		}
		close(start)
		wg.Wait()

		var failed, ok int
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, store.ErrLastAdmin):
				failed++
			default:
				t.Fatalf("round %d: unexpected error %v", round, err)
			}
		}
		if ok != 1 || failed != 1 {
			t.Fatalf("round %d: ok=%d failed=%d, want 1 and 1 (errs %v)", round, ok, failed, errs)
		}
		n, err := q.CountAdmins(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("round %d: admins = %d, want 1", round, n)
		}
		// Re-promote both so the next round starts with two admins.
		for _, id := range []int64{a.ID, b.ID} {
			if _, err := store.SetUserRole(ctx, pool, id, store.RoleAdmin); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func newSessionFixture(t *testing.T) (*pgxpool.Pool, *store.Queries, store.User) {
	t.Helper()
	pool := storetest.New(t)
	q := store.New(pool)
	return pool, q, mustLocal(t, q, "sess", "viewer")
}

func getSession(q *store.Queries, h []byte, now, cutoff time.Time) (store.GetSessionRow, error) {
	return q.GetSession(context.Background(), store.GetSessionParams{IDHash: h, Now: ts(now), IdleCutoff: ts(cutoff)})
}

func TestSessionLifecycle(t *testing.T) {
	t.Parallel()
	pool, q, u := newSessionFixture(t)
	ctx := context.Background()

	h := hash(7)
	expires := baseTime.Add(24 * time.Hour)
	if err := q.CreateSession(ctx, store.CreateSessionParams{IDHash: h, UserID: u.ID, ExpiresAt: ts(expires)}); err != nil {
		t.Fatal(err)
	}
	// Pin last_seen_at to a known value.
	seen := baseTime.Add(-time.Hour)
	if _, err := pool.Exec(ctx, `UPDATE sessions SET last_seen_at = $2 WHERE id_hash = $1`, h, ts(seen)); err != nil {
		t.Fatal(err)
	}

	row, err := getSession(q, h, baseTime, baseTime.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if string(row.Session.IDHash) != string(h) || row.Session.UserID != u.ID ||
		!row.Session.ExpiresAt.Time.Equal(expires) || !row.Session.LastSeenAt.Time.Equal(seen) || !row.Session.CreatedAt.Valid {
		t.Errorf("session = %+v", row.Session)
	}
	if row.User.ID != u.ID || row.User.Username != u.Username || row.User.Role != u.Role || row.User.Source != u.Source {
		t.Errorf("user = %+v, want %+v", row.User, u)
	}

	// Unknown hash.
	if _, err := getSession(q, hash(8), baseTime, baseTime.Add(-2*time.Hour)); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("unknown hash: %v", err)
	}

	// Expiry boundary: expires_at <= now is rejected, just before is accepted.
	if _, err := getSession(q, h, expires, baseTime.Add(-2*time.Hour)); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expires_at == now: %v", err)
	}
	if _, err := getSession(q, h, expires.Add(time.Hour), baseTime.Add(-2*time.Hour)); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("past expiry: %v", err)
	}
	if _, err := getSession(q, h, expires.Add(-time.Microsecond), baseTime.Add(-2*time.Hour)); err != nil {
		t.Errorf("just before expiry: %v", err)
	}

	// Idle boundary: last_seen_at <= cutoff is rejected.
	if _, err := getSession(q, h, baseTime, seen); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("last_seen == cutoff: %v", err)
	}
	if _, err := getSession(q, h, baseTime, seen.Add(time.Minute)); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("idle: %v", err)
	}
	if _, err := getSession(q, h, baseTime, seen.Add(-time.Microsecond)); err != nil {
		t.Errorf("just inside idle window: %v", err)
	}

	// Touch revives an otherwise idle session.
	touched := baseTime.Add(time.Minute)
	if err := q.TouchSession(ctx, store.TouchSessionParams{Now: ts(touched), IDHash: h}); err != nil {
		t.Fatal(err)
	}
	row, err = getSession(q, h, touched, baseTime)
	if err != nil {
		t.Fatalf("after touch: %v", err)
	}
	if !row.Session.LastSeenAt.Time.Equal(touched) {
		t.Errorf("last_seen_at = %v, want %v", row.Session.LastSeenAt.Time, touched)
	}
	// Touching an unknown hash is a no-op.
	if err := q.TouchSession(ctx, store.TouchSessionParams{Now: ts(touched), IDHash: hash(99)}); err != nil {
		t.Errorf("touch unknown: %v", err)
	}

	// Delete.
	if err := q.DeleteSession(ctx, h); err != nil {
		t.Fatal(err)
	}
	if _, err := getSession(q, h, baseTime, baseTime.Add(-100*time.Hour)); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("after delete: %v", err)
	}
	if err := q.DeleteSession(ctx, h); err != nil {
		t.Errorf("delete twice: %v", err)
	}
}

func TestDeleteExpiredSessions(t *testing.T) {
	t.Parallel()
	pool, q, u := newSessionFixture(t)
	ctx := context.Background()

	now := baseTime
	cutoff := now.Add(-30 * time.Minute)
	sessions := []struct {
		h        byte
		expires  time.Time
		lastSeen time.Time
		deleted  bool
	}{
		{1, now.Add(time.Hour), now.Add(-time.Minute), false},        // valid
		{2, now.Add(time.Microsecond), now, false},                   // valid, barely
		{3, now.Add(-time.Second), now.Add(-time.Minute), true},      // expired
		{4, now, now.Add(-time.Minute), true},                        // expires exactly now
		{5, now.Add(time.Hour), cutoff, true},                        // idle exactly at cutoff
		{6, now.Add(time.Hour), cutoff.Add(-time.Hour), true},        // idle
		{7, now.Add(-time.Hour), cutoff.Add(-time.Hour), true},       // expired and idle
		{8, now.Add(time.Hour), cutoff.Add(time.Microsecond), false}, // just active
	}
	var want int64
	for _, s := range sessions {
		if err := q.CreateSession(ctx, store.CreateSessionParams{IDHash: hash(s.h), UserID: u.ID, ExpiresAt: ts(s.expires)}); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE sessions SET last_seen_at = $2 WHERE id_hash = $1`, hash(s.h), ts(s.lastSeen)); err != nil {
			t.Fatal(err)
		}
		if s.deleted {
			want++
		}
	}
	n, err := q.DeleteExpiredSessions(ctx, store.DeleteExpiredSessionsParams{Now: ts(now), IdleCutoff: ts(cutoff)})
	if err != nil {
		t.Fatal(err)
	}
	if n != want {
		t.Errorf("rows deleted = %d, want %d", n, want)
	}
	for _, s := range sessions {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sessions WHERE id_hash = $1)`, hash(s.h)).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists == s.deleted {
			t.Errorf("session %d: exists=%v, want %v", s.h, exists, !s.deleted)
		}
	}
	n, err = q.DeleteExpiredSessions(ctx, store.DeleteExpiredSessionsParams{Now: ts(now), IdleCutoff: ts(cutoff)})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("second run deleted %d, want 0", n)
	}
}

func TestInsertAudit(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	ctx := context.Background()
	q := store.New(pool)

	detail := []byte(`{"target": 42, "tags": ["a", "b"], "nested": {"ok": true}}`)
	if err := q.InsertAudit(ctx, store.InsertAuditParams{Actor: "user:7", Action: "user.role_changed", Detail: detail}); err != nil {
		t.Fatal(err)
	}
	var (
		row     store.AuditLog
		eq      bool
		jsonOut string
	)
	err := pool.QueryRow(ctx, `SELECT id, at, actor, action, detail = $1::jsonb, detail::text FROM audit_log`, string(detail)).
		Scan(&row.ID, &row.At, &row.Actor, &row.Action, &eq, &jsonOut)
	if err != nil {
		t.Fatal(err)
	}
	if row.ID == 0 || row.Actor != "user:7" || row.Action != "user.role_changed" || !eq {
		t.Errorf("row = %+v eq=%v", row, eq)
	}
	// jsonb normalises whitespace and key order.
	if want := `{"tags": ["a", "b"], "nested": {"ok": true}, "target": 42}`; jsonOut != want {
		t.Errorf("detail = %s, want %s", jsonOut, want)
	}
	if !row.At.Valid || time.Since(row.At.Time) > time.Minute || time.Until(row.At.Time) > time.Minute {
		t.Errorf("at = %v", row.At.Time)
	}

	if err := q.InsertAudit(ctx, store.InsertAuditParams{Actor: "system", Action: "boot", Detail: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	var n int
	var d string
	if err := pool.QueryRow(ctx, `SELECT count(*), max(detail::text) FILTER (WHERE actor = 'system') FROM audit_log`).Scan(&n, &d); err != nil {
		t.Fatal(err)
	}
	if n != 2 || d != "{}" {
		t.Errorf("n=%d detail=%q", n, d)
	}

	// Invalid JSON is rejected.
	if err := q.InsertAudit(ctx, store.InsertAuditParams{Actor: "x", Action: "y", Detail: []byte(`{bad`)}); err == nil {
		t.Error("invalid JSON accepted")
	}
}
