// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/secretbox"
	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

// testKey is the secret key of the installations in these tests.
var testKey = bytes.Repeat([]byte{7}, secretbox.KeySize)

const (
	totpSecret   = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	recoveryCode = "abcdefghjkmnpqrstuvw" // normalised, 20 symbols
	sonarToken   = "squ_example_token"
)

func testFingerprint(t *testing.T) string {
	t.Helper()
	fp, err := KeyFingerprint(testKey)
	if err != nil {
		t.Fatal(err)
	}
	return fp
}

func testBox(t *testing.T) *secretbox.Box {
	t.Helper()
	b, err := secretbox.New(testKey)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// recoveryHash is the stored form of a version 2 recovery code, computed
// here from the definition rather than with the code under test.
func recoveryHash(t *testing.T, userID int64, code string) []byte {
	t.Helper()
	key, err := auth.RecoveryKey(testKey)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, key)
	var id [8]byte
	binary.BigEndian.PutUint64(id[:], uint64(userID))
	mac.Write(id[:])
	mac.Write([]byte(code))
	return mac.Sum(nil)
}

func mustExec(t *testing.T, db store.DBTX, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("%v in %.80s", err, q)
	}
}

// seedAll fills every table of the schema with rows: users with an
// encrypted TOTP secret and recovery codes, plain and encrypted settings,
// services, scans with findings and secret findings, SonarQube analyses and
// deliveries, audit entries and a session. It returns alice's id.
func seedAll(t *testing.T, db store.DBTX) int64 {
	t.Helper()
	ctx := context.Background()
	box := testBox(t)
	var alice int64
	if err := db.QueryRow(ctx, `INSERT INTO users (source, username, name, email, role, password_hash)
VALUES ('local', 'alice', 'Alice', 'alice@example.com', 'admin', '$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA') RETURNING id`).Scan(&alice); err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal([]byte(totpSecret), fmt.Sprintf("totp:%d", alice))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `UPDATE users SET totp_secret_enc = $1, totp_enabled_at = now(), totp_last_step = 42 WHERE id = $2`, sealed, alice)
	mustExec(t, db, `INSERT INTO users (source, gitlab_user_id, gitlab_issuer, username, role) VALUES ('gitlab', 7, 'https://gitlab.example.com', 'bob', 'viewer')`)
	mustExec(t, db, `INSERT INTO recovery_codes (user_id, code_hash, version) VALUES ($1, $2, 2), ($1, $3, 1)`,
		alice, recoveryHash(t, alice, recoveryCode), bytes.Repeat([]byte{1}, 32))

	token, err := box.Seal([]byte(sonarToken), "setting:sonar.token")
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO settings (key, value, secret_enc, source, updated_by) VALUES
('retention.days', '120', NULL, 'ui', 'alice'),
('gitlab.url', '"https://gitlab.example.com"', NULL, 'env', 'system'),
('sonar.token', NULL, $1, 'ui', 'alice'),
('backup.keep_days', '7', NULL, 'ui', 'alice')`, token)

	mustExec(t, db, `INSERT INTO services (name, gitlab_project_id, gitlab_project_path, sonar_project_key, created_via) VALUES
('payments-api', 101, 'example/payments-api', 'example_payments-api', 'ingest'),
('billing-api', NULL, 'example/billing-api', NULL, 'manual')`)
	mustExec(t, db, `INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected, commit_sha, trivy_version, schema_version, scanned_at, count_high, raw_report)
SELECT s.id, CASE WHEN g % 3 = 0 THEN 'secret-fs' ELSE 'image' END, 'app', g, s.id * 1000 + g, 'main', g % 2 = 0, 'c' || g, '0.60.0', 2,
       now() - g * interval '1 day', 2, decode('1f8b0800' || md5(g::text), 'hex')
FROM services s, generate_series(1, 6) g`)
	mustExec(t, db, `INSERT INTO vulnerabilities (scan_id, target, class, pkg_type, pkg_name, installed_version, fixed_version, vuln_id, severity, title, primary_url, suppressed)
SELECT s.id, 'debian 12', 'os-pkgs', 'debian', 'openssl', '3.0.1', '3.0.2', 'CVE-2024-' || g, 'HIGH',
       E'title with "quotes", a comma\nand a new line ' || g, 'https://example.com/' || g, g = 3
FROM scans s, generate_series(1, 3) g WHERE s.kind = 'image'`)
	mustExec(t, db, `INSERT INTO secret_findings (scan_id, target, rule_id, category, severity, title, start_line, end_line)
SELECT id, 'config/app.env', 'aws-access-key-id', 'AWS', 'CRITICAL', 'AWS key', 3, 3 FROM scans WHERE kind = 'secret-fs'`)
	mustExec(t, db, `INSERT INTO sonar_analyses (service_id, task_id, analysis_key, analysed_at, revision, quality_gate_status, gate_conditions, measures, mode, reliability_rating, coverage, duplicated_lines_density, gitlab_pipeline_id)
VALUES (1, 'task-1', 'AY1', now() - interval '2 days', 'c1', 'OK', '[{"metric": "coverage", "status": "OK"}]', '{"coverage": "81.3"}', 'mqr', 'A', 0.1::float8 + 0.2::float8, NULL, 5)`)
	mustExec(t, db, `INSERT INTO sonar_deliveries (task_id, project_key, outcome, service_id, detail, attempts) VALUES
('task-1', 'example_payments-api', 'linked', 1, '', 1), ('task-2', 'unknown', 'unlinked', NULL, 'no service', 2)`)
	mustExec(t, db, `INSERT INTO audit_log (actor, action, detail) VALUES
('alice', 'login.succeeded', '{"ip": "192.0.2.1"}'), ('system', 'retention.deleted', '{"scans": 0}')`)
	mustExec(t, db, `INSERT INTO sessions (id_hash, user_id, expires_at, mfa_verified) VALUES ($1, $2, now() + interval '1 hour', true)`,
		bytes.Repeat([]byte{9}, 32), alice)
	return alice
}

// changeAll changes the data of every table after a backup was taken.
func changeAll(t *testing.T, db store.DBTX) {
	t.Helper()
	for _, q := range []string{
		`DELETE FROM services WHERE name = 'billing-api'`,
		`UPDATE users SET name = 'Alice Changed', totp_last_step = 99 WHERE username = 'alice'`,
		`INSERT INTO users (source, username, role, password_hash) VALUES ('local', 'carol', 'admin', 'x')`,
		`DELETE FROM recovery_codes WHERE version = 1`,
		`UPDATE settings SET value = '30' WHERE key = 'retention.days'`,
		`DELETE FROM settings WHERE key = 'sonar.token'`,
		`INSERT INTO services (name, gitlab_project_path, created_via) VALUES ('orders-api', 'example/orders-api', 'manual')`,
		`INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected, commit_sha, schema_version, scanned_at, raw_report)
SELECT id, 'image', 'app', 77, 77, 'main', true, 'c77', 2, now(), '\x00' FROM services WHERE name = 'orders-api'`,
		`UPDATE vulnerabilities SET suppressed = NOT suppressed`,
		`DELETE FROM secret_findings`,
		`UPDATE sonar_analyses SET quality_gate_status = 'ERROR'`,
		`INSERT INTO sonar_deliveries (task_id, project_key, outcome) VALUES ('task-3', 'x', 'failed')`,
		`INSERT INTO audit_log (actor, action) VALUES ('carol', 'user.created')`,
		`INSERT INTO sessions (id_hash, user_id, expires_at) SELECT decode(repeat('ab', 32), 'hex'), id, now() + interval '1 hour' FROM users WHERE username = 'carol'`,
	} {
		mustExec(t, db, q)
	}
}

// snapshot returns the full contents of every table of the current schema,
// each row as text, sorted, keyed by table name. goose_db_version is
// compared without its time stamps. "(sequences)" holds the state of every
// sequence and "(indexes)" the definition of every index.
func snapshot(t *testing.T, db store.DBTX) map[string][]string {
	t.Helper()
	ctx := context.Background()
	strs := func(q string) []string {
		rows, err := db.Query(ctx, q)
		if err != nil {
			t.Fatalf("%v in %s", err, q)
		}
		out, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	snap := map[string][]string{}
	for _, name := range strs(`SELECT tablename FROM pg_tables WHERE schemaname = current_schema() ORDER BY 1`) {
		q := fmt.Sprintf(`SELECT x::text FROM %s x ORDER BY 1`, pgx.Identifier{name}.Sanitize())
		if name == "goose_db_version" {
			q = `SELECT (id, version_id, is_applied)::text FROM goose_db_version ORDER BY id`
		}
		snap[name] = strs(q)
	}
	snap["(sequences)"] = strs(`SELECT sequencename || ' ' || coalesce(last_value::text, '-') FROM pg_sequences WHERE schemaname = current_schema() ORDER BY 1`)
	snap["(indexes)"] = strs(`SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() ORDER BY 1`)
	return snap
}

// sameSnapshot fails the test for every table whose contents differ.
func sameSnapshot(t *testing.T, what string, want, got map[string][]string) {
	t.Helper()
	keys := map[string]bool{}
	for k := range want {
		keys[k] = true
	}
	for k := range got {
		keys[k] = true
	}
	for k := range keys {
		w, g := want[k], got[k]
		if slices.Equal(w, g) {
			continue
		}
		var only []string
		for _, l := range w {
			if !slices.Contains(g, l) {
				only = append(only, "  - "+l)
			}
		}
		for _, l := range g {
			if !slices.Contains(w, l) {
				only = append(only, "  + "+l)
			}
		}
		if len(only) > 6 {
			only = append(only[:6], "  ...")
		}
		t.Errorf("%s: table %s differs (%d rows, want %d):\n%s", what, k, len(g), len(w), strings.Join(only, "\n"))
	}
	if len(want) < 10 {
		t.Errorf("%s: the snapshot has only %d entries; it checks nothing", what, len(want))
	}
}

// schemaOf describes the tables of the current schema: columns, indexes,
// constraints and storage parameters.
func schemaOf(t *testing.T, db store.DBTX) []string {
	t.Helper()
	rows, err := db.Query(context.Background(), `
SELECT line FROM (
  SELECT format('column %s.%s %s notnull=%s default=%s identity=%s', c.relname, a.attname,
                format_type(a.atttypid, a.atttypmod), a.attnotnull, coalesce(pg_get_expr(d.adbin, d.adrelid), '-'), a.attidentity) AS line
  FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid
  LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
  WHERE c.relnamespace = current_schema()::regnamespace AND c.relkind = 'r' AND a.attnum > 0 AND NOT a.attisdropped
  UNION ALL
  SELECT format('table %s options=%s', relname, coalesce(reloptions::text, '{}')) FROM pg_class
  WHERE relnamespace = current_schema()::regnamespace AND relkind = 'r'
  UNION ALL
  SELECT 'index ' || indexdef FROM pg_indexes WHERE schemaname = current_schema()
  UNION ALL
  SELECT format('constraint %s.%s %s', c.relname, k.conname, pg_get_constraintdef(k.oid))
  FROM pg_constraint k JOIN pg_class c ON c.oid = k.conrelid WHERE k.connamespace = current_schema()::regnamespace
) l ORDER BY line`)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) < 50 {
		t.Fatalf("the schema has only %d lines", len(lines))
	}
	return lines
}

// dumpPool takes a backup of pool into a new folder and returns its path.
func dumpPool(t *testing.T, pool *pgxpool.Pool, fingerprint string) string {
	t.Helper()
	dir, err := OpenDir(t.TempDir(), fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	d := &Dumper{Pool: pool, Dir: dir, App: "0.4.0", Commit: "abc", KeyFingerprint: fingerprint}
	info, err := d.Dump(context.Background(), KindManual, "alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := dir.Path(info.Name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func countCSV(t *testing.T, data []byte) int64 {
	t.Helper()
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	var n int64
	for {
		_, err := r.Read()
		if err == io.EOF {
			return n
		}
		if err != nil {
			t.Fatal(err)
		}
		n++
	}
}

// rebuild writes a copy of the backup src to a new file next to it, with
// valid checksums, after editManifest and editData (either may be nil) have
// changed it. It returns the path of the copy.
func rebuild(t *testing.T, src string, editManifest func(*Manifest), editData func(table string, data []byte) []byte) string {
	t.Helper()
	f, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	data := map[string][]byte{}
	m, err := ReadTables(f, func(tb TableSpec, r io.Reader) (int64, error) {
		b, err := io.ReadAll(r)
		data[tb.Name] = b
		return countCSV(t, b), err
	})
	if err != nil {
		t.Fatal(err)
	}
	if editManifest != nil {
		editManifest(&m)
	}
	dst := filepath.Join(t.TempDir(), filepath.Base(src))
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	_, err = WriteArchive(context.Background(), out, m, func(_ context.Context, tb TableSpec, w io.Writer) (int64, error) {
		b := data[tb.Name]
		if editData != nil {
			b = editData(tb.Name, b)
		}
		_, err := w.Write(b)
		return countCSV(t, b), err
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// tamperTrailer returns the bytes of the backup src with its trailer
// changed by edit and everything else as it was.
func tamperTrailer(t *testing.T, src string, edit func(*Trailer)) []byte {
	t.Helper()
	f, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == trailerName {
			var trailer Trailer
			if err := json.Unmarshal(body, &trailer); err != nil {
				t.Fatal(err)
			}
			edit(&trailer)
			if body, err = json.Marshal(trailer); err != nil {
				t.Fatal(err)
			}
			h.Size = int64(len(body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func manifestOf(t *testing.T, path string) Manifest {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	m, err := ReadManifest(f)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func restoreOpts(t *testing.T) RestoreOptions {
	return RestoreOptions{Actor: "alice", KeyFingerprint: testFingerprint(t)}
}

// gooseClean checks that goose sees every migration as applied, so starting
// the server applies none.
func gooseClean(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var logs bytes.Buffer
	if err := store.MigrateLogged(context.Background(), pool, slog.New(slog.NewTextHandler(&logs, nil))); err != nil {
		t.Fatalf("migrate after the restore: %v", err)
	}
	if strings.Contains(logs.String(), "applying migration") {
		t.Errorf("migrate applied migrations after the restore:\n%s", logs.String())
	}
	if v, err := store.SchemaVersion(context.Background(), pool); err != nil || v != store.LatestVersion() {
		t.Errorf("schema version after the restore = %d (%v), want %d", v, err, store.LatestVersion())
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := storetest.New(t)
	alice := seedAll(t, pool)
	path := dumpPool(t, pool, testFingerprint(t))
	m := manifestOf(t, path)
	want := snapshot(t, pool)
	changeAll(t, pool)

	var steps []Progress
	o := restoreOpts(t)
	o.Progress = func(p Progress) { steps = append(steps, p) }
	if err := Restore(ctx, pool, path, o); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got := snapshot(t, pool)

	if len(want["sessions"]) == 0 || len(got["sessions"]) != 0 {
		t.Errorf("sessions: %d before the backup, %d after the restore; want some, then none", len(want["sessions"]), len(got["sessions"]))
	}
	var added []string
	for _, l := range got["audit_log"] {
		if !slices.Contains(want["audit_log"], l) {
			added = append(added, l)
		}
	}
	if len(added) != 1 || len(got["audit_log"]) != len(want["audit_log"])+1 {
		t.Errorf("audit log after the restore: %d rows, %d new: %q; want one new row", len(got["audit_log"]), len(added), added)
	}
	for _, k := range []string{"sessions", "audit_log", "(sequences)"} {
		delete(want, k)
		delete(got, k)
	}
	sameSnapshot(t, "after the restore", want, got)

	var actor string
	var detail map[string]any
	var auditID, maxBefore int64
	if err := pool.QueryRow(ctx, `SELECT id, actor, detail FROM audit_log WHERE action = 'backup.restored'`).Scan(&auditID, &actor, &detail); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT max(id) FROM audit_log WHERE action <> 'backup.restored'`).Scan(&maxBefore); err != nil {
		t.Fatal(err)
	}
	wantDetail := map[string]any{
		"name": filepath.Base(path), "created_at": m.CreatedAt.Format(time.RFC3339), "app_version": "0.4.0", "actor": "alice",
	}
	if actor != "alice" || fmt.Sprint(detail) != fmt.Sprint(wantDetail) || auditID != maxBefore+1 {
		t.Errorf("audit entry: id %d (max before %d), actor %q, detail %v; want id %d, actor alice, detail %v",
			auditID, maxBefore, actor, detail, maxBefore+1, wantDetail)
	}

	// The sequences continue after the restored rows.
	for _, q := range []string{
		`INSERT INTO services (name, gitlab_project_path, created_via) VALUES ('new-api', 'example/new-api', 'manual')`,
		`INSERT INTO users (source, username, role, password_hash) VALUES ('local', 'dave', 'viewer', 'x')`,
		`INSERT INTO sonar_deliveries (task_id, project_key, outcome) VALUES ('task-9', 'x', 'failed')`,
	} {
		mustExec(t, pool, q)
	}

	// Encrypted values open with the key, and the recovery code works.
	box := testBox(t)
	var totpEnc, tokenEnc []byte
	if err := pool.QueryRow(ctx, `SELECT totp_secret_enc FROM users WHERE id = $1`, alice).Scan(&totpEnc); err != nil {
		t.Fatal(err)
	}
	if b, err := box.Open(totpEnc, fmt.Sprintf("totp:%d", alice)); err != nil || string(b) != totpSecret {
		t.Errorf("TOTP secret after the restore: %q, %v", b, err)
	}
	if err := pool.QueryRow(ctx, `SELECT secret_enc FROM settings WHERE key = 'sonar.token'`).Scan(&tokenEnc); err != nil {
		t.Fatal(err)
	}
	if b, err := box.Open(tokenEnc, "setting:sonar.token"); err != nil || string(b) != sonarToken {
		t.Errorf("sonar.token after the restore: %q, %v", b, err)
	}
	used, err := store.New(pool).UseRecoveryCode(ctx, store.UseRecoveryCodeParams{UserID: alice, CodeHash: recoveryHash(t, alice, recoveryCode), Version: 2})
	if err != nil || used != alice {
		t.Errorf("recovery code after the restore: %d, %v", used, err)
	}

	gooseClean(t, pool)
	if len(steps) == 0 {
		t.Fatal("no progress reported")
	}
	last := steps[len(steps)-1]
	if last.TablesTotal != len(m.Tables) || last.TablesDone != last.TablesTotal || last.Bytes <= 0 {
		t.Errorf("last progress = %+v, want all %d tables done", last, len(m.Tables))
	}
}

var errInjected = errors.New("injected failure")

// restoreStages are the stages of a restore, in order.
var restoreStages = []string{
	stageVerified, stageDropped, stageSchema, stageLoaded, stageSequences,
	stageMigrated, stageRecorded, stageSettings, stageSessions, stageAudited,
}

func TestRestoreFailureLeavesDataUnchanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := storetest.New(t)
	seedAll(t, pool)
	// Enough findings that the copy of the table is still running when it
	// is cancelled.
	mustExec(t, pool, `INSERT INTO vulnerabilities (scan_id, target, class, pkg_name, vuln_id, severity, title)
SELECT s.id, 't', 'os-pkgs', 'pkg', 'CVE-2025-' || g, 'LOW', repeat(md5(g::text), 4)
FROM scans s, generate_series(1, 5000) g WHERE s.kind = 'image'`)
	good := dumpPool(t, pool, testFingerprint(t))
	m := manifestOf(t, good)
	lastTable := m.Tables[len(m.Tables)-1].Name
	badChecksum := tamperTrailer(t, good, func(tr *Trailer) {
		s := tr.Tables[lastTable]
		s.SHA256 = strings.Repeat("0", 64)
		tr.Tables[lastTable] = s
	})
	damaged := filepath.Join(t.TempDir(), filepath.Base(good))
	if err := os.WriteFile(damaged, badChecksum, 0o600); err != nil {
		t.Fatal(err)
	}
	// A finding repeated: its id breaks the primary key during the load.
	duplicate := rebuild(t, good, nil, func(table string, data []byte) []byte {
		if table != "vulnerabilities" {
			return data
		}
		// The last record, one of the plain ones added above.
		last := data[bytes.LastIndexByte(data[:len(data)-1], '\n')+1:]
		return append(slices.Clip(data), last...)
	})
	// The same data under a manifest that differs only in its key.
	otherKey, err := os.ReadFile(rebuild(t, good, func(m *Manifest) { m.KeyFingerprint = "another-key" }, nil))
	if err != nil {
		t.Fatal(err)
	}
	changeAll(t, pool)

	type tc struct {
		name     string
		file     string
		failAt   string // stage whose hook returns errInjected
		swapWith []byte // written over the file once it has been verified
		cancelIn string // table whose load cancels the context
		wantErr  error  // errors.Is target, if any
		wantText string // text the error must contain, if any
	}
	cases := []tc{
		{name: "damaged file", file: damaged, wantErr: ErrCorrupt, wantText: "checksum of table " + lastTable},
		{name: "checksum of the last table wrong after verifying", file: good, swapWith: badChecksum, wantErr: ErrCorrupt, wantText: "checksum of table " + lastTable},
		{name: "file replaced by a backup of another key after verifying", file: good, swapWith: otherKey, wantErr: ErrCorrupt, wantText: "changed while"},
		{name: "row violating a constraint", file: duplicate, wantText: "duplicate key"},
		{name: "context cancelled during a copy", file: good, cancelIn: "vulnerabilities", wantErr: context.Canceled},
	}
	for _, s := range restoreStages {
		cases = append(cases, tc{name: "failure after stage " + s, file: good, failAt: s, wantErr: errInjected})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := snapshot(t, pool)
			cctx, cancel := context.WithCancel(ctx)
			defer cancel()
			o := restoreOpts(t)
			if c.cancelIn != "" {
				// A megabyte into the data of the table, so the copy is
				// running when the context ends.
				start := int64(-1)
				o.Progress = func(p Progress) {
					switch {
					case p.Table != c.cancelIn:
					case start < 0:
						start = p.Bytes
					case p.Bytes-start > 1<<20:
						cancel()
					}
				}
			}
			original, err := os.ReadFile(c.file)
			if err != nil {
				t.Fatal(err)
			}
			o.testHook = func(stage string) error {
				if stage == stageVerified && c.swapWith != nil {
					// In place, as a file changed on disk while it is open.
					if err := os.WriteFile(c.file, c.swapWith, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if stage == c.failAt {
					return errInjected
				}
				return nil
			}
			err = Restore(cctx, pool, c.file, o)
			if c.swapWith != nil {
				if err := os.WriteFile(c.file, original, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("restore failed with: %v", err)
			switch {
			case err == nil:
				t.Fatal("restore succeeded")
			case c.wantErr != nil && !errors.Is(err, c.wantErr):
				t.Errorf("err = %v, want %v", err, c.wantErr)
			case !strings.Contains(err.Error(), c.wantText):
				t.Errorf("err = %v, want it to contain %q", err, c.wantText)
			}
			sameSnapshot(t, "after the failed restore", before, snapshot(t, pool))
		})
	}

	// After all that, the same file restores.
	if err := Restore(ctx, pool, good, restoreOpts(t)); err != nil {
		t.Fatalf("restore after the failures: %v", err)
	}
}

func TestRestoreRefusesKeyMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := storetest.New(t)
	seedAll(t, pool)
	path := dumpPool(t, pool, testFingerprint(t))
	noKey := dumpPool(t, pool, "")
	changeAll(t, pool)
	before := snapshot(t, pool)
	other, err := KeyFingerprint(bytes.Repeat([]byte{8}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct{ file, fp string }{
		"other key":                       {path, other},
		"no running fingerprint":          {path, ""},
		"backup without one":              {noKey, ""},
		"backup without one, running key": {noKey, testFingerprint(t)},
	} {
		o := restoreOpts(t)
		o.KeyFingerprint = c.fp
		err := Restore(ctx, pool, c.file, o)
		if !errors.Is(err, ErrKeyMismatch) {
			t.Errorf("%s: err = %v, want ErrKeyMismatch", name, err)
		}
		sameSnapshot(t, name, before, snapshot(t, pool))
	}
	if err := Restore(ctx, pool, path, RestoreOptions{Actor: "alice", KeyFingerprint: other}); err == nil ||
		!strings.Contains(err.Error(), "SCANOPSIS_SECRET_KEY") {
		t.Errorf("the refusal does not say how to fix it: %v", err)
	}
}

func TestRestoreRefusesNewerSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := storetest.New(t)
	seedAll(t, pool)
	newer := rebuild(t, dumpPool(t, pool, testFingerprint(t)), func(m *Manifest) { m.Schema = store.LatestVersion() + 1 }, nil)
	changeAll(t, pool)
	before := snapshot(t, pool)
	err := Restore(ctx, pool, newer, restoreOpts(t))
	if !errors.Is(err, ErrNewerSchema) {
		t.Errorf("err = %v, want ErrNewerSchema", err)
	}
	sameSnapshot(t, "after the refusal", before, snapshot(t, pool))
}

// olderFixtures inserts rows that every schema version from 3 on accepts.
const olderFixtures = `
INSERT INTO users (source, username, role, password_hash) VALUES ('local', 'alice', 'admin', 'x');
INSERT INTO settings (key, value, source, updated_by) VALUES ('gitlab.url', '"https://gitlab.example.com"', 'ui', 'alice');
INSERT INTO services (name, gitlab_project_path, created_via) VALUES ('payments-api', 'example/payments-api', 'manual');
INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected, commit_sha, schema_version, scanned_at, raw_report)
  SELECT 1, 'image', 'app', g, g, 'main', true, 'c' || g, 2, now() - g * interval '1 hour', '\x1f8b' FROM generate_series(1, 5) g;
INSERT INTO vulnerabilities (scan_id, target, class, pkg_name, vuln_id, severity) SELECT id, 't', 'os-pkgs', 'pkg', 'CVE-2024-1', 'HIGH' FROM scans;
INSERT INTO audit_log (actor, action) VALUES ('alice', 'login.succeeded');
`

func TestRestoreOlderSchema(t *testing.T) {
	t.Parallel()
	for _, version := range []int64{9, 7} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			old := storetest.New(t)
			if err := pgx.BeginFunc(ctx, old, func(tx pgx.Tx) error {
				if err := store.DropApplicationTables(ctx, tx); err != nil {
					return err
				}
				if err := store.ReplayMigrations(ctx, tx, 0, version); err != nil {
					return err
				}
				return store.RecordGooseVersions(ctx, tx, version)
			}); err != nil {
				t.Fatal(err)
			}
			mustExec(t, old, olderFixtures)
			if version >= 8 { // GitLab issuers and recovery code versions came with 8
				mustExec(t, old, `INSERT INTO users (source, gitlab_user_id, gitlab_issuer, username, role) VALUES ('gitlab', 7, 'https://gitlab.example.com', 'bob', 'viewer')`)
				mustExec(t, old, `INSERT INTO recovery_codes (user_id, code_hash, version) VALUES (1, decode(repeat('01', 32), 'hex'), 1)`)
			} else {
				mustExec(t, old, `INSERT INTO users (source, gitlab_user_id, username, role) VALUES ('gitlab', 7, 'bob', 'viewer')`)
				mustExec(t, old, `INSERT INTO recovery_codes (user_id, code_hash) VALUES (1, decode(repeat('01', 32), 'hex'))`)
			}
			path := dumpPool(t, old, testFingerprint(t))
			if m := manifestOf(t, path); m.Schema != version {
				t.Fatalf("the backup has schema %d, want %d", m.Schema, version)
			}

			pool := storetest.New(t)
			seedAll(t, pool)
			if err := Restore(ctx, pool, path, restoreOpts(t)); err != nil {
				t.Fatalf("restore: %v", err)
			}
			gooseClean(t, pool)
			if want, got := schemaOf(t, storetest.New(t)), schemaOf(t, pool); !slices.Equal(want, got) {
				t.Errorf("schema after the restore differs from a migrated one:\nwant %q\ngot  %q", want, got)
			}

			var services, scans, findings int
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM services), (SELECT count(*) FROM scans), (SELECT count(*) FROM vulnerabilities)`).Scan(&services, &scans, &findings); err != nil {
				t.Fatal(err)
			}
			if services != 1 || scans != 5 || findings != 5 {
				t.Errorf("restored %d services, %d scans, %d findings; want 1, 5, 5", services, scans, findings)
			}
			// The newer migrations ran over the restored rows.
			var issuer string
			var codeVersion int
			if err := pool.QueryRow(ctx, `SELECT u.gitlab_issuer, (SELECT version FROM recovery_codes) FROM users u WHERE username = 'bob'`).Scan(&issuer, &codeVersion); err != nil {
				t.Fatal(err)
			}
			if issuer != "https://gitlab.example.com" || codeVersion != 1 {
				t.Errorf("after the newer migrations: issuer %q, recovery code version %d", issuer, codeVersion)
			}
			mustExec(t, pool, `INSERT INTO services (name, gitlab_project_path, created_via) VALUES ('new-api', 'example/new-api', 'manual')`)
		})
	}
}

func settingValue(t *testing.T, pool *pgxpool.Pool, key string) string {
	t.Helper()
	var v string
	err := pool.QueryRow(context.Background(), `SELECT coalesce(value::text, '(secret)') FROM settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "(none)"
	}
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRestoreKeepsHostSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := storetest.New(t)
	seedAll(t, pool) // backup.keep_days = 7
	path := dumpPool(t, pool, testFingerprint(t))
	host := func() {
		mustExec(t, pool, `UPDATE settings SET value = '30', updated_by = 'carol' WHERE key = 'backup.keep_days'`)
		mustExec(t, pool, `INSERT INTO settings (key, value, source, updated_by) VALUES ('backup.schedule_enabled', 'true', 'ui', 'carol')
ON CONFLICT (key) DO NOTHING`)
		mustExec(t, pool, `UPDATE settings SET value = '30' WHERE key = 'retention.days'`)
	}

	host()
	o := restoreOpts(t)
	o.KeepHostSettings = true
	if err := Restore(ctx, pool, path, o); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"backup.keep_days": "30", "backup.schedule_enabled": "true", "retention.days": "120"} {
		if got := settingValue(t, pool, key); got != want {
			t.Errorf("keeping the host settings: %s = %s, want %s", key, got, want)
		}
	}
	var by string
	if err := pool.QueryRow(ctx, `SELECT updated_by FROM settings WHERE key = 'backup.keep_days'`).Scan(&by); err != nil || by != "carol" {
		t.Errorf("kept setting updated_by = %q, %v; want carol", by, err)
	}

	host()
	if err := Restore(ctx, pool, path, restoreOpts(t)); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"backup.keep_days": "7", "backup.schedule_enabled": "(none)", "retention.days": "120"} {
		if got := settingValue(t, pool, key); got != want {
			t.Errorf("taking the backup's settings: %s = %s, want %s", key, got, want)
		}
	}
}

func TestRestoreNarrowGrant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, narrow := storetest.NarrowRole(t, "restore_narrow")
	if err := store.Migrate(ctx, narrow); err != nil {
		t.Fatal(err)
	}
	seedAll(t, narrow)
	path := dumpPool(t, narrow, testFingerprint(t))
	want := snapshot(t, narrow)
	changeAll(t, narrow)
	if err := Restore(ctx, narrow, path, restoreOpts(t)); err != nil {
		t.Fatalf("restore as the narrow role: %v", err)
	}
	got := snapshot(t, narrow)
	for _, k := range []string{"sessions", "audit_log", "(sequences)"} {
		delete(want, k)
		delete(got, k)
	}
	sameSnapshot(t, "after the restore as the narrow role", want, got)
	gooseClean(t, narrow)
}

// TestRestoreOutlastsStatementTimeout restores, on a pool whose statements
// are limited to 200 milliseconds, a table that takes longer than that to
// load.
func TestRestoreOutlastsStatementTimeout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := storetest.New(t)
	short := shortTimeoutPool(t, pool)
	seedLarge(t, pool, short)
	path := dumpPool(t, pool, testFingerprint(t))
	mustExec(t, pool, `DELETE FROM scans`)
	if err := Restore(ctx, short, path, restoreOpts(t)); err != nil {
		t.Fatalf("restore on a pool with a 200 ms statement timeout: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM vulnerabilities`).Scan(&n); err != nil || n != 400000 {
		t.Errorf("vulnerabilities after the restore: %d, %v; want 400000", n, err)
	}
}

func TestPreviewRetention(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := storetest.New(t)
	now := time.Now()
	mustExec(t, pool, `INSERT INTO services (name, gitlab_project_path, created_via) VALUES ('payments-api', 'example/payments-api', 'manual')`)
	mustExec(t, pool, `INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected, commit_sha, schema_version, scanned_at, raw_report)
VALUES (1, 'image', 'app', 1, 1, 'main', false, 'c', 2, $1, '\x00'), (1, 'image', 'app', 2, 2, 'main', false, 'c', 2, $2, '\x00')`,
		now.Add(-10*24*time.Hour), now.Add(-100*24*time.Hour))
	plain := dumpPool(t, pool, testFingerprint(t))
	mustExec(t, pool, `INSERT INTO settings (key, value, source, updated_by) VALUES ('retention.days', '120', 'ui', 'alice')`)
	long := dumpPool(t, pool, testFingerprint(t))
	mustExec(t, pool, `UPDATE settings SET value = '7' WHERE key = 'retention.days'`)
	short := dumpPool(t, pool, testFingerprint(t))

	for _, c := range []struct {
		name     string
		file     string
		current  int
		removed  int64
		days     int
		keyMatch bool
		fp       string
	}{
		{"current retention", plain, 90, 1, 90, true, testFingerprint(t)},
		{"current retention, shorter", plain, 7, 2, 7, true, testFingerprint(t)},
		{"backup's retention", long, 90, 0, 120, true, testFingerprint(t)},
		{"backup's short retention", short, 365, 2, 7, true, testFingerprint(t)},
		{"other key", plain, 90, 1, 90, false, "0123"},
	} {
		p, err := PreviewRestore(ctx, c.file, c.fp, c.current, now)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if p.ScansRemovedByRetention != c.removed || p.RetentionDays != c.days || p.KeyMatches != c.keyMatch || !p.SchemaOK {
			t.Errorf("%s: preview = %+v; want %d removed at %d days, key matches %v", c.name, p, c.removed, c.days, c.keyMatch)
		}
		if p.OldestScanAt == nil || p.OldestScanAt.Sub(now.Add(-100*24*time.Hour)).Abs() > time.Second {
			t.Errorf("%s: oldest scan = %v, want about 100 days ago", c.name, p.OldestScanAt)
		}
		if p.Info.Name != filepath.Base(c.file) || p.Info.Kind != KindManual || p.Info.CreatedBy != "alice" || p.Info.Size <= 0 ||
			p.Info.Schema != store.LatestVersion() || p.Info.KeyMatches != c.keyMatch {
			t.Errorf("%s: info = %+v", c.name, p.Info)
		}
	}

	newer := rebuild(t, plain, func(m *Manifest) { m.Schema = store.LatestVersion() + 1 }, nil)
	if p, err := PreviewRestore(ctx, newer, testFingerprint(t), 90, now); err != nil || p.SchemaOK {
		t.Errorf("newer schema: SchemaOK %v, %v; want false", p.SchemaOK, err)
	}

	damaged := filepath.Join(t.TempDir(), filepath.Base(plain))
	if err := os.WriteFile(damaged, tamperTrailer(t, plain, func(tr *Trailer) {
		s := tr.Tables["scans"]
		s.Rows++
		tr.Tables["scans"] = s
	}), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewRestore(ctx, damaged, testFingerprint(t), 90, now); !errors.Is(err, ErrCorrupt) {
		t.Errorf("damaged file: err = %v, want ErrCorrupt", err)
	}
}
