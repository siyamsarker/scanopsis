// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package store_test

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/store"
)

// artifactKeyFixtures stores, at schema 10, the scans whose keys migration
// 00011 fills in. Service 1 is the project demo/web-frontend; service 2 is
// another project whose secret scan names a folder of demo/web-frontend.
// Every scan is older than the retention cutoff the test uses, so retention
// decides by "latest of its artifact" alone. The last scan is stored after a
// jump of the id sequence, so the keys are filled in more than one batch,
// and names the project path in another letter case.
const artifactKeyFixtures = `
INSERT INTO services (name, gitlab_project_path, created_via) VALUES
    ('web-frontend', 'demo/web-frontend', 'manual'),
    ('payments-api', 'shop/payments-api', 'manual');
INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected,
                   commit_sha, schema_version, scanned_at, count_high, raw_report)
SELECT f.service_id, f.kind, f.name, f.n, f.n, 'main', true, 'c' || f.n, 2,
       now() - interval '100 days' + f.n * interval '1 hour', f.n, '\x1f8b'
FROM (VALUES
    (1, 1, 'image', 'web-frontend-stage:2'),
    (2, 1, 'image', 'web-frontend-stage:3'),
    (3, 1, 'image', 'web-frontend-stage:4'),
    (4, 1, 'image', 'registry:5000/team/app:1'),
    (5, 1, 'image', 'app@sha256:4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945'),
    (6, 1, 'image', 'registry:5000/team/tool'),
    (7, 1, 'image', 'Registry.example.com/Team/Worker:1.2@sha256:0d2b1e8f'),
    (8, 1, 'secret-fs', '/builds/x/0/demo/web-frontend'),
    (9, 1, 'secret-fs', '/builds/x/0/demo/web-frontend/services/api'),
    (10, 1, 'secret-fs', '.'),
    (11, 1, 'secret-fs', 'services/web'),
    (12, 1, 'secret-fs', '/builds/x/0/demo/web-frontend-old/services/api'),
    (13, 2, 'secret-fs', '/builds/x/0/demo/web-frontend/services/api')
) AS f(n, service_id, kind, name);
SELECT setval(pg_get_serial_sequence('scans', 'id'), 20000);
INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected,
                   commit_sha, schema_version, scanned_at, count_high, raw_report)
VALUES (1, 'secret-fs', '/builds/x/0/Demo/Web-Frontend/services/api', 14, 14, 'main', true, 'c14', 2,
        now() - interval '100 days' + 14 * interval '1 hour', 14, '\x1f8b');
INSERT INTO vulnerabilities (scan_id, target, class, pkg_type, pkg_name, installed_version, fixed_version,
                             vuln_id, severity, title, suppressed)
SELECT s.id, f.target, 'os-pkgs', 'alpine', 'pkg', '1', '', 'CVE-2026-' || f.n, 'HIGH', 't', false
FROM (VALUES
    (1, 3, 'web-frontend-stage:4 (alpine 3.20.3)'),
    (2, 3, 'web-frontend-stage:4'),
    (3, 3, 'app/package-lock.json'),
    (4, 3, 'web-frontend-stage:40 (alpine 3.20.3)'),
    (5, 4, 'registry:5000/team/app:1 (ubuntu 22.04)'),
    (6, 5, 'app@sha256:4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945 (debian 12.8)'),
    (7, 6, 'registry:5000/team/tool (debian 12.8)'),
    (8, 12, '/builds/x/0/demo/web-frontend-old/services/api (debian 12.8)')
) AS f(n, job, target)
JOIN scans s ON s.gitlab_job_id = f.job;
SELECT setval(pg_get_serial_sequence('vulnerabilities', 'id'), 60000);
INSERT INTO vulnerabilities (scan_id, target, class, pkg_type, pkg_name, installed_version, fixed_version,
                             vuln_id, severity, title, suppressed)
SELECT id, 'Registry.example.com/Team/Worker:1.2@sha256:0d2b1e8f (alpine 3.20.3)', 'os-pkgs', 'alpine', 'pkg', '1', '',
       'CVE-2026-9', 'HIGH', 't', false
FROM scans WHERE gitlab_job_id = 7;
`

// wantTargets is the target of each vulnerability of artifactKeyFixtures
// after migration 00011, by vuln_id: the OS packages of an image scan named
// after the image get its key in place of the name.
var wantTargets = map[string]string{
	"CVE-2026-1": "web-frontend-stage (alpine 3.20.3)",
	"CVE-2026-2": "web-frontend-stage",
	"CVE-2026-3": "app/package-lock.json",
	"CVE-2026-4": "web-frontend-stage:40 (alpine 3.20.3)",
	"CVE-2026-5": "registry:5000/team/app (ubuntu 22.04)",
	"CVE-2026-6": "app (debian 12.8)",
	"CVE-2026-7": "registry:5000/team/tool (debian 12.8)",
	"CVE-2026-8": "/builds/x/0/demo/web-frontend-old/services/api (debian 12.8)",
	"CVE-2026-9": "Registry.example.com/Team/Worker (alpine 3.20.3)",
}

// sameTargets checks the targets of the vulnerabilities of
// artifactKeyFixtures, which span more than one batch of the rename.
func sameTargets(t *testing.T, db store.DBTX) {
	t.Helper()
	rows, err := db.Query(context.Background(), `SELECT vuln_id, target FROM vulnerabilities`)
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ ID, Target string }
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range list {
		if want, ok := wantTargets[r.ID]; !ok || r.Target != want {
			t.Errorf("%s: target %q, want %q", r.ID, r.Target, want)
		}
	}
	if len(list) != len(wantTargets) {
		t.Errorf("%d vulnerabilities, want %d", len(list), len(wantTargets))
	}
	var lo, hi int64
	if err := db.QueryRow(context.Background(), `SELECT min(id), max(id) FROM vulnerabilities`).Scan(&lo, &hi); err != nil {
		t.Fatal(err)
	}
	if hi-lo < 2*20000 {
		t.Errorf("vulnerability ids %d to %d span less than two batches", lo, hi)
	}
}

// wantArtifactKeys is the key migration 00011 gives each scan of
// artifactKeyFixtures, by gitlab_job_id.
var wantArtifactKeys = map[int64]string{
	1:  "web-frontend-stage",
	2:  "web-frontend-stage",
	3:  "web-frontend-stage",
	4:  "registry:5000/team/app",
	5:  "app",
	6:  "registry:5000/team/tool",
	7:  "Registry.example.com/Team/Worker",
	8:  ".",
	9:  "services/api",
	10: ".",
	11: "services/web",
	12: "/builds/x/0/demo/web-frontend-old/services/api",
	13: "/builds/x/0/demo/web-frontend/services/api",
	14: "services/api",
}

// artifactKeys returns the artifact key of every scan by gitlab_job_id.
func artifactKeys(t *testing.T, db store.DBTX) map[int64]string {
	t.Helper()
	rows, err := db.Query(context.Background(), `SELECT gitlab_job_id, artifact_key FROM scans`)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		Job int64
		Key string
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		t.Fatal(err)
	}
	out := map[int64]string{}
	for _, r := range list {
		out[r.Job] = r.Key
	}
	return out
}

func sameKeys(t *testing.T, got map[int64]string) {
	t.Helper()
	for _, job := range slices.Sorted(maps.Keys(wantArtifactKeys)) {
		if got[job] != wantArtifactKeys[job] {
			t.Errorf("scan of job %d: artifact key %q, want %q", job, got[job], wantArtifactKeys[job])
		}
	}
	if len(got) != len(wantArtifactKeys) {
		t.Errorf("%d scans, want %d", len(got), len(wantArtifactKeys))
	}
}

// keysOfJobs checks the key of every scan that is left, by gitlab_job_id.
func keysOfJobs(t *testing.T, got map[int64]string, jobs []int64) {
	t.Helper()
	for _, job := range jobs {
		if k, ok := got[job]; !ok || k != wantArtifactKeys[job] {
			t.Errorf("scan of job %d: artifact key %q (present %v), want %q", job, k, ok, wantArtifactKeys[job])
		}
	}
	if len(got) != len(jobs) {
		t.Errorf("%d scans, want %d", len(got), len(jobs))
	}
}

// batchesUsed fails unless the scans span more than one batch of the
// backfill (5000 ids).
func batchesUsed(t *testing.T, db store.DBTX) {
	t.Helper()
	var lo, hi int64
	if err := db.QueryRow(context.Background(), `SELECT min(id), max(id) FROM scans`).Scan(&lo, &hi); err != nil {
		t.Fatal(err)
	}
	if hi-lo < 3*5000 {
		t.Errorf("scan ids %d to %d span less than three batches", lo, hi)
	}
}

// TestMigrate00011Backfill stores scans at schema 10 and migrates with
// goose: every scan gets the key of its artifact, the current state holds
// one scan per key (the latest tag of an image), retention keeps the latest
// scan per key, the column refuses NULL, and the indexes on artifact_name
// are replaced by indexes on artifact_key. A start that was interrupted
// after the key was filled in, or part-way through filling it in, applies
// the migration again and fills in the rest.
func TestMigrate00011Backfill(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := emptyDatabase(t, "migrate_00011")
	if _, err := provider(t, pool).UpTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, artifactKeyFixtures); err != nil {
		t.Fatal(err)
	}
	batchesUsed(t, pool)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sameKeys(t, artifactKeys(t, pool))
	sameTargets(t, pool)

	q := store.New(pool)
	cur, err := q.CurrentScans(ctx, store.CurrentScansParams{ServiceID: 1, Ref: "main"})
	if err != nil {
		t.Fatal(err)
	}
	var images, secrets []string
	for _, s := range cur {
		if s.Kind == "image" {
			images = append(images, s.ArtifactName)
		} else {
			secrets = append(secrets, s.ArtifactName)
		}
	}
	slices.Sort(images)
	slices.Sort(secrets)
	if want := []string{
		"Registry.example.com/Team/Worker:1.2@sha256:0d2b1e8f",
		"app@sha256:4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945",
		"registry:5000/team/app:1",
		"registry:5000/team/tool",
		"web-frontend-stage:4",
	}; !slices.Equal(images, want) {
		t.Errorf("current image scans %q, want %q", images, want)
	}
	// "." and the build folder of the project are one artifact; its latest
	// scan is the one named ".". The folder services/api, named with the
	// project path in two letter cases, is one artifact too.
	if want := []string{".", "/builds/x/0/Demo/Web-Frontend/services/api", "/builds/x/0/demo/web-frontend-old/services/api", "services/web"}; !slices.Equal(secrets, want) {
		t.Errorf("current secret scans %q, want %q", secrets, want)
	}

	// Retention: every scan is 100 days old and on a protected ref, so only
	// the latest scan of each key is kept.
	cutoff := pgtype.Timestamptz{Time: time.Now().Add(-30 * 24 * time.Hour), Valid: true}
	deleted, err := q.DeleteExpiredScans(ctx, store.DeleteExpiredScansParams{
		AfterAt: pgtype.Timestamptz{Time: time.Unix(0, 0), Valid: true}, Cutoff: cutoff, BatchSize: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 4 {
		t.Errorf("retention deleted %d scans, want 4 (the tags 2 and 3, the build folder scan and the first services/api scan)", len(deleted))
	}
	left := artifactKeys(t, pool)
	for _, job := range []int64{1, 2, 8, 9} {
		if _, ok := left[job]; ok {
			t.Errorf("retention kept the scan of job %d (%s), which is not the latest of its key", job, wantArtifactKeys[job])
		}
	}
	if _, ok := left[3]; !ok {
		t.Error("retention deleted web-frontend-stage:4, the latest scan of its key")
	}

	if _, err := pool.Exec(ctx, `INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref,
		ref_protected, commit_sha, schema_version, scanned_at, raw_report)
		VALUES (1, 'image', 'web-frontend-stage:5', 20, 20, 'main', true, 'c20', 2, now(), '\x1f8b')`); err == nil {
		t.Error("a scan without an artifact key was stored")
	}

	checkArtifactKeyIndexes(t, pool)

	kept := []int64{3, 4, 5, 6, 7, 10, 11, 12, 13, 14}

	// A start that stopped after the key was filled in, with an index left
	// invalid by an interrupted build, applies the whole migration again.
	for _, q := range []string{
		`UPDATE pg_index SET indisvalid = false WHERE indexrelid = 'scans_current_key_idx'::regclass`,
		`DELETE FROM goose_db_version WHERE version_id = 11`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate again: %v", err)
	}
	checkArtifactKeyIndexes(t, pool)
	keysOfJobs(t, artifactKeys(t, pool), kept)
	sameTargets(t, pool)

	// A start that stopped part-way through the backfill: some scans have
	// a key, the others none, and NOT NULL is not set yet. The next start
	// fills in the rest, in more than one batch, and sets NOT NULL.
	for _, q := range []string{
		`ALTER TABLE scans ALTER COLUMN artifact_key DROP NOT NULL`,
		`UPDATE scans SET artifact_key = NULL WHERE gitlab_job_id IN (3, 7, 12, 14)`,
		// ... and part-way through renaming the targets.
		`UPDATE vulnerabilities SET target = 'web-frontend-stage:4 (alpine 3.20.3)' WHERE vuln_id = 'CVE-2026-1'`,
		`UPDATE vulnerabilities SET target = 'Registry.example.com/Team/Worker:1.2@sha256:0d2b1e8f (alpine 3.20.3)' WHERE vuln_id = 'CVE-2026-9'`,
		`DELETE FROM goose_db_version WHERE version_id = 11`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	batchesUsed(t, pool)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate after an interrupted backfill: %v", err)
	}
	keysOfJobs(t, artifactKeys(t, pool), kept)
	sameTargets(t, pool)
	var notNull bool
	if err := pool.QueryRow(ctx, `SELECT attnotnull FROM pg_attribute
		WHERE attrelid = 'scans'::regclass AND attname = 'artifact_key'`).Scan(&notNull); err != nil || !notNull {
		t.Errorf("artifact_key NOT NULL after the interrupted backfill: %v (%v)", notNull, err)
	}
	checkArtifactKeyIndexes(t, pool)

	// The migration can be rolled back and applied again.
	if _, err := provider(t, pool).DownTo(ctx, 10); err != nil {
		t.Fatalf("down to 10: %v", err)
	}
	after := indexes(t, pool)
	for _, name := range []string{"scans_current_cover_idx", "scans_protected_idx"} {
		if valid, ok := after[name]; !ok || !valid {
			t.Errorf("after the rollback, index %s: present %v, valid %v", name, ok, valid)
		}
	}
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate after the rollback: %v", err)
	}
	checkArtifactKeyIndexes(t, pool)
}

// checkArtifactKeyIndexes checks that the scans indexes are on artifact_key
// and valid, and that those they replace are gone.
func checkArtifactKeyIndexes(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	got := indexes(t, pool)
	for _, name := range []string{"scans_current_key_idx", "scans_protected_key_idx"} {
		if valid, ok := got[name]; !ok || !valid {
			t.Errorf("index %s: present %v, valid %v", name, ok, valid)
		}
	}
	for _, name := range []string{"scans_current_cover_idx", "scans_protected_idx"} {
		if _, ok := got[name]; ok {
			t.Errorf("index %s still exists", name)
		}
	}
}

// TestReplay00011Backfill loads scans into a schema replayed to version 10
// and replays the newer migrations in the same transaction, as a restore of
// a backup of version 0.4.0 does: the keys are filled in there too.
func TestReplay00011Backfill(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := emptyDatabase(t, "replay_00011")
	replay(t, pool, func(tx pgx.Tx) error {
		if err := store.ReplayMigrations(ctx, tx, 0, 10); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, artifactKeyFixtures); err != nil {
			return err
		}
		batchesUsed(t, tx)
		if err := store.ReplayMigrations(ctx, tx, 10, store.LatestVersion()); err != nil {
			return err
		}
		return store.RecordGooseVersions(ctx, tx, store.LatestVersion())
	})
	sameKeys(t, artifactKeys(t, pool))
	sameTargets(t, pool)
	checkArtifactKeyIndexes(t, pool)
	gooseAppliesNothing(t, pool)
}
