// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"archive/tar"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"regexp"
	"time"
)

// FormatVersion is the version of the file layout described in the package
// documentation.
const FormatVersion = 1

// ChunkSize is the largest size of one tables/<name>/<number>.csv entry.
const ChunkSize = 8 << 20

// DefaultMaxBytes is the ceiling Verify and ReadTables put on the amount of
// data a backup may expand to (all bytes of the decompressed archive). It is
// generous for real data but finite, so a small file that expands to
// terabytes (a decompression bomb) is stopped.
const DefaultMaxBytes int64 = 1 << 40

// NoLimit, passed to VerifyLimited or ReadTablesLimited, removes the ceiling.
// Use it only for input that is trusted.
const NoLimit int64 = math.MaxInt64

// defaultLimit is DefaultMaxBytes; tests lower it.
var defaultLimit = DefaultMaxBytes

const (
	manifestName  = "manifest.json"
	trailerName   = "checksums.json"
	signatureName = "signature"

	maxManifestSize = 1 << 20 // the manifest is read into memory
	maxTrailerSize  = 4 << 20 // so is the trailer
	maxTrailingData = 1 << 20 // padding allowed after the tar end marker
	maxChunks       = 999999  // the sequence number has six digits
	maxColumns      = 1000    // columns listed for one table
	maxTables       = 1000    // tables listed in a manifest

	errTooBig = "the file expands to more data than a backup of this size can hold"
)

// Kind says why a backup was made.
type Kind string

// The reasons a backup exists.
const (
	KindManual        Kind = "manual"
	KindScheduled     Kind = "scheduled"
	KindUploaded      Kind = "uploaded"
	KindBeforeRestore Kind = "before-restore"
)

// Errors returned by the readers. They are wrapped with a reason that is
// written for a person, so use errors.Is to test for them and err.Error() to
// show them.
var (
	// ErrNotBackup means the input is not a Scanopsis backup at all.
	ErrNotBackup = errors.New("the file is not a Scanopsis backup")
	// ErrCorrupt means the input is a backup that is incomplete or changed.
	ErrCorrupt = errors.New("the file is damaged")
	// ErrFormat means the backup uses a layout this version cannot read.
	ErrFormat = errors.New("the backup format is not supported")
	// ErrSignature means the signature of the backup is missing or does
	// not match the signing key: the file was made with another secret key,
	// or someone changed it after it was made.
	ErrSignature = errors.New("the backup was not made by this installation's secret key, or it was changed after it was made")
)

// formatError is an ErrFormat that carries the offending version.
type formatError struct{ got int }

func (e *formatError) Error() string {
	return fmt.Sprintf("the backup was made with format %d; this version reads format %d", e.got, FormatVersion)
}

func (e *formatError) Is(target error) bool { return target == ErrFormat }

// TableSpec names a table and the columns saved for it.
type TableSpec struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
}

// Manifest describes a backup. It is the first entry of the file.
type Manifest struct {
	Format         int         `json:"format"`
	AppVersion     string      `json:"app_version"`
	AppCommit      string      `json:"app_commit"`
	Schema         int64       `json:"schema"`
	CreatedAt      time.Time   `json:"created_at"`
	Kind           Kind        `json:"kind"`
	CreatedBy      string      `json:"created_by"`
	KeyFingerprint string      `json:"key_fingerprint"`
	Tables         []TableSpec `json:"tables"`
}

// TableSum is what the trailer records about one table.
type TableSum struct {
	Rows   int64  `json:"rows"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Trailer is the last entry of the file: one sum per table.
type Trailer struct {
	Tables map[string]TableSum `json:"tables"`
}

// Progress tells how far WriteArchive has come.
type Progress struct {
	Table       string `json:"table"`
	TablesDone  int    `json:"tables_done"`
	TablesTotal int    `json:"tables_total"`
	// Bytes is the amount of table data written so far, for all tables.
	Bytes int64 `json:"bytes"`
}

// TableCopier writes the CSV data of one table to w and returns the number of
// rows it wrote.
type TableCopier func(ctx context.Context, t TableSpec, w io.Writer) (rows int64, err error)

var tableName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// checkTables validates the table list of a manifest: plain unique table
// names, so they can safely become part of an entry name, and plain unique
// column names, since a restore puts them into a COPY statement.
func checkTables(tables []TableSpec) error {
	if len(tables) > maxTables {
		return fmt.Errorf("the manifest lists too many tables (%d)", len(tables))
	}
	seen := make(map[string]bool, len(tables))
	for _, t := range tables {
		if !tableName.MatchString(t.Name) {
			return fmt.Errorf("table name %q is not allowed", t.Name)
		}
		if seen[t.Name] {
			return fmt.Errorf("table %s is listed twice", t.Name)
		}
		seen[t.Name] = true
		if len(t.Columns) == 0 {
			return fmt.Errorf("table %s has no columns", t.Name)
		}
		if len(t.Columns) > maxColumns {
			return fmt.Errorf("table %s has too many columns", t.Name)
		}
		cols := make(map[string]bool, len(t.Columns))
		for _, c := range t.Columns {
			if !tableName.MatchString(c) {
				return fmt.Errorf("column name %q of table %s is not allowed", c, t.Name)
			}
			if cols[c] {
				return fmt.Errorf("column %s of table %s is listed twice", c, t.Name)
			}
			cols[c] = true
		}
	}
	return nil
}

func chunkName(table string, seq int) string {
	return fmt.Sprintf("tables/%s/%06d.csv", table, seq)
}

// WriteArchive writes a backup to w. It writes the manifest, then calls
// copyTable for each table of m in order, then the trailer, which it also
// returns, and finally the signature made with signingKey (see SigningKey).
// At most ChunkSize bytes of table data are held in memory. progress may be
// nil.
func WriteArchive(ctx context.Context, w io.Writer, m Manifest, signingKey []byte, copyTable TableCopier, progress func(Progress)) (Trailer, error) {
	if len(signingKey) == 0 {
		return Trailer{}, errNoKey
	}
	m.Format = FormatVersion
	if err := checkTables(m.Tables); err != nil {
		return Trailer{}, fmt.Errorf("write backup: %w", err)
	}
	gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
	if err != nil {
		return Trailer{}, fmt.Errorf("write backup: %w", err)
	}
	aw := &archiveWriter{ctx: ctx, tw: tar.NewWriter(gz), mod: m.CreatedAt, total: len(m.Tables), progress: progress}

	manifestBody, err := json.Marshal(m)
	if err != nil {
		return Trailer{}, fmt.Errorf("write backup: encode manifest: %w", err)
	}
	if err := aw.entry(manifestName, manifestBody); err != nil {
		return Trailer{}, err
	}

	aw.buf = make([]byte, 0, ChunkSize)
	trailer := Trailer{Tables: make(map[string]TableSum, len(m.Tables))}
	for i, t := range m.Tables {
		if err := ctx.Err(); err != nil {
			return Trailer{}, err
		}
		aw.start(t.Name, i)
		rows, err := copyTable(ctx, t, aw)
		if err != nil {
			return Trailer{}, fmt.Errorf("write backup: copy table %s: %w", t.Name, err)
		}
		if err := aw.finish(); err != nil {
			return Trailer{}, err
		}
		trailer.Tables[t.Name] = TableSum{Rows: rows, Bytes: aw.tableBytes, SHA256: hex.EncodeToString(aw.sum.Sum(nil))}
		aw.done = i + 1
		aw.allBytes += aw.tableBytes
		aw.tableBytes = 0
		aw.report()
	}

	trailerBody, err := json.Marshal(trailer)
	if err != nil {
		return Trailer{}, fmt.Errorf("write backup: encode checksums: %w", err)
	}
	if err := aw.entry(trailerName, trailerBody); err != nil {
		return Trailer{}, err
	}
	sig := hex.EncodeToString(sign(signingKey, manifestBody, trailerBody))
	if err := aw.entry(signatureName, []byte(sig)); err != nil {
		return Trailer{}, err
	}
	if err := aw.tw.Close(); err != nil {
		return Trailer{}, fmt.Errorf("write backup: %w", err)
	}
	if err := gz.Close(); err != nil {
		return Trailer{}, fmt.Errorf("write backup: %w", err)
	}
	return trailer, nil
}

// archiveWriter cuts the data of one table at a time into chunk entries. It is
// the io.Writer handed to the TableCopier.
type archiveWriter struct {
	ctx      context.Context
	tw       *tar.Writer
	mod      time.Time
	progress func(Progress)

	buf        []byte // the one reusable chunk buffer
	table      string
	seq        int // chunks written for the current table
	sum        hash.Hash
	tableBytes int64
	allBytes   int64 // bytes of finished tables
	done       int
	total      int
}

func (a *archiveWriter) header(name string, size int) *tar.Header {
	return &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(size), ModTime: a.mod, Format: tar.FormatUSTAR}
}

func (a *archiveWriter) entry(name string, body []byte) error {
	if err := a.tw.WriteHeader(a.header(name, len(body))); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}
	if _, err := a.tw.Write(body); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}
	return nil
}

func (a *archiveWriter) start(table string, i int) {
	a.table, a.seq, a.tableBytes, a.done = table, 0, 0, i
	a.sum = sha256.New()
	a.buf = a.buf[:0]
}

// Write implements io.Writer for the copier.
func (a *archiveWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		if len(a.buf) == cap(a.buf) {
			if err := a.flush(); err != nil {
				return n - len(p), err
			}
		}
		k := copy(a.buf[len(a.buf):cap(a.buf)], p)
		a.buf = a.buf[:len(a.buf)+k]
		p = p[k:]
	}
	return n, nil
}

// flush writes the buffer as the next chunk entry.
func (a *archiveWriter) flush() error {
	if err := a.ctx.Err(); err != nil {
		return err
	}
	if a.seq >= maxChunks {
		return fmt.Errorf("write backup: table %s has too many chunks", a.table)
	}
	a.seq++
	if err := a.entry(chunkName(a.table, a.seq), a.buf); err != nil {
		return err
	}
	a.sum.Write(a.buf)
	a.tableBytes += int64(len(a.buf))
	a.buf = a.buf[:0]
	a.report()
	return nil
}

// finish writes what is left in the buffer. A table with no data still gets
// one empty chunk, so every table is present in the file.
func (a *archiveWriter) finish() error {
	if len(a.buf) > 0 || a.seq == 0 {
		if err := a.flush(); err != nil {
			return err
		}
	}
	return nil
}

func (a *archiveWriter) report() {
	if a.progress != nil {
		a.progress(Progress{Table: a.table, TablesDone: a.done, TablesTotal: a.total, Bytes: a.allBytes + a.tableBytes})
	}
}

// damaged wraps a read error as ErrCorrupt with a fixed sentence for a
// person. The text of the library error is not passed on. A context that
// ended is not damage and is returned as it is.
func damaged(err error) error {
	switch {
	case errors.Is(err, ErrCorrupt), errors.Is(err, ErrNotBackup), errors.Is(err, ErrFormat),
		errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Errorf("%w: the file ends too early", ErrCorrupt)
	case errors.Is(err, gzip.ErrChecksum):
		return fmt.Errorf("%w: the compressed data does not match its checksum", ErrCorrupt)
	case errors.Is(err, gzip.ErrHeader):
		return fmt.Errorf("%w: the compression header is wrong", ErrCorrupt)
	case errors.Is(err, tar.ErrHeader):
		return fmt.Errorf("%w: the archive structure is wrong", ErrCorrupt)
	}
	var flateErr flate.CorruptInputError
	if errors.As(err, &flateErr) {
		return fmt.Errorf("%w: the compressed data is broken", ErrCorrupt)
	}
	return fmt.Errorf("%w: the file could not be read", ErrCorrupt)
}

func corruptf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrCorrupt, fmt.Sprintf(format, args...))
}

// archiveReader walks the entries of a backup with one entry of look-ahead.
type archiveReader struct {
	src     *limitReader // the decompressed stream; everything read passes here
	tr      *tar.Reader
	pending *tar.Header

	// The exact bytes of the manifest and the trailer, which the signature
	// covers.
	manifestBody, trailerBody []byte
}

// limitReader counts every byte taken from the decompressed stream and fails
// once more than max have been read.
type limitReader struct {
	r   io.Reader
	n   int64
	max int64
}

func (l *limitReader) Read(p []byte) (int, error) {
	if l.n > l.max {
		return 0, corruptf("%s", errTooBig)
	}
	if left := l.max - l.n; left < math.MaxInt64 && int64(len(p)) > left+1 {
		p = p[:left+1] // read one byte past the limit to notice it
	}
	n, err := l.r.Read(p)
	l.n += int64(n)
	if l.n > l.max {
		return n, corruptf("%s", errTooBig)
	}
	return n, err
}

func openArchive(r io.Reader, maxBytes int64) (*archiveReader, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		if errors.Is(err, gzip.ErrHeader) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("%w: it is not a gzip file", ErrNotBackup)
		}
		return nil, damaged(err)
	}
	src := &limitReader{r: gz, max: maxBytes}
	return &archiveReader{src: src, tr: tar.NewReader(src)}, nil
}

// next returns the next entry header, or io.EOF at the end of the archive.
// Entries other than plain files are refused.
func (a *archiveReader) next() (*tar.Header, error) {
	if h := a.pending; h != nil {
		a.pending = nil
		return h, nil
	}
	h, err := a.tr.Next()
	if errors.Is(err, io.EOF) {
		return nil, io.EOF
	}
	if err != nil {
		return nil, damaged(err)
	}
	if h.Typeflag != tar.TypeReg {
		return nil, corruptf("entry %q is not a plain file", h.Name)
	}
	return h, nil
}

func (a *archiveReader) unread(h *tar.Header) { a.pending = h }

// readBody reads the whole body of the entry h, which must not exceed limit.
func (a *archiveReader) readBody(h *tar.Header, limit int64) ([]byte, error) {
	if h.Size > limit {
		return nil, corruptf("entry %s is too large", h.Name)
	}
	b, err := io.ReadAll(io.LimitReader(a.tr, h.Size))
	if err != nil {
		return nil, damaged(err)
	}
	if int64(len(b)) != h.Size {
		return nil, damaged(io.ErrUnexpectedEOF)
	}
	return b, nil
}

// manifest reads and checks the first entry.
func (a *archiveReader) manifest() (Manifest, error) {
	h, err := a.tr.Next()
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, tar.ErrHeader) {
			return Manifest{}, fmt.Errorf("%w: it has no manifest", ErrNotBackup)
		}
		return Manifest{}, damaged(err)
	}
	if h.Name != manifestName || h.Typeflag != tar.TypeReg {
		return Manifest{}, fmt.Errorf("%w: it has no manifest", ErrNotBackup)
	}
	if h.Size > maxManifestSize {
		return Manifest{}, fmt.Errorf("%w: the manifest is too large", ErrNotBackup)
	}
	b, err := a.readBody(h, maxManifestSize)
	if err != nil {
		return Manifest{}, err
	}
	a.manifestBody = b
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("%w: the manifest cannot be read", ErrNotBackup)
	}
	if m.Format != FormatVersion {
		if m.Format <= 0 {
			return Manifest{}, fmt.Errorf("%w: the manifest has no format", ErrNotBackup)
		}
		return Manifest{}, &formatError{got: m.Format}
	}
	if err := checkTables(m.Tables); err != nil {
		return Manifest{}, corruptf("the manifest is wrong: %v", err)
	}
	return m, nil
}

// ReadManifest reads only the manifest, the first entry of a backup.
func ReadManifest(r io.Reader) (Manifest, error) {
	a, err := openArchive(r, DefaultMaxBytes)
	if err != nil {
		return Manifest{}, err
	}
	return a.manifest()
}

// tableReader streams all chunks of one table as a single reader and keeps
// the checksum and size of what it passed on.
type tableReader struct {
	a         *archiveReader
	name      string
	seq       int   // chunks entered so far
	remaining int64 // bytes left in the current chunk
	finished  bool
	sum       hash.Hash
	bytes     int64
	prevSize  int64 // size of the previous chunk
	err       error
	budget    *budget
}

// budget counts the table bytes read over all tables for the progress report.
type budget struct {
	total    int64
	progress func(int64)
}

func (t *tableReader) fail(err error) (int, error) {
	t.err = err
	return 0, err
}

// nextChunk moves to the next chunk of this table, or sets finished.
func (t *tableReader) nextChunk() error {
	if t.seq >= maxChunks {
		return corruptf("table %s has too many chunks", t.name)
	}
	h, err := t.a.next()
	if errors.Is(err, io.EOF) {
		if t.seq == 0 {
			return corruptf("table %s is missing", t.name)
		}
		t.finished = true
		return nil
	}
	if err != nil {
		return err
	}
	if h.Name != chunkName(t.name, t.seq+1) {
		if t.seq == 0 {
			return corruptf("table %s is missing or out of order (found %q)", t.name, h.Name)
		}
		t.a.unread(h)
		t.finished = true
		return nil
	}
	if h.Size > ChunkSize {
		return corruptf("entry %s is larger than the chunk size", h.Name)
	}
	if t.seq > 0 && (t.prevSize != ChunkSize || h.Size == 0) {
		return corruptf("table %s is not cut into chunks the way a backup is", t.name)
	}
	t.seq++
	t.prevSize = h.Size
	t.remaining = h.Size
	return nil
}

func (t *tableReader) Read(p []byte) (int, error) {
	if t.err != nil {
		return 0, t.err
	}
	for t.remaining == 0 {
		if t.finished {
			return 0, io.EOF
		}
		if err := t.nextChunk(); err != nil {
			return t.fail(err)
		}
	}
	if int64(len(p)) > t.remaining {
		p = p[:t.remaining]
	}
	n, err := t.a.tr.Read(p)
	if errors.Is(err, io.EOF) {
		err = nil
		if n == 0 {
			err = io.ErrUnexpectedEOF
		}
	}
	t.remaining -= int64(n)
	t.sum.Write(p[:n])
	t.bytes += int64(n)
	if b := t.budget; b != nil {
		b.total += int64(n)
		if b.progress != nil {
			b.progress(b.total)
		}
	}
	if err != nil {
		t.err = damaged(err)
		return n, t.err
	}
	return n, nil
}

// readAll walks a whole backup. With load nil it only checks the file; with
// load it hands each table's data to load first. It returns the trailer
// stored in the file after comparing it with what was read and checking the
// signature with signingKey.
func readAll(r io.Reader, signingKey []byte, progress func(int64), maxBytes int64, load func(TableSpec, io.Reader) (int64, error)) (Manifest, Trailer, error) {
	if len(signingKey) == 0 {
		return Manifest{}, Trailer{}, errNoKey
	}
	a, err := openArchive(r, maxBytes)
	if err != nil {
		return Manifest{}, Trailer{}, err
	}
	m, err := a.manifest()
	if err != nil {
		return Manifest{}, Trailer{}, err
	}

	bud := &budget{progress: progress}
	got := make(map[string]TableSum, len(m.Tables))
	for _, t := range m.Tables {
		tr := &tableReader{a: a, name: t.Name, sum: sha256.New(), budget: bud}
		var rows int64
		if load != nil {
			rows, err = load(t, tr)
			if err != nil {
				return m, Trailer{}, err
			}
		}
		// Whatever the loader left unread still counts towards the sums.
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return m, Trailer{}, err
		}
		got[t.Name] = TableSum{Rows: rows, Bytes: tr.bytes, SHA256: hex.EncodeToString(tr.sum.Sum(nil))}
	}

	trailer, err := a.trailer()
	if err != nil {
		return m, Trailer{}, err
	}
	// The sums are stored after the data, so they can only be compared here;
	// ReadTables callers have not committed anything before this returns.
	if len(trailer.Tables) != len(m.Tables) {
		return m, Trailer{}, corruptf("the checksums do not match the list of tables")
	}
	for _, t := range m.Tables {
		want, ok := trailer.Tables[t.Name]
		if !ok {
			return m, Trailer{}, corruptf("checksum of table %s is missing", t.Name)
		}
		have := got[t.Name]
		switch {
		case have.SHA256 != want.SHA256:
			return m, Trailer{}, corruptf("checksum of table %s does not match", t.Name)
		case have.Bytes != want.Bytes:
			return m, Trailer{}, corruptf("size of table %s does not match", t.Name)
		case load != nil && have.Rows != want.Rows:
			return m, Trailer{}, corruptf("row count of table %s does not match (%d loaded, %d expected)", t.Name, have.Rows, want.Rows)
		}
	}
	if err := a.signature(signingKey); err != nil {
		return m, Trailer{}, err
	}
	if err := a.end(); err != nil {
		return m, Trailer{}, err
	}
	return m, trailer, nil
}

// signature reads the signature, which must be the next entry, and checks it
// against the manifest and the trailer.
func (a *archiveReader) signature(signingKey []byte) error {
	h, err := a.next()
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("%w (it has no signature)", ErrSignature)
	}
	if err != nil {
		return err
	}
	if h.Name != signatureName {
		return corruptf("unexpected entry %q", h.Name)
	}
	b, err := a.readBody(h, 2*sha256.Size)
	if err != nil {
		return err
	}
	got, err := hex.DecodeString(string(b))
	if err != nil || !hmac.Equal(got, sign(signingKey, a.manifestBody, a.trailerBody)) {
		return ErrSignature
	}
	return nil
}

// trailer reads checksums.json, which must be the next entry.
func (a *archiveReader) trailer() (Trailer, error) {
	h, err := a.next()
	if errors.Is(err, io.EOF) {
		return Trailer{}, corruptf("the checksums are missing")
	}
	if err != nil {
		return Trailer{}, err
	}
	if h.Name != trailerName {
		return Trailer{}, corruptf("unexpected entry %q", h.Name)
	}
	b, err := a.readBody(h, maxTrailerSize)
	if err != nil {
		return Trailer{}, err
	}
	a.trailerBody = b
	var tr Trailer
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&tr); err != nil {
		return Trailer{}, corruptf("the checksums cannot be read")
	}
	return tr, nil
}

// end checks that nothing follows the signature.
func (a *archiveReader) end() error {
	if _, err := a.next(); !errors.Is(err, io.EOF) {
		if err == nil {
			return corruptf("there is an extra entry after the signature")
		}
		return err
	}
	n, err := io.Copy(io.Discard, io.LimitReader(a.src, maxTrailingData+1))
	if err != nil {
		return damaged(err)
	}
	if n > maxTrailingData {
		return corruptf("there is extra data after the end of the archive")
	}
	return nil
}

// Verify reads a whole backup and checks its order, entry names, checksums,
// sizes, format version and signature (with signingKey, see SigningKey; a
// missing or wrong signature is ErrSignature). progress, if not nil, is
// called with the number of table bytes read so far. Row counts are not
// checked, since that needs the data to be parsed.
func Verify(r io.Reader, signingKey []byte, progress func(bytes int64)) (Manifest, Trailer, error) {
	return readAll(r, signingKey, progress, defaultLimit, nil)
}

// VerifyLimited is Verify with an explicit ceiling on the decompressed size of
// the whole archive (table data, entry headers, the manifest and the trailer).
// A small gzip file can expand to a huge amount of data, so a caller that knows
// how much a backup of this size may hold passes maxBytes; a file that expands
// beyond it fails with ErrCorrupt. maxBytes must be positive; NoLimit removes
// the ceiling.
func VerifyLimited(r io.Reader, signingKey []byte, progress func(bytes int64), maxBytes int64) (Manifest, Trailer, error) {
	if maxBytes <= 0 {
		return Manifest{}, Trailer{}, errLimit
	}
	return readAll(r, signingKey, progress, maxBytes, nil)
}

// ReadTables reads a whole backup, passing the data of each table, as one
// continuous reader, to load. load returns the number of rows it loaded.
// Everything is compared with the trailer, and the trailer with the
// signature (made with signingKey), which are stored after the data, so a
// mismatch is reported (as ErrCorrupt or ErrSignature) by the last step of
// ReadTables; the caller must not make what load did permanent before
// ReadTables returns nil.
func ReadTables(r io.Reader, signingKey []byte, load func(t TableSpec, data io.Reader) (rows int64, err error)) (Manifest, error) {
	if load == nil {
		return Manifest{}, errors.New("read backup: no loader given")
	}
	m, _, err := readAll(r, signingKey, nil, defaultLimit, load)
	return m, err
}

// ReadTablesLimited is ReadTables with an explicit ceiling on the decompressed
// size of the archive, as VerifyLimited has. maxBytes must be positive.
func ReadTablesLimited(r io.Reader, signingKey []byte, maxBytes int64, load func(t TableSpec, data io.Reader) (rows int64, err error)) (Manifest, error) {
	if load == nil {
		return Manifest{}, errors.New("read backup: no loader given")
	}
	if maxBytes <= 0 {
		return Manifest{}, errLimit
	}
	m, _, err := readAll(r, signingKey, nil, maxBytes, load)
	return m, err
}

var (
	errLimit = errors.New("read backup: the size limit must be positive")
	errNoKey = errors.New("backup: no signing key given")
)
