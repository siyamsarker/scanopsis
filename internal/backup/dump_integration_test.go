// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package backup

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

func newDumper(t *testing.T) (*Dumper, *pgxpool.Pool) {
	t.Helper()
	pool := storetest.New(t)
	dir, err := OpenDir(t.TempDir(), "fp")
	if err != nil {
		t.Fatal(err)
	}
	return &Dumper{Pool: pool, Dir: dir, App: "0.4.0", Commit: "abc", KeyFingerprint: "fp"}, pool
}

func allTables(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestTablesCoverSchema(t *testing.T) {
	pool := storetest.New(t)
	tables, err := Tables(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	inBackup := map[string]bool{}
	for _, tb := range tables {
		inBackup[tb.Name] = true
	}
	app := map[string]bool{}
	for _, n := range store.ApplicationTables() {
		app[n] = true
	}
	exists := map[string]bool{}
	for _, n := range allTables(t, pool) {
		exists[n] = true
		if !app[n] {
			t.Errorf("table %q exists after migrating but store.ApplicationTables() does not list it", n)
		}
	}
	for n := range app {
		if exists[n] && !inBackup[n] && !slices.Contains(Excluded, n) {
			t.Errorf("table %q is neither in the backup nor in Excluded: add it to one of them", n)
		}
	}
	for n := range inBackup {
		if !app[n] || !exists[n] || slices.Contains(Excluded, n) {
			t.Errorf("table %q is in the backup list but should not be", n)
		}
	}
}

func TestTablesIgnoreOtherApplications(t *testing.T) {
	d, pool := newDumper(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE other_app_data (id int PRIMARY KEY, note text)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO other_app_data VALUES (1, 'not ours')`); err != nil {
		t.Fatal(err)
	}
	tables, err := Tables(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range tables {
		if tb.Name == "other_app_data" {
			t.Fatal("a table of another application is in the backup list")
		}
	}
	info, err := d.Dump(ctx, KindManual, "alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := d.Dir.Path(info.Name)
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := ReadManifest(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range m.Tables {
		if tb.Name == "other_app_data" {
			t.Error("a table of another application is in the archive")
		}
	}
}

func TestTablesPassArchiveValidation(t *testing.T) {
	pool := storetest.New(t)
	tables, err := Tables(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkTables(tables); err != nil {
		t.Errorf("the schema has a table or column the backup format cannot hold: %v", err)
	}
}

func TestTablesOrder(t *testing.T) {
	pool := storetest.New(t)
	ctx := context.Background()
	tables, err := Tables(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, tb := range tables {
		pos[tb.Name] = i
	}
	rows, err := pool.Query(ctx, `
SELECT r.relname, p.relname FROM pg_constraint k
JOIN pg_class r ON r.oid = k.conrelid JOIN pg_class p ON p.oid = k.confrelid
WHERE k.contype = 'f' AND r.oid <> p.oid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			t.Fatal(err)
		}
		fp, ok1 := pos[from]
		tp, ok2 := pos[to]
		if ok1 && ok2 && tp > fp {
			t.Errorf("%s refers to %s but comes first", from, to)
		}
		n++
	}
	if n == 0 {
		t.Error("the schema has no foreign keys; the test checks nothing")
	}
}

func TestOrderTablesCycle(t *testing.T) {
	by := map[string]*TableSpec{"a": {Name: "a"}, "b": {Name: "b"}}
	_, err := orderTables([]string{"a", "b"}, by, map[string][]string{"a": {"b"}, "b": {"a"}})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Errorf("err = %v", err)
	}
}

func TestDumpBasic(t *testing.T) {
	d, pool := newDumper(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO services (name, gitlab_project_path, created_via) VALUES ('payments-api', 'example/payments-api', 'manual')`); err != nil {
		t.Fatal(err)
	}
	var steps []Progress
	info, err := d.Dump(ctx, KindManual, "alice", func(p Progress) { steps = append(steps, p) })
	if err != nil {
		t.Fatal(err)
	}
	want, _ := store.SchemaVersion(ctx, pool)
	if info.Kind != KindManual || info.CreatedBy != "alice" || info.Schema != want || !info.KeyMatches || info.AppVersion != "0.4.0" || info.Size <= 0 {
		t.Errorf("info = %+v", info)
	}
	if len(steps) == 0 {
		t.Error("no progress reported")
	}
	p, _ := d.Dir.Path(info.Name)
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file: %v %v", st, err)
	}
	f, _ := os.Open(p)
	defer f.Close()
	if _, _, err := Verify(f, nil); err != nil {
		t.Errorf("Verify: %v", err)
	}
	if list, _ := d.Dir.List(); len(list) != 1 || list[0].Name != info.Name {
		t.Errorf("List = %+v", list)
	}
}

func TestDumpConsistentUnderWrites(t *testing.T) {
	d, pool := newDumper(t)
	ctx := context.Background()
	var svc int64
	if err := pool.QueryRow(ctx, `INSERT INTO services (name, gitlab_project_path, created_via) VALUES ('payments-api', 'example/payments-api', 'manual') RETURNING id`).Scan(&svc); err != nil {
		t.Fatal(err)
	}
	const perScan = 5
	insert := func(i int) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		var id int64
		err = tx.QueryRow(ctx, `INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected, commit_sha, schema_version, scanned_at, count_high, raw_report)
VALUES ($1, 'image', 'app', $2, $2, 'main', false, 'c', 2, now(), $3, '\x1f8b') RETURNING id`, svc, i, perScan).Scan(&id)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO vulnerabilities (scan_id, target, class, pkg_name, vuln_id, severity)
SELECT $1, 't', 'os-pkgs', 'pkg' || g, 'CVE-2024-' || g, 'HIGH' FROM generate_series(1, $2) g`, id, perScan)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	for i := 0; i < 50; i++ {
		if err := insert(i); err != nil {
			t.Fatal(err)
		}
	}
	stop := make(chan struct{})
	var committed atomic.Int64
	var wg sync.WaitGroup
	var werr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1000; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := insert(i); err != nil {
				werr = err
				return
			}
			committed.Add(1)
		}
	}()
	var infos []Info
	before := committed.Load()
	for i := 0; i < 20 && (i < 5 || committed.Load() == before); i++ {
		info, err := d.Dump(ctx, KindManual, "alice", nil)
		if err != nil {
			t.Fatal(err)
		}
		infos = append(infos, info)
	}
	during := committed.Load() - before
	close(stop)
	wg.Wait()
	if werr != nil {
		t.Fatalf("writer: %v", werr)
	}
	if during == 0 {
		t.Fatal("the writer committed nothing while the dumps ran, so the test proves nothing")
	}
	t.Logf("%d inserts committed while %d dumps ran", during, len(infos))
	for _, info := range infos {
		checkConsistent(t, d, info.Name, perScan)
	}
}

func checkConsistent(t *testing.T, d *Dumper, name string, perScan int) {
	t.Helper()
	p, _ := d.Dir.Path(name)
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scans := map[string]int{} // id -> declared findings
	found := map[string]int{} // scan id -> findings seen
	var vulnScanIDs []string
	_, err = ReadTables(f, func(tb TableSpec, data io.Reader) (int64, error) {
		idCol := slices.Index(tb.Columns, "id")
		scanCol := slices.Index(tb.Columns, "scan_id")
		highCol := slices.Index(tb.Columns, "count_high")
		r := csv.NewReader(data)
		var n int64
		for {
			rec, err := r.Read()
			if err == io.EOF {
				return n, nil
			}
			if err != nil {
				return n, err
			}
			n++
			switch tb.Name {
			case "scans":
				h, _ := strconv.Atoi(rec[highCol])
				scans[rec[idCol]] = h
			case "vulnerabilities":
				vulnScanIDs = append(vulnScanIDs, rec[scanCol])
				found[rec[scanCol]]++
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range vulnScanIDs {
		if _, ok := scans[id]; !ok {
			t.Fatalf("%s: a finding refers to scan %s, which is not in the file", name, id)
		}
	}
	for id, want := range scans {
		if found[id] != want || want != perScan {
			t.Fatalf("%s: scan %s has %d findings, want %d", name, id, found[id], want)
		}
	}
	if len(scans) < 50 {
		t.Errorf("only %d scans in the file", len(scans))
	}
}

func partials(t *testing.T, dir string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, "*.partial"))
	return m
}

func TestDumpPartialRemovedOnError(t *testing.T) {
	d, pool := newDumper(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Enough data to span several chunks, so the cancel lands mid-dump.
	if _, err := pool.Exec(ctx, `INSERT INTO services (name, gitlab_project_path, created_via) VALUES ('payments-api', 'example/payments-api', 'manual')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected, commit_sha, schema_version, scanned_at, raw_report)
SELECT 1, 'image', 'app', g, g, 'main', false, 'c', 2, now(), '\x1f8b' FROM generate_series(1, 20000) g`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO vulnerabilities (scan_id, target, class, pkg_name, vuln_id, severity, title)
SELECT s.id, 't', 'os-pkgs', 'pkg', 'CVE-' || s.id || '-' || g, 'HIGH', repeat(md5(g::text), 4) FROM scans s, generate_series(1, 20) g`); err != nil {
		t.Fatal(err)
	}
	_, err := d.Dump(ctx, KindManual, "alice", func(p Progress) {
		if p.Table != "" && p.Bytes > 0 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if m := partials(t, d.Dir.path); len(m) != 0 {
		t.Errorf("partial files left: %v", m)
	}
	list, err := d.Dir.List()
	if err != nil || len(list) != 0 {
		t.Errorf("List = %v, %v", list, err)
	}
	// The connection must be usable afterwards.
	if _, err := d.Dump(context.Background(), KindManual, "alice", nil); err != nil {
		t.Errorf("dump after cancel: %v", err)
	}
}

func TestDumpNoSpace(t *testing.T) {
	d, _ := newDumper(t)
	d.Dir.freeBytes = func() (uint64, error) { return 1 << 20, nil }
	_, err := d.Dump(context.Background(), KindManual, "alice", nil)
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("err = %v, want ErrNoSpace", err)
	}
	if !strings.Contains(err.Error(), "1.0 MiB") || !strings.Contains(err.Error(), "MiB") {
		t.Errorf("message lacks numbers: %v", err)
	}
	if entries, _ := os.ReadDir(d.Dir.path); len(entries) != 0 {
		t.Errorf("files left: %v", entries)
	}
}

func TestDumpRejectsKind(t *testing.T) {
	d, _ := newDumper(t)
	if _, err := d.Dump(context.Background(), Kind("bogus"), "alice", nil); err == nil {
		t.Error("unknown kind accepted")
	}
}

func TestDumpFolderGone(t *testing.T) {
	d, _ := newDumper(t)
	root := d.Dir.path
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	_, err := d.Dump(context.Background(), KindManual, "alice", nil)
	if !errors.Is(err, ErrFolder) || !strings.Contains(err.Error(), root) {
		t.Errorf("err = %v, want ErrFolder naming %s", err, root)
	}
}

func TestDumpFolderReadOnly(t *testing.T) {
	d, _ := newDumper(t)
	root := d.Dir.path
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(root, 0o700) }()
	if f, err := os.CreateTemp(root, "x"); err == nil { // running as root
		_ = f.Close()
		t.Skip("folder permissions do not apply to this user")
	}
	_, err := d.Dump(context.Background(), KindManual, "alice", nil)
	if !errors.Is(err, ErrFolder) || !strings.Contains(err.Error(), root) || !strings.Contains(err.Error(), "writable") {
		t.Errorf("err = %v, want ErrFolder naming %s with the fix", err, root)
	}
}

func TestDumpPartialRemovedOnPanic(t *testing.T) {
	d, _ := newDumper(t)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic was swallowed")
			}
		}()
		_, _ = d.Dump(context.Background(), KindManual, "alice", func(Progress) { panic("boom") })
	}()
	if m := partials(t, d.Dir.path); len(m) != 0 {
		t.Errorf("partial files left: %v", m)
	}
	if _, err := d.Dump(context.Background(), KindManual, "alice", nil); err != nil {
		t.Errorf("dump after panic: %v", err)
	}
}
