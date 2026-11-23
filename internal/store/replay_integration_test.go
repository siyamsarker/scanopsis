// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package store_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

// catalogQuery describes every object of the current schema as one line of
// text: relations (with owner and storage parameters), columns (type,
// default, nullability, identity, generation, collation), indexes,
// constraints, sequences (with their state and owning column), triggers and
// functions. Two databases with the same lines have the same schema.
const catalogQuery = `
WITH s AS (SELECT oid FROM pg_namespace WHERE nspname = current_schema())
SELECT line FROM (
  SELECT format('relation %s kind=%s persistence=%s owner=%s options=%s',
                c.relname, c.relkind, c.relpersistence, pg_get_userbyid(c.relowner),
                coalesce(c.reloptions::text, '{}')) AS line
  FROM pg_class c WHERE c.relnamespace = (SELECT oid FROM s)
  UNION ALL
  SELECT format('column %s.%s #%s type=%s notnull=%s default=%s identity=%s generated=%s collation=%s',
                c.relname, a.attname, a.attnum, format_type(a.atttypid, a.atttypmod), a.attnotnull,
                coalesce(pg_get_expr(d.adbin, d.adrelid), '-'), a.attidentity, a.attgenerated,
                coalesce(co.collname, '-'))
  FROM pg_attribute a
  JOIN pg_class c ON c.oid = a.attrelid
  LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
  LEFT JOIN pg_collation co ON co.oid = a.attcollation
  WHERE c.relnamespace = (SELECT oid FROM s) AND c.relkind IN ('r', 'p', 'v', 'm', 'f')
    AND a.attnum > 0 AND NOT a.attisdropped
  UNION ALL
  SELECT format('index %s valid=%s def=%s', ic.relname, i.indisvalid, pg_get_indexdef(i.indexrelid))
  FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid
  WHERE ic.relnamespace = (SELECT oid FROM s)
  UNION ALL
  SELECT format('constraint %s.%s type=%s deferrable=%s def=%s',
                c.relname, k.conname, k.contype, k.condeferrable, pg_get_constraintdef(k.oid))
  FROM pg_constraint k JOIN pg_class c ON c.oid = k.conrelid
  WHERE k.connamespace = (SELECT oid FROM s)
  UNION ALL
  SELECT format('sequence %s type=%s start=%s increment=%s min=%s max=%s cache=%s cycle=%s last=%s owned_by=%s',
                c.relname, format_type(q.seqtypid, NULL), q.seqstart, q.seqincrement, q.seqmin, q.seqmax,
                q.seqcache, q.seqcycle, coalesce(ps.last_value::text, '-'),
                (SELECT format('%s.%s', o.relname, a.attname) FROM pg_depend d
                 JOIN pg_class o ON o.oid = d.refobjid
                 JOIN pg_attribute a ON a.attrelid = d.refobjid AND a.attnum = d.refobjsubid
                 WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype IN ('a', 'i')))
  FROM pg_sequence q JOIN pg_class c ON c.oid = q.seqrelid
  LEFT JOIN pg_sequences ps ON ps.schemaname = current_schema() AND ps.sequencename = c.relname
  WHERE c.relnamespace = (SELECT oid FROM s)
  UNION ALL
  SELECT format('trigger %s', pg_get_triggerdef(t.oid))
  FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid
  WHERE c.relnamespace = (SELECT oid FROM s) AND NOT t.tgisinternal
  UNION ALL
  SELECT format('function %s', p.oid::regprocedure)
  FROM pg_proc p WHERE p.pronamespace = (SELECT oid FROM s)
) lines ORDER BY line`

// catalog returns the lines of catalogQuery for the database of pool.
func catalog(t *testing.T, db store.DBTX) []string {
	t.Helper()
	rows, err := db.Query(context.Background(), catalogQuery)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

// sameCatalog fails the test with the lines that only one side has.
func sameCatalog(t *testing.T, wantName string, want []string, gotName string, got []string) {
	t.Helper()
	var onlyWant, onlyGot []string
	for _, l := range want {
		if !slices.Contains(got, l) {
			onlyWant = append(onlyWant, l)
		}
	}
	for _, l := range got {
		if !slices.Contains(want, l) {
			onlyGot = append(onlyGot, l)
		}
	}
	if len(onlyWant) == 0 && len(onlyGot) == 0 {
		if len(want) == 0 {
			t.Fatalf("both catalogs are empty")
		}
		return
	}
	var b strings.Builder
	b.WriteString("catalogs differ\n")
	for _, l := range onlyWant {
		b.WriteString("  only in " + wantName + ": " + l + "\n")
	}
	for _, l := range onlyGot {
		b.WriteString("  only in " + gotName + ": " + l + "\n")
	}
	t.Error(b.String())
}

type gooseRow struct {
	ID        int64
	VersionID int64
	IsApplied bool
}

// gooseRows returns the rows of goose_db_version in id order.
func gooseRows(t *testing.T, pool *pgxpool.Pool) []gooseRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT id, version_id, is_applied FROM goose_db_version ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[gooseRow])
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// latestFile is the version of the newest migration file, read from the
// directory rather than from the package under test.
func latestFile(t *testing.T) int64 {
	t.Helper()
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var latest int64
	for _, e := range entries {
		prefix, _, _ := strings.Cut(e.Name(), "_")
		v, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			t.Fatalf("migration %s: %v", e.Name(), err)
		}
		latest = max(latest, v)
	}
	return latest
}

// gooseAppliesNothing checks that goose sees every migration as applied and
// that starting the server would apply none.
func gooseAppliesNothing(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	statuses, err := provider(t, pool).Status(ctx)
	if err != nil {
		t.Fatalf("goose status: %v", err)
	}
	for _, st := range statuses {
		if st.State != goose.StateApplied {
			t.Errorf("goose status of version %d = %s, want applied", st.Source.Version, st.State)
		}
	}
	var logs bytes.Buffer
	if err := store.MigrateLogged(ctx, pool, slog.New(slog.NewTextHandler(&logs, nil))); err != nil {
		t.Fatalf("migrate after the replay: %v", err)
	}
	if strings.Contains(logs.String(), "applying migration") {
		t.Errorf("migrate applied migrations after the replay:\n%s", logs.String())
	}
}

// replay runs fn in one transaction on pool and commits it.
func replay(t *testing.T, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) {
	t.Helper()
	if err := pgx.BeginFunc(context.Background(), pool, fn); err != nil {
		t.Fatal(err)
	}
}

// TestReplayMatchesGoose replays every migration into an empty database in
// one transaction and records the versions: the schema and the version table
// are the same as after migrating with goose, and goose then has nothing to
// apply.
func TestReplayMatchesGoose(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if got, want := store.LatestVersion(), latestFile(t); got != want {
		t.Fatalf("LatestVersion() = %d, want %d", got, want)
	}
	migrated := storetest.New(t)
	replayed := emptyDatabase(t, "replay_matches_goose")
	replay(t, replayed, func(tx pgx.Tx) error {
		if err := store.ReplayMigrations(ctx, tx, 0, store.LatestVersion()); err != nil {
			return err
		}
		return store.RecordGooseVersions(ctx, tx, store.LatestVersion())
	})

	sameCatalog(t, "goose", catalog(t, migrated), "replay", catalog(t, replayed))
	if want, got := gooseRows(t, migrated), gooseRows(t, replayed); !slices.Equal(got, want) {
		t.Errorf("goose_db_version rows after the replay = %v, want %v", got, want)
	}
	for name, pool := range map[string]*pgxpool.Pool{"goose": migrated, "replay": replayed} {
		if v, err := store.SchemaVersion(ctx, pool); err != nil || v != store.LatestVersion() {
			t.Errorf("SchemaVersion after %s = %d (%v), want %d", name, v, err, store.LatestVersion())
		}
	}
	gooseAppliesNothing(t, replayed)
	sameCatalog(t, "goose", catalog(t, migrated), "replay then migrate", catalog(t, replayed))
}

// TestReplayPartialThenForward replays the migrations in two steps, as a
// restore of an older backup does: up to version 6, then the rest in the
// same transaction. It also checks that versions recorded at 6 let goose
// apply the remaining migrations itself.
func TestReplayPartialThenForward(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	migrated := storetest.New(t)
	want := catalog(t, migrated)

	twoSteps := emptyDatabase(t, "replay_two_steps")
	replay(t, twoSteps, func(tx pgx.Tx) error {
		if err := store.ReplayMigrations(ctx, tx, 0, 6); err != nil {
			return err
		}
		// Data of the older schema, loaded between the two steps.
		if _, err := tx.Exec(ctx, `INSERT INTO services (name, gitlab_project_path, created_via) VALUES ('payments-api', 'shop/payments-api', 'manual')`); err != nil {
			return err
		}
		if err := store.ReplayMigrations(ctx, tx, 6, store.LatestVersion()); err != nil {
			return err
		}
		return store.RecordGooseVersions(ctx, tx, store.LatestVersion())
	})
	// The identity sequence of services advanced with the row; delete it
	// and compare the rest.
	var name string
	if err := twoSteps.QueryRow(ctx, `DELETE FROM services RETURNING name`).Scan(&name); err != nil || name != "payments-api" {
		t.Fatalf("row loaded between the steps: %q, %v", name, err)
	}
	if _, err := twoSteps.Exec(ctx, `ALTER TABLE services ALTER COLUMN id RESTART`); err != nil {
		t.Fatal(err)
	}
	sameCatalog(t, "goose", want, "replay 0-6-latest", catalog(t, twoSteps))
	if got, wantRows := gooseRows(t, twoSteps), gooseRows(t, migrated); !slices.Equal(got, wantRows) {
		t.Errorf("goose_db_version rows = %v, want %v", got, wantRows)
	}
	gooseAppliesNothing(t, twoSteps)

	// Replay to 6 against goose up to 6, then goose applies the rest.
	gooseTo6 := emptyDatabase(t, "replay_goose_to_6")
	if _, err := provider(t, gooseTo6).UpTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	replayTo6 := emptyDatabase(t, "replay_to_6")
	replay(t, replayTo6, func(tx pgx.Tx) error {
		if err := store.ReplayMigrations(ctx, tx, 0, 6); err != nil {
			return err
		}
		return store.RecordGooseVersions(ctx, tx, 6)
	})
	sameCatalog(t, "goose up to 6", catalog(t, gooseTo6), "replay to 6", catalog(t, replayTo6))
	if v, err := store.SchemaVersion(ctx, replayTo6); err != nil || v != 6 {
		t.Errorf("SchemaVersion after the replay to 6 = %d (%v), want 6", v, err)
	}
	if err := store.Migrate(ctx, replayTo6); err != nil {
		t.Fatalf("goose after the replay to 6: %v", err)
	}
	sameCatalog(t, "goose", want, "replay to 6 then goose", catalog(t, replayTo6))
}

// TestReplayRollsBack checks that a replay is undone with its transaction,
// in an empty database and in one whose tables were dropped first, and that
// invalid version ranges are refused before anything runs.
func TestReplayRollsBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	errLater := errors.New("a later step failed")

	empty := emptyDatabase(t, "replay_rolls_back")
	err := pgx.BeginFunc(ctx, empty, func(tx pgx.Tx) error {
		if err := store.ReplayMigrations(ctx, tx, 0, store.LatestVersion()); err != nil {
			return err
		}
		if err := store.RecordGooseVersions(ctx, tx, store.LatestVersion()); err != nil {
			return err
		}
		if len(catalog(t, tx)) == 0 {
			t.Error("the replay created nothing inside its transaction")
		}
		return errLater
	})
	if !errors.Is(err, errLater) {
		t.Fatalf("transaction: %v", err)
	}
	if got := catalog(t, empty); len(got) != 0 {
		t.Errorf("schema after the rollback is not empty:\n%s", strings.Join(got, "\n"))
	}

	// A migrated database with data: drop, replay, fail, roll back.
	pool := storetest.New(t)
	if _, err := pool.Exec(ctx, `INSERT INTO services (name, gitlab_project_path, created_via) VALUES ('payments-api', 'shop/payments-api', 'manual')`); err != nil {
		t.Fatal(err)
	}
	before := catalog(t, pool)
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if err := store.DropApplicationTables(ctx, tx); err != nil {
			return err
		}
		if got := catalog(t, tx); len(got) != 0 {
			t.Errorf("schema after DropApplicationTables is not empty:\n%s", strings.Join(got, "\n"))
		}
		if err := store.ReplayMigrations(ctx, tx, 0, store.LatestVersion()); err != nil {
			return err
		}
		return errLater
	})
	if !errors.Is(err, errLater) {
		t.Fatalf("transaction: %v", err)
	}
	sameCatalog(t, "before", before, "after the rollback", catalog(t, pool))
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM services WHERE name = 'payments-api'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("services row after the rollback: count %d, %v", n, err)
	}

	// A statement that fails names its migration, and the transaction
	// leaves the schema as it was.
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		return store.ReplayMigrations(ctx, tx, 0, 1)
	})
	if err == nil || !strings.Contains(err.Error(), "00001_users_sessions_audit.sql") || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("replay over existing tables: %v, want an error naming 00001_users_sessions_audit.sql", err)
	}
	sameCatalog(t, "before", before, "after a failed replay", catalog(t, pool))

	// Ranges outside the embedded migrations are refused.
	latest := store.LatestVersion()
	for _, tc := range []struct {
		name string
		fn   func(tx pgx.Tx) error
	}{
		{"replay past the latest", func(tx pgx.Tx) error { return store.ReplayMigrations(ctx, tx, 0, latest+1) }},
		{"replay backwards", func(tx pgx.Tx) error { return store.ReplayMigrations(ctx, tx, 5, 3) }},
		{"replay from below zero", func(tx pgx.Tx) error { return store.ReplayMigrations(ctx, tx, -1, 3) }},
		{"record past the latest", func(tx pgx.Tx) error { return store.RecordGooseVersions(ctx, tx, latest+1) }},
		{"record below zero", func(tx pgx.Tx) error { return store.RecordGooseVersions(ctx, tx, -1) }},
	} {
		if err := pgx.BeginFunc(ctx, empty, tc.fn); err == nil {
			t.Errorf("%s: no error", tc.name)
		}
	}
	if got := catalog(t, empty); len(got) != 0 {
		t.Errorf("schema after refused ranges is not empty:\n%s", strings.Join(got, "\n"))
	}
	// An empty range does nothing.
	replay(t, empty, func(tx pgx.Tx) error { return store.ReplayMigrations(ctx, tx, 4, 4) })
	if got := catalog(t, empty); len(got) != 0 {
		t.Errorf("schema after an empty range is not empty:\n%s", strings.Join(got, "\n"))
	}
}

// TestDropApplicationTablesNarrowGrant runs as a role that only has CREATE
// on the public schema of a database it does not own, as "Use an existing
// PostgreSQL" in docs/deployment.md allows: goose migrates, then dropping
// the tables and replaying the migrations succeed in one transaction, leave
// the same schema, and keep a table of another role.
func TestDropApplicationTablesNarrowGrant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := storetest.New(t)
	for _, q := range []string{
		`CREATE ROLE replay_narrow LOGIN PASSWORD 'narrow-secret'`,
		`CREATE DATABASE replay_narrow`,
	} {
		if _, err := admin.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, q := range []string{`DROP DATABASE replay_narrow WITH (FORCE)`, `DROP ROLE replay_narrow`} {
			if _, err := admin.Exec(context.Background(), q); err != nil {
				t.Errorf("clean up: %v", err)
			}
		}
	})

	// As the administrator, in the new database: grant CREATE on public and
	// create a table the role does not own.
	cfg := admin.Config().Copy()
	cfg.ConnConfig.Database = "replay_narrow"
	owner, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	for _, q := range []string{
		`GRANT CREATE ON SCHEMA public TO replay_narrow`,
		`CREATE TABLE other_app (note text)`,
		`INSERT INTO other_app VALUES ('kept')`,
		`GRANT SELECT ON other_app TO replay_narrow`,
	} {
		if _, err := owner.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	cfg = cfg.Copy()
	cfg.ConnConfig.User = "replay_narrow"
	cfg.ConnConfig.Password = "narrow-secret"
	narrow, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(narrow.Close)
	var super, ownsDB, canCreate bool
	if err := narrow.QueryRow(ctx, `
		SELECT r.rolsuper, d.datdba = r.oid, has_schema_privilege('public', 'CREATE')
		FROM pg_roles r, pg_database d WHERE r.rolname = current_user AND d.datname = current_database()`,
	).Scan(&super, &ownsDB, &canCreate); err != nil {
		t.Fatal(err)
	}
	if super || ownsDB || !canCreate {
		t.Fatalf("role: superuser %v, owns the database %v, can create in public %v; want false, false, true", super, ownsDB, canCreate)
	}

	if err := store.Migrate(ctx, narrow); err != nil {
		t.Fatalf("migrate as the narrow role: %v", err)
	}
	if _, err := narrow.Exec(ctx, `INSERT INTO services (name, gitlab_project_path, created_via) VALUES ('payments-api', 'shop/payments-api', 'manual')`); err != nil {
		t.Fatal(err)
	}
	before := catalog(t, narrow)
	rowsBefore := gooseRows(t, narrow)

	replay(t, narrow, func(tx pgx.Tx) error {
		if err := store.DropApplicationTables(ctx, tx); err != nil {
			return err
		}
		if err := store.ReplayMigrations(ctx, tx, 0, store.LatestVersion()); err != nil {
			return err
		}
		return store.RecordGooseVersions(ctx, tx, store.LatestVersion())
	})

	// The tables are new and empty: the services sequence starts again, as
	// it does after a fresh migrate.
	var n int
	if err := narrow.QueryRow(ctx, `SELECT count(*) FROM services`).Scan(&n); err != nil || n != 0 {
		t.Errorf("services after the drop and replay: %d rows, %v; want 0", n, err)
	}
	reset := false
	for i, l := range before {
		if strings.HasPrefix(l, "sequence services_id_seq ") && strings.Contains(l, " last=1 ") {
			before[i] = strings.Replace(l, " last=1 ", " last=- ", 1)
			reset = true
		}
	}
	if !reset {
		t.Fatalf("no services_id_seq line with last=1 in the catalog:\n%s", strings.Join(before, "\n"))
	}
	sameCatalog(t, "goose", before, "drop and replay", catalog(t, narrow))
	if got := gooseRows(t, narrow); !slices.Equal(got, rowsBefore) {
		t.Errorf("goose_db_version rows = %v, want %v", got, rowsBefore)
	}
	var note string
	if err := narrow.QueryRow(ctx, `SELECT note FROM other_app`).Scan(&note); err != nil || note != "kept" {
		t.Errorf("table of another role after the drop: %q, %v", note, err)
	}
	gooseAppliesNothing(t, narrow)
}
