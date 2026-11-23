// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/store"
)

var (
	// ErrKeyMismatch means the backup was made with another secret key than
	// the one this server runs with, so its encrypted settings, two-factor
	// secrets and recovery codes could not be used after a restore.
	ErrKeyMismatch = errors.New("the backup was made with another secret key")
	// ErrNewerSchema means the backup was made by a newer version of
	// Scanopsis, whose database this version does not know.
	ErrNewerSchema = errors.New("the backup was made by a newer version of Scanopsis")
	// ErrCommitUncertain means the commit of the restore transaction
	// failed in a way that leaves open whether PostgreSQL committed it, for
	// example because the connection broke: the database may hold the
	// backup's data or the data from before.
	ErrCommitUncertain = errors.New("the database did not confirm the end of the restore, so it may or may not have been saved")
)

// actionRestored is the audit action of a finished restore.
const actionRestored = "backup.restored"

// hostSettingsPrefix starts the keys of the settings that belong to the
// server rather than to its data: the backup schedule and how long scheduled
// backups are kept.
const hostSettingsPrefix = "backup."

// The stages of a restore, in order. The test hook of RestoreOptions is
// called after each one.
const (
	stageVerified  = "verified" // the file was checked; nothing was written yet
	stageDropped   = "dropped"
	stageSchema    = "schema"
	stageLoaded    = "loaded"
	stageSequences = "sequences"
	stageMigrated  = "migrated"
	stageRecorded  = "recorded"
	stageSettings  = "settings"
	stageSessions  = "sessions"
	stageAudited   = "audited"
	stageAnalyzed  = "analyzed"
)

// restoreSettings apply to the transaction of a restore. Loading a large
// table takes far longer than the statement timeout of the pool, so the
// limit is lifted for this transaction. Dropping the tables waits for the
// server's own queries to finish with them, but not for ever: a lock that
// is not granted within 30 seconds fails the restore, which then changes
// nothing.
var restoreSettings = []string{
	`SET LOCAL statement_timeout = 0`,
	`SET LOCAL lock_timeout = '30s'`,
}

// RestoreOptions control Restore.
type RestoreOptions struct {
	// KeepHostSettings keeps this server's backup schedule and keep-days
	// settings (keys starting with "backup.") instead of taking those of the
	// backup. A restore into a running installation keeps them; a restore
	// that sets up a new installation takes them from the backup.
	KeepHostSettings bool
	// Actor is who started the restore, for the audit log.
	Actor string
	// KeyFingerprint is KeyFingerprint of the secret key this server runs
	// with. The backup must have been made with the same key.
	KeyFingerprint string
	// SigningKey is SigningKey of the same secret key. The backup must carry
	// a signature made with it (see ErrSignature).
	SigningKey []byte
	// Progress, if not nil, is called as the tables are loaded. It is
	// called from a goroutine of the database driver while that goroutine
	// streams the data to PostgreSQL, so it must return quickly and must
	// not block: store the values and let another goroutine report them.
	Progress func(Progress)

	// Verified, if not nil, is called once the whole file was checked and
	// before BeforeReplace. An error it returns stops the restore, which
	// then changes nothing. The restore job takes the backup of the current
	// data here, so that the data can change only for a moment between that
	// backup and the replacement.
	Verified func(ctx context.Context) error

	// BeforeReplace, if not nil, is called once the whole file was checked,
	// just before the transaction that replaces the data starts. It is not
	// called when the file is refused.
	BeforeReplace func()

	// testHook, if not nil, is called after every stage; an error it
	// returns fails the restore there.
	testHook func(stage string) error
	// testCommit, if not nil, commits instead of tx.Commit.
	testCommit func(ctx context.Context, tx pgx.Tx) error
}

// Preview describes what restoring a backup would do.
type Preview struct {
	Info Info `json:"info"`
	// SchemaOK is false when the backup was made by a newer version of
	// Scanopsis, which this version cannot restore.
	SchemaOK bool `json:"schema_ok"`
	// KeyMatches is false when the backup was made with another secret key,
	// which this server refuses to restore.
	KeyMatches bool `json:"key_matches"`
	// ScansRemovedByRetention is the number of scans in the backup that are
	// older than RetentionDays counted back from now: the next data
	// clean-up after the restore deletes them, apart from the latest scans
	// it always keeps.
	ScansRemovedByRetention int64 `json:"scans_removed_by_retention"`
	// OldestScanAt is the time of the oldest scan in the backup; nil when
	// it has none.
	OldestScanAt *time.Time `json:"oldest_scan_at"`
	// RetentionDays is the retention period the restored installation will
	// have: the backup's own setting when it has one, else the current one.
	RetentionDays int `json:"retention_days"`
}

// keysMatch reports whether a backup made with the key of fingerprint
// backup can be restored on a server whose key has fingerprint running. A
// missing fingerprint on either side never matches.
func keysMatch(backup, running string) bool {
	return backup != "" && running != "" && backup == running
}

// checkManifest refuses a backup that cannot be restored here.
func checkManifest(m Manifest, keyFingerprint string) error {
	if !keysMatch(m.KeyFingerprint, keyFingerprint) {
		return fmt.Errorf("%w and cannot be restored into this installation. To use it, set up a new installation with that secret key and restore the backup on its setup screen. Never change the secret key of an installation in use", ErrKeyMismatch)
	}
	if latest := store.LatestVersion(); m.Schema > latest {
		return fmt.Errorf("%w (%s, database schema %d; this version has schema %d): upgrade Scanopsis to that version or a newer one, then restore the backup", ErrNewerSchema, m.AppVersion, m.Schema, latest)
	}
	return checkKnownTables(m)
}

// checkKnownTables refuses a manifest without a schema version or with a
// table that a backup does not hold: a restore loads only Scanopsis tables.
func checkKnownTables(m Manifest) error {
	if m.Schema < 1 {
		return corruptf("the manifest has no database schema version")
	}
	apps := store.ApplicationTables()
	for _, t := range m.Tables {
		if !slices.Contains(apps, t.Name) || slices.Contains(Excluded, t.Name) {
			return corruptf("the manifest lists table %s, which is not part of a Scanopsis backup", t.Name)
		}
	}
	return nil
}

func sameTable(a, b TableSpec) bool { return a.Name == b.Name && slices.Equal(a.Columns, b.Columns) }

// sameManifest reports whether a and b describe the same backup.
func sameManifest(a, b Manifest) bool {
	return a.Format == b.Format && a.AppVersion == b.AppVersion && a.AppCommit == b.AppCommit &&
		a.Schema == b.Schema && a.CreatedAt.Equal(b.CreatedAt) && a.Kind == b.Kind &&
		a.CreatedBy == b.CreatedBy && a.KeyFingerprint == b.KeyFingerprint &&
		slices.EqualFunc(a.Tables, b.Tables, sameTable)
}

var errChanged = corruptf("the file changed while it was being restored")

// deflateMaxRatio is the most that deflate can expand its input: a stream
// of n bytes decompresses to at most about 1032 times n.
const deflateMaxRatio = 1032

// expansionLimit is the decompressed size a backup file of fileSize bytes
// can honestly have: deflate's largest expansion plus room for the headers,
// never more than DefaultMaxBytes. A file that expands further is a
// decompression bomb, and is stopped as soon as it passes the limit.
func expansionLimit(fileSize int64) int64 {
	const margin = 64 << 20
	if fileSize < 0 {
		fileSize = 0
	}
	if fileSize > (DefaultMaxBytes-margin)/deflateMaxRatio {
		return DefaultMaxBytes
	}
	return fileSize*deflateMaxRatio + margin
}

// ctxReader fails reads once ctx is done, so reading a large file stops
// when the caller gives up.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// Restore replaces the data of the database with the backup at path. It
// reads and checks the whole file first and refuses, before writing
// anything, a damaged file (ErrCorrupt and the other errors of Verify), a
// backup made with another secret key (ErrKeyMismatch, decided from the
// fingerprint in the manifest, which gives the clearer message), a file
// without a valid signature of this installation's key (ErrSignature: made
// elsewhere, or changed after it was made) and one made by a newer version
// (ErrNewerSchema).
//
// Once the file passed, Verified and then BeforeReplace are called. The
// restore then runs in
// one transaction: it drops the Scanopsis tables,
// creates them again at the schema version of the backup, loads every
// table with COPY, sets each sequence after the highest restored id,
// applies the migrations that are newer than the backup and records them
// as goose does, puts back this server's backup settings if asked to,
// deletes all sessions and writes a "backup.restored" entry to the restored
// audit log. Only then is it committed: on any failure, including a
// cancelled ctx, the transaction is rolled back and the database is
// exactly as it was. A failed commit is the exception: PostgreSQL may have
// committed before the error, so Restore returns ErrCommitUncertain.
//
// Restore depends only on the database and the file, not on the state of a
// running server; the caller stops other writers and reloads what it caches
// afterwards.
func Restore(ctx context.Context, pool *pgxpool.Pool, path string, o RestoreOptions) error {
	if o.Actor == "" {
		return errors.New("restore backup: no actor given")
	}
	if o.testHook == nil {
		o.testHook = func(string) error { return nil }
	}
	if len(o.SigningKey) == 0 {
		return fmt.Errorf("restore backup: %w", errNoKey)
	}
	f, err := os.Open(path) //nolint:gosec // the caller resolves path in the backup folder
	if err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}
	limit := expansionLimit(fi.Size())
	rewind := func() error {
		_, err := f.Seek(0, io.SeekStart)
		return err
	}

	// The manifest alone is enough to refuse a backup of another key or a
	// newer version, without reading a large file to the end first.
	m, err := ReadManifest(ctxReader{ctx, f})
	if err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}
	if err := checkManifest(m, o.KeyFingerprint); err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}
	if err := rewind(); err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}
	verified, _, err := VerifyLimited(ctxReader{ctx, f}, o.SigningKey, nil, limit)
	if err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}
	if !sameManifest(m, verified) {
		return fmt.Errorf("restore backup: %w", errChanged)
	}
	if err := o.testHook(stageVerified); err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}
	if o.Verified != nil {
		if err := o.Verified(ctx); err != nil {
			return fmt.Errorf("restore backup: %w", err)
		}
	}
	if err := rewind(); err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}
	if o.BeforeReplace != nil {
		o.BeforeReplace()
	}
	if err := restoreTx(ctx, pool, f, limit, filepath.Base(path), m, o); err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}
	return nil
}

func restoreTx(ctx context.Context, pool *pgxpool.Pool, f io.Reader, limit int64, name string, m Manifest, o RestoreOptions) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = tx.Rollback(rctx)
	}()
	if err := setLocal(ctx, tx, restoreSettings); err != nil {
		return err
	}

	var kept []store.Setting
	if o.KeepHostSettings {
		all, err := store.New(tx).ListSettings(ctx)
		if err != nil {
			return fmt.Errorf("read the settings of this server: %w", err)
		}
		for _, s := range all {
			if strings.HasPrefix(s.Key, hostSettingsPrefix) {
				kept = append(kept, s)
			}
		}
	}

	if err := store.DropApplicationTables(ctx, tx); err != nil {
		return err
	}
	if err := o.testHook(stageDropped); err != nil {
		return err
	}
	if err := store.ReplayMigrations(ctx, tx, 0, m.Schema); err != nil {
		return err
	}
	if err := sameTables(ctx, tx, m); err != nil {
		return err
	}
	if err := o.testHook(stageSchema); err != nil {
		return err
	}
	if err := load(ctx, tx, f, limit, o.SigningKey, m, o.Progress); err != nil {
		return err
	}
	if err := o.testHook(stageLoaded); err != nil {
		return err
	}
	if err := resetSequences(ctx, tx); err != nil {
		return err
	}
	if err := o.testHook(stageSequences); err != nil {
		return err
	}
	latest := store.LatestVersion()
	if err := store.ReplayMigrations(ctx, tx, m.Schema, latest); err != nil {
		return err
	}
	// The newer migrations may have added identity columns or rows.
	if err := resetSequences(ctx, tx); err != nil {
		return err
	}
	if err := o.testHook(stageMigrated); err != nil {
		return err
	}
	if err := store.RecordGooseVersions(ctx, tx, latest); err != nil {
		return err
	}
	if err := o.testHook(stageRecorded); err != nil {
		return err
	}
	if o.KeepHostSettings {
		if err := putSettings(ctx, tx, kept); err != nil {
			return err
		}
	}
	if err := o.testHook(stageSettings); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions`); err != nil {
		return fmt.Errorf("end all sessions: %w", err)
	}
	if err := o.testHook(stageSessions); err != nil {
		return err
	}
	detail, err := json.Marshal(map[string]any{
		"name": name, "created_at": m.CreatedAt.UTC().Format(time.RFC3339), "app_version": m.AppVersion, "actor": o.Actor,
	})
	if err != nil {
		return err
	}
	if err := store.New(tx).InsertAudit(ctx, store.InsertAuditParams{Actor: o.Actor, Action: actionRestored, Detail: detail}); err != nil {
		return fmt.Errorf("write the audit entry: %w", err)
	}
	if err := o.testHook(stageAudited); err != nil {
		return err
	}
	// Fresh tables have no statistics until autovacuum gets to them, and
	// the first queries after a large restore would be planned blind.
	if err := analyze(ctx, tx); err != nil {
		return err
	}
	if err := o.testHook(stageAnalyzed); err != nil {
		return err
	}
	commit := tx.Commit
	if o.testCommit != nil {
		commit = func(ctx context.Context) error { return o.testCommit(ctx, tx) }
	}
	if err := commit(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrCommitUncertain, err)
	}
	return nil
}

// sameTables checks, after the schema of the backup's version was created,
// that the backup holds exactly the tables and columns a backup of that
// version has, so a table cannot be left out and silently end up empty.
func sameTables(ctx context.Context, tx pgx.Tx, m Manifest) error {
	want, err := Tables(ctx, tx)
	if err != nil {
		return err
	}
	columns := func(t TableSpec) []string { return slices.Sorted(slices.Values(t.Columns)) }
	have := make(map[string][]string, len(m.Tables))
	for _, t := range m.Tables {
		have[t.Name] = columns(t)
	}
	for _, t := range want {
		cols, ok := have[t.Name]
		if !ok {
			return corruptf("table %s of database schema %d is missing from the backup", t.Name, m.Schema)
		}
		if !slices.Equal(cols, columns(t)) {
			return corruptf("the columns of table %s do not match database schema %d", t.Name, m.Schema)
		}
		delete(have, t.Name)
	}
	if len(have) > 0 {
		extra := slices.Sorted(maps.Keys(have))
		return corruptf("table %s is not part of database schema %d", extra[0], m.Schema)
	}
	return nil
}

// analyze gathers planner statistics for the restored tables.
func analyze(ctx context.Context, tx pgx.Tx) error {
	tables, err := Tables(ctx, tx)
	if err != nil {
		return err
	}
	var schema string
	if err := tx.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return err
	}
	names := make([]string, len(tables))
	for i, t := range tables {
		names[i] = pgx.Identifier{schema, t.Name}.Sanitize()
	}
	if len(names) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, "ANALYZE "+strings.Join(names, ", ")); err != nil {
		return fmt.Errorf("gather statistics: %w", err)
	}
	return nil
}

// load copies the tables of the backup in f into the tables of tx. The
// manifest read again from f must be m, the one that was checked.
func load(ctx context.Context, tx pgx.Tx, f io.Reader, limit int64, signingKey []byte, m Manifest, progress func(Progress)) error {
	var schema string
	if err := tx.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return err
	}
	pc := tx.Conn().PgConn()
	var done int
	var copied int64
	report := func(table string) {
		if progress != nil {
			progress(Progress{Table: table, TablesDone: done, TablesTotal: len(m.Tables), Bytes: copied})
		}
	}
	copyTable := func(t TableSpec, data io.Reader) (int64, error) {
		if done >= len(m.Tables) || !sameTable(t, m.Tables[done]) {
			return 0, errChanged
		}
		report(t.Name)
		cols := make([]string, len(t.Columns))
		for i, c := range t.Columns {
			cols[i] = pgx.Identifier{c}.Sanitize()
		}
		// COPY writes the stored values of identity columns, even of those
		// GENERATED ALWAYS, as pg_dump relies on.
		sql := fmt.Sprintf("COPY %s (%s) FROM STDIN (FORMAT csv)", pgx.Identifier{schema, t.Name}.Sanitize(), strings.Join(cols, ", "))
		src := &copySource{r: data, onRead: func(n int) {
			copied += int64(n)
			report(t.Name)
		}}
		tag, err := pc.CopyFrom(ctx, src, sql)
		if rerr := src.close(); rerr != nil {
			// The file failed, not the database: say so.
			return 0, rerr
		}
		if err != nil {
			return 0, fmt.Errorf("load table %s: %w", t.Name, err)
		}
		done++
		report(t.Name)
		return tag.RowsAffected(), nil
	}
	read, err := ReadTablesLimited(ctxReader{ctx, f}, signingKey, limit, copyTable)
	if err != nil {
		return err
	}
	if !sameManifest(m, read) || done != len(m.Tables) {
		return errChanged
	}
	return nil
}

// copySource is the reader COPY takes the data of one table from. pgconn
// reads it on a goroutine of its own, which may still be running when
// CopyFrom has returned after a broken connection; close waits for a read
// in progress and stops any further one, so the archive is never read by
// two goroutines. It also keeps the first error of the archive, which COPY
// would report only as its own failure.
type copySource struct {
	mu     sync.Mutex
	r      io.Reader
	onRead func(n int)
	closed bool
	err    error
}

var errSourceClosed = errors.New("the copy has ended")

func (s *copySource) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, errSourceClosed
	}
	n, err := s.r.Read(p)
	if n > 0 && s.onRead != nil {
		s.onRead(n)
	}
	if err != nil && !errors.Is(err, io.EOF) && s.err == nil {
		s.err = err
	}
	return n, err
}

// close ends reading and returns the error the archive had, if any.
func (s *copySource) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return s.err
}

// resetSequences sets the sequence of every identity and serial column of
// the Scanopsis tables after the highest value in the column, so new rows
// get new ids; the sequence of an empty column starts again at its start
// value.
func resetSequences(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `
SELECT c.relname, a.attname, pg_get_serial_sequence(format('%I.%I', n.nspname, c.relname), a.attname)
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid
WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p') AND c.relname = ANY($1)
  AND a.attnum > 0 AND NOT a.attisdropped
  AND pg_get_serial_sequence(format('%I.%I', n.nspname, c.relname), a.attname) IS NOT NULL
ORDER BY 1, 2`, store.ApplicationTables())
	if err != nil {
		return fmt.Errorf("list sequences: %w", err)
	}
	type owned struct{ Table, Column, Sequence string }
	seqs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[owned])
	if err != nil {
		return fmt.Errorf("list sequences: %w", err)
	}
	for _, s := range seqs {
		q := fmt.Sprintf(`
SELECT setval($1::text::regclass, greatest(m.v, q.seqstart), coalesce(m.v >= q.seqstart, false))
FROM (SELECT max(%s) AS v FROM %s) m, pg_sequence q
WHERE q.seqrelid = $1::text::regclass`, pgx.Identifier{s.Column}.Sanitize(), pgx.Identifier{s.Table}.Sanitize())
		if _, err := tx.Exec(ctx, q, s.Sequence); err != nil {
			return fmt.Errorf("reset the sequence of %s.%s: %w", s.Table, s.Column, err)
		}
	}
	return nil
}

// putSettings replaces the backup settings of the restored data with kept,
// as they were on this server.
func putSettings(ctx context.Context, tx pgx.Tx, kept []store.Setting) error {
	if _, err := tx.Exec(ctx, `DELETE FROM settings WHERE starts_with(key, $1)`, hostSettingsPrefix); err != nil {
		return fmt.Errorf("keep the backup settings of this server: %w", err)
	}
	for _, s := range kept {
		if _, err := tx.Exec(ctx, `INSERT INTO settings (key, value, secret_enc, source, updated_by, updated_at) VALUES ($1, $2, $3, $4, $5, $6)`,
			s.Key, s.Value, s.SecretEnc, s.Source, s.UpdatedBy, s.UpdatedAt); err != nil {
			return fmt.Errorf("keep setting %s of this server: %w", s.Key, err)
		}
	}
	return nil
}

// infoOf describes the backup file called name from its manifest.
func infoOf(name string, size int64, m Manifest, keyFingerprint string) Info {
	kind := m.Kind
	if sub := fileName.FindStringSubmatch(name); sub != nil {
		kind = Kind(sub[2])
	}
	return Info{
		Name: name, Kind: kind, Size: size, CreatedAt: m.CreatedAt, CreatedBy: m.CreatedBy,
		AppVersion: m.AppVersion, Schema: m.Schema, KeyMatches: keysMatch(m.KeyFingerprint, keyFingerprint),
		Readable: true, NewerVersion: m.Schema > store.LatestVersion(),
	}
}

// PreviewRestore tells what restoring the backup at path would do: whether
// it is possible (SchemaOK, KeyMatches) and how much of the restored history
// the next data clean-up removes. retentionDays is the retention period of
// this server, used when the backup has no setting of its own, and now is
// the time the clean-up counts back from.
//
// A backup that cannot be restored here, because a newer version or another
// secret key made it, is described from its manifest alone, with SchemaOK or
// KeyMatches false and no error: its layout may be one this version does not
// know, and its signature cannot be checked. Any other backup is read and
// checked in full, as Restore does before it writes anything; a damaged file
// or one without a valid signature of signingKey (ErrSignature) is an error.
func PreviewRestore(ctx context.Context, path string, keyFingerprint string, signingKey []byte, retentionDays int, now time.Time) (Preview, error) {
	f, err := os.Open(path) //nolint:gosec // the caller resolves path in the backup folder
	if err != nil {
		return Preview{}, fmt.Errorf("check backup: %w", err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return Preview{}, fmt.Errorf("check backup: %w", err)
	}
	m, err := ReadManifest(ctxReader{ctx, f})
	if err != nil {
		return Preview{}, fmt.Errorf("check backup: %w", err)
	}
	p := Preview{
		Info:          infoOf(filepath.Base(path), fi.Size(), m, keyFingerprint),
		SchemaOK:      m.Schema <= store.LatestVersion(),
		KeyMatches:    keysMatch(m.KeyFingerprint, keyFingerprint),
		RetentionDays: effectiveRetention(retentionDays),
	}
	if !p.SchemaOK || !p.KeyMatches {
		return p, nil
	}
	if err := checkKnownTables(m); err != nil {
		return Preview{}, fmt.Errorf("check backup: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Preview{}, fmt.Errorf("check backup: %w", err)
	}

	var ages retentionCount
	backupDays, haveDays := 0, false
	scan := func(t TableSpec, data io.Reader) (int64, error) {
		c := rowCounter{}
		switch t.Name {
		case "scans":
			at := slices.Index(t.Columns, "scanned_at")
			if at < 0 {
				return 0, corruptf("table scans has no scanned_at column")
			}
			c.cols = []int{at}
			c.row = func(v [][]byte) error {
				ts, err := parseTimestamp(string(v[0]))
				if err != nil {
					return corruptf("a scan time cannot be read: %v", err)
				}
				ages.add(now, ts)
				return nil
			}
		case "settings":
			key, value := slices.Index(t.Columns, "key"), slices.Index(t.Columns, "value")
			if key < 0 || value < 0 {
				return 0, corruptf("table settings has no key or value column")
			}
			c.cols = []int{key, value}
			c.row = func(v [][]byte) error {
				var days int
				if string(v[0]) == settings.KeyRetentionDays && json.Unmarshal(v[1], &days) == nil {
					backupDays, haveDays = days, true
				}
				return nil
			}
		}
		_, err := io.Copy(&c, data)
		if err == nil {
			err = c.finish()
		}
		return c.rows, err
	}
	read, err := ReadTablesLimited(ctxReader{ctx, f}, signingKey, expansionLimit(fi.Size()), scan)
	if err != nil {
		return Preview{}, fmt.Errorf("check backup: %w", err)
	}
	if !sameManifest(m, read) {
		return Preview{}, fmt.Errorf("check backup: %w", corruptf("the file changed while it was being read"))
	}

	days := retentionDays
	if haveDays {
		days = backupDays
	}
	p.RetentionDays = effectiveRetention(days)
	p.ScansRemovedByRetention = ages.removed(days)
	if ages.seen {
		oldest := ages.oldest
		p.OldestScanAt = &oldest
	}
	return p, nil
}

// effectiveRetention is the retention period the data clean-up uses for a
// setting of days: kept within the allowed range, as the clean-up does.
func effectiveRetention(days int) int {
	return min(max(days, settings.MinRetentionDays), settings.MaxRetentionDays)
}

// retentionCount counts scans by age in whole days, enough to tell for any
// retention period how many are older than it, in a fixed amount of memory.
type retentionCount struct {
	// byAge[k] counts the scans whose age is more than k days and at most
	// k+1 days; the last element also holds every older scan.
	byAge  [settings.MaxRetentionDays + 1]int64
	oldest time.Time
	seen   bool
}

func (c *retentionCount) add(now, at time.Time) {
	if !c.seen || at.Before(c.oldest) {
		c.oldest, c.seen = at, true
	}
	age := now.Sub(at) // saturates for times centuries away
	if age <= 0 {
		return
	}
	k := int64((age - 1) / (24 * time.Hour))
	c.byAge[min(k, int64(len(c.byAge)-1))]++
}

// removed returns the number of scans older than the retention period of
// days, as the data clean-up decides: a scan expires when its time is
// before now minus the period.
func (c *retentionCount) removed(days int) int64 {
	var n int64
	for _, v := range c.byAge[effectiveRetention(days):] {
		n += v
	}
	return n
}

// rowCounter counts the records of PostgreSQL CSV data written to it. With
// cols set, it also collects the fields at those positions and calls row
// with them at the end of every record. Collected fields are limited to
// maxField bytes, so a crafted file cannot make it hold much in memory;
// other fields are not kept at all.
type rowCounter struct {
	rows int64
	cols []int
	row  func(fields [][]byte) error

	inQuotes bool // counting only

	state  csvState
	col    int
	fields [][]byte
	err    error
}

type csvState int

const (
	fieldStart  csvState = iota
	unquoted             // in a field without quotes
	quoted               // in a quoted field
	quoteQuoted          // after a quote in a quoted field: its end, or a doubled quote
)

const maxField = 1 << 20

func (c *rowCounter) Write(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	if c.cols == nil {
		// Quotes inside a quoted field are doubled, so a new line ends a
		// record whenever the quotes so far are balanced.
		for _, b := range p {
			switch {
			case b == '"':
				c.inQuotes = !c.inQuotes
			case b == '\n' && !c.inQuotes:
				c.rows++
			}
		}
		return len(p), nil
	}
	if c.fields == nil {
		c.fields = make([][]byte, len(c.cols))
	}
	for _, b := range p {
		if err := c.next(b); err != nil {
			c.err = err
			return 0, err
		}
	}
	return len(p), nil
}

func (c *rowCounter) next(b byte) error {
	switch c.state {
	case fieldStart:
		if b == '"' {
			c.state = quoted
			return nil
		}
		c.state = unquoted
		return c.plain(b)
	case unquoted:
		return c.plain(b)
	case quoted:
		if b == '"' {
			c.state = quoteQuoted
			return nil
		}
		return c.keep(b)
	default: // quoteQuoted
		if b == '"' {
			c.state = quoted
			return c.keep(b)
		}
		return c.plain(b)
	}
}

// plain handles a byte outside quotes.
func (c *rowCounter) plain(b byte) error {
	switch b {
	case ',':
		c.col++
		c.state = fieldStart
	case '\n':
		return c.endRecord()
	default:
		if c.state == quoteQuoted {
			return corruptf("a quoted value is followed by more text")
		}
		return c.keep(b)
	}
	return nil
}

func (c *rowCounter) keep(b byte) error {
	i := slices.Index(c.cols, c.col)
	if i < 0 {
		return nil
	}
	if len(c.fields[i]) >= maxField {
		return corruptf("a value is longer than a backup holds")
	}
	c.fields[i] = append(c.fields[i], b)
	return nil
}

func (c *rowCounter) endRecord() error {
	c.rows++
	if c.row != nil {
		if err := c.row(c.fields); err != nil {
			return err
		}
	}
	for i := range c.fields {
		c.fields[i] = c.fields[i][:0]
	}
	c.col, c.state = 0, fieldStart
	return nil
}

// finish reports data that ends inside a record.
func (c *rowCounter) finish() error {
	if c.err != nil {
		return c.err
	}
	if c.cols != nil && (c.col != 0 || c.state != fieldStart) {
		return corruptf("the last record is incomplete")
	}
	return nil
}

var (
	// farPast and farFuture stand for times that time.Time cannot parse
	// from PostgreSQL's text: -infinity and dates before the common era,
	// infinity and years after 9999.
	farPast   = time.Date(-4713, 1, 1, 0, 0, 0, 0, time.UTC)
	farFuture = time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC)

	timestampLayouts = []string{
		"2006-01-02 15:04:05.999999999-07",
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999-07:00:00",
	}
)

// parseTimestamp reads a timestamptz as PostgreSQL writes it with DateStyle
// ISO, in any time zone.
func parseTimestamp(s string) (time.Time, error) {
	switch s {
	case "infinity":
		return farFuture, nil
	case "-infinity":
		return farPast, nil
	}
	if rest, bc := strings.CutSuffix(s, " BC"); bc {
		if _, err := parseTimestamp(rest); err != nil {
			return time.Time{}, err
		}
		return farPast, nil
	}
	if i := strings.IndexByte(s, '-'); i > 4 && strings.Trim(s[:i], "0123456789") == "" {
		if _, err := parseTimestamp("2000" + s[i:]); err != nil {
			return time.Time{}, err
		}
		return farFuture, nil
	}
	for _, l := range timestampLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a time", s)
}
