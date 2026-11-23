// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/store"
)

// spaceMargin is the room left over after the estimated size of a backup.
const spaceMargin = 512 << 20

// ErrNoSpace means the backup folder is too full for a new backup.
var ErrNoSpace = errors.New("not enough free space for a backup")

// spaceError carries the numbers of an ErrNoSpace.
type spaceError struct{ need, have uint64 }

func (e *spaceError) Error() string {
	return fmt.Sprintf("%v: a backup needs about %s and the backup folder has %s free", ErrNoSpace, humanBytes(e.need), humanBytes(e.have))
}

func (e *spaceError) Is(target error) bool { return target == ErrNoSpace }

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// dumpSettings apply to the transaction of a dump. The pool limits every
// statement to a few seconds (store.DefaultStatementTimeout), which is right
// for the server's queries but not for copying a table of millions of rows,
// so the dump lifts the limit for its own transaction only. The others fix
// how values are written, so the file is the same whatever the server or
// the database role configures: dates in ISO form and in UTC, floating-point
// numbers with every digit, binary data in hex.
var dumpSettings = []string{
	`SET LOCAL statement_timeout = 0`,
	`SET LOCAL TimeZone = 'UTC'`,
	`SET LOCAL DateStyle = 'ISO, YMD'`,
	`SET LOCAL IntervalStyle = 'postgres'`,
	`SET LOCAL extra_float_digits = 3`,
	`SET LOCAL bytea_output = 'hex'`,
}

// setLocal runs the SET LOCAL statements in tx.
func setLocal(ctx context.Context, tx pgx.Tx, stmts []string) error {
	for _, q := range stmts {
		if _, err := tx.Exec(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	return nil
}

// Dumper takes backups of the database into a Dir. KeyFingerprint and
// SigningKey are derived from the secret key of the installation with
// KeyFingerprint and SigningKey.
type Dumper struct {
	Pool           *pgxpool.Pool
	Dir            *Dir
	App, Commit    string
	KeyFingerprint string
	SigningKey     []byte
}

// Dump writes a backup of the database into the folder and returns its
// description. All tables are read in one REPEATABLE READ, READ ONLY
// transaction, so the file is a consistent picture of one moment even while
// scans arrive. The file is written under a .partial name and renamed when it
// is complete and on disk; on any error nothing is left behind. actor is who
// asked for it (a user name, or "scheduler"). progress may be nil.
func (d *Dumper) Dump(ctx context.Context, kind Kind, actor string, progress func(Progress)) (Info, error) {
	if !fileName.MatchString(d.Dir.NewName(kind, time.Now())) {
		return Info{}, fmt.Errorf("take backup: %q is not a kind of backup", kind)
	}
	tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Info{}, fmt.Errorf("take backup: %w", err)
	}
	defer func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = tx.Rollback(rctx)
	}()
	if err := setLocal(ctx, tx, dumpSettings); err != nil {
		return Info{}, fmt.Errorf("take backup: %w", err)
	}

	tables, err := Tables(ctx, tx)
	if err != nil {
		return Info{}, fmt.Errorf("take backup: %w", err)
	}
	version, err := store.SchemaVersion(ctx, tx)
	if err != nil {
		return Info{}, fmt.Errorf("take backup: %w", err)
	}
	var schema string
	if err := tx.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return Info{}, fmt.Errorf("take backup: %w", err)
	}
	sizes, err := d.checkSpace(ctx, tx, schema, tables)
	if err != nil {
		return Info{}, err
	}
	if progress != nil {
		// The bar moves with the data written, against an estimate made
		// from the size of each table.
		estimate, report := newBackupEstimate(sizes), progress
		progress = func(p Progress) { report(estimate.apply(p)) }
	}

	now := time.Now().UTC().Truncate(time.Second)
	m := Manifest{
		AppVersion: d.App, AppCommit: d.Commit, Schema: version, CreatedAt: now, Kind: kind,
		CreatedBy: actor, KeyFingerprint: d.KeyFingerprint, Tables: tables,
	}
	name := d.Dir.NewName(kind, now)
	if err := d.write(ctx, tx, schema, name, m, progress); err != nil {
		return Info{}, err
	}
	fi, err := os.Stat(filepath.Join(d.Dir.path, name))
	if err != nil {
		return Info{}, fmt.Errorf("take backup: %w", err)
	}
	// The file was written whole and synced: it needs no check to be listed.
	d.Dir.markChecked(name, fi, "")
	return d.Dir.info(name, fi), nil
}

// CheckSpace refuses, with ErrNoSpace and the numbers, a backup that would
// not fit the folder, so a request can be answered at once. Dump checks
// again when it runs.
func (d *Dumper) CheckSpace(ctx context.Context) error {
	tables, err := Tables(ctx, d.Pool)
	if err != nil {
		return fmt.Errorf("take backup: %w", err)
	}
	var schema string
	if err := d.Pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return fmt.Errorf("take backup: %w", err)
	}
	_, err = d.checkSpace(ctx, d.Pool, schema, tables)
	return err
}

// checkSpace refuses a backup that would not fit the folder, and returns
// the size PostgreSQL stores for each of tables, in their order.
func (d *Dumper) checkSpace(ctx context.Context, db store.DBTX, schema string, tables []TableSpec) ([]int64, error) {
	names := make([]string, len(tables))
	for i, t := range tables {
		names[i] = t.Name
	}
	var sizes []int64
	err := db.QueryRow(ctx, `SELECT coalesce(array_agg(pg_table_size(format('%I.%I', $1::text, n)::regclass) ORDER BY i), '{}')::bigint[] FROM unnest($2::text[]) WITH ORDINALITY AS t(n, i)`, schema, names).Scan(&sizes)
	if err != nil {
		return nil, fmt.Errorf("take backup: estimate size: %w", err)
	}
	if len(sizes) != len(tables) {
		return nil, fmt.Errorf("take backup: estimate size: %d sizes for %d tables", len(sizes), len(tables))
	}
	var size int64
	for _, s := range sizes {
		size += max(s, 0)
	}
	have, err := d.Dir.FreeBytes()
	if err != nil {
		return nil, fmt.Errorf("take backup: %w", err)
	}
	need := uint64(size) + spaceMargin //nolint:gosec // a sum of sizes of zero or more
	if have < need {
		return nil, fmt.Errorf("take backup: %w", &spaceError{need: need, have: have})
	}
	return sizes, nil
}

// folderError reports a failure of a backup to write into the backup
// folder.
func (d *Dir) folderError(err error) error {
	return fmt.Errorf("take backup: %w", d.writeError(err))
}

// errWriter remembers the first error of the file it writes to, so a failure
// of the folder can be told from a failure of the database.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	n, err := e.w.Write(p)
	if err != nil && e.err == nil {
		e.err = err
	}
	return n, err
}

func (d *Dumper) write(ctx context.Context, tx pgx.Tx, schema, name string, m Manifest, progress func(Progress)) error {
	final := filepath.Join(d.Dir.path, name)
	tmp := final + partial
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // name comes from NewName
	if err != nil {
		return d.Dir.folderError(err)
	}
	// Whatever happens, including a panic in the progress callback, the
	// partial file goes unless the backup was completed.
	complete := false
	defer func() {
		if !complete {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	ew := &errWriter{w: f}
	bw := bufio.NewWriterSize(ew, 256<<10)
	pc := tx.Conn().PgConn()
	copyTable := func(ctx context.Context, t TableSpec, w io.Writer) (int64, error) {
		cols := make([]string, len(t.Columns))
		for i, c := range t.Columns {
			cols[i] = pgx.Identifier{c}.Sanitize()
		}
		sql := fmt.Sprintf("COPY (SELECT %s FROM %s) TO STDOUT (FORMAT csv)", strings.Join(cols, ", "), pgx.Identifier{schema, t.Name}.Sanitize())
		tag, err := pc.CopyTo(ctx, w, sql)
		if err != nil {
			return 0, err
		}
		return tag.RowsAffected(), nil
	}
	if _, err := WriteArchive(ctx, bw, m, d.SigningKey, copyTable, progress); err != nil {
		if ew.err != nil {
			return d.Dir.folderError(ew.err)
		}
		return fmt.Errorf("take backup: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return d.Dir.folderError(err)
	}
	if err := f.Sync(); err != nil {
		return d.Dir.folderError(err)
	}
	if err := f.Close(); err != nil {
		return d.Dir.folderError(err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return d.Dir.folderError(err)
	}
	complete = true
	// Without a synced folder the rename may be lost after a crash, so the
	// backup is not reported as taken; take the file back out.
	dir, derr := os.Open(d.Dir.path)
	if derr == nil {
		derr = dir.Sync()
		_ = dir.Close()
	}
	if derr != nil {
		_ = os.Remove(final)
		return d.Dir.folderError(derr)
	}
	return nil
}
