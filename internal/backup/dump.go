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

// Dumper takes backups of the database into a Dir.
type Dumper struct {
	Pool           *pgxpool.Pool
	Dir            *Dir
	App, Commit    string
	KeyFingerprint string
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

	tables, err := Tables(ctx, tx)
	if err != nil {
		return Info{}, fmt.Errorf("take backup: %w", err)
	}
	schema, err := store.SchemaVersion(ctx, tx)
	if err != nil {
		return Info{}, fmt.Errorf("take backup: %w", err)
	}
	if err := d.checkSpace(ctx, tx, tables); err != nil {
		return Info{}, err
	}

	now := time.Now().UTC().Truncate(time.Second)
	m := Manifest{
		AppVersion: d.App, AppCommit: d.Commit, Schema: schema, CreatedAt: now, Kind: kind,
		CreatedBy: actor, KeyFingerprint: d.KeyFingerprint, Tables: tables,
	}
	name := d.Dir.NewName(kind, now)
	if err := d.write(ctx, tx, name, m, progress); err != nil {
		return Info{}, err
	}
	fi, err := os.Stat(filepath.Join(d.Dir.path, name))
	if err != nil {
		return Info{}, fmt.Errorf("take backup: %w", err)
	}
	return d.Dir.info(name, fi), nil
}

func (d *Dumper) checkSpace(ctx context.Context, tx pgx.Tx, tables []TableSpec) error {
	names := make([]string, len(tables))
	for i, t := range tables {
		names[i] = pgx.Identifier{t.Name}.Sanitize()
	}
	var size int64
	err := tx.QueryRow(ctx, `SELECT coalesce(sum(pg_table_size(n::regclass)), 0)::bigint FROM unnest($1::text[]) n`, names).Scan(&size)
	if err != nil {
		return fmt.Errorf("take backup: estimate size: %w", err)
	}
	have, err := d.Dir.FreeBytes()
	if err != nil {
		return fmt.Errorf("take backup: %w", err)
	}
	need := uint64(max(size, 0)) + spaceMargin //nolint:gosec // clamped to zero or more
	if have < need {
		return fmt.Errorf("take backup: %w", &spaceError{need: need, have: have})
	}
	return nil
}

func (d *Dumper) write(ctx context.Context, tx pgx.Tx, name string, m Manifest, progress func(Progress)) (err error) {
	final := filepath.Join(d.Dir.path, name)
	tmp := final + partial
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // name comes from NewName
	if err != nil {
		return fmt.Errorf("take backup: %w", err)
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	bw := bufio.NewWriterSize(f, 256<<10)
	pc := tx.Conn().PgConn()
	copyTable := func(ctx context.Context, t TableSpec, w io.Writer) (int64, error) {
		cols := make([]string, len(t.Columns))
		for i, c := range t.Columns {
			cols[i] = pgx.Identifier{c}.Sanitize()
		}
		sql := fmt.Sprintf("COPY (SELECT %s FROM %s) TO STDOUT (FORMAT csv)", strings.Join(cols, ", "), pgx.Identifier{t.Name}.Sanitize())
		tag, err := pc.CopyTo(ctx, w, sql)
		if err != nil {
			return 0, err
		}
		return tag.RowsAffected(), nil
	}
	if _, err = WriteArchive(ctx, bw, m, copyTable, progress); err != nil {
		return fmt.Errorf("take backup: %w", err)
	}
	if err = bw.Flush(); err != nil {
		return fmt.Errorf("take backup: %w", err)
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("take backup: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("take backup: %w", err)
	}
	if err = os.Rename(tmp, final); err != nil {
		return fmt.Errorf("take backup: %w", err)
	}
	// Without a synced folder the rename may be lost after a crash, so the
	// backup is not reported as taken; take the file back out.
	dir, derr := os.Open(d.Dir.path)
	if derr == nil {
		derr = dir.Sync()
		_ = dir.Close()
	}
	if derr != nil {
		_ = os.Remove(final)
		return fmt.Errorf("take backup: sync folder: %w", derr)
	}
	return nil
}
