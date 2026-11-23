// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// randomBytes returns n deterministic pseudo-random bytes for a seed.
func randomBytes(seed uint64, n int) []byte {
	var s [32]byte
	s[0] = byte(seed)
	b := make([]byte, n)
	_, _ = rand.NewChaCha8(s).Read(b)
	return b
}

func testManifest() Manifest {
	return Manifest{
		AppVersion:     "0.4.0",
		AppCommit:      "abc1234",
		Schema:         42,
		CreatedAt:      time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC),
		Kind:           KindManual,
		CreatedBy:      "alice",
		KeyFingerprint: "00112233445566778899aabbccddeeff",
		Tables: []TableSpec{
			{Name: "projects", Columns: []string{"id", "name"}},
			{Name: "scans", Columns: []string{"id", "data"}},
			{Name: "empty_one", Columns: []string{"id"}},
		},
	}
}

// testData is the fixed content of each table in testManifest.
func testData() map[string][]byte {
	return map[string][]byte{
		"projects":  []byte("1,payments-api\n2,web-shop\n"),
		"scans":     randomBytes(7, 20<<20),
		"empty_one": nil,
	}
}

func dataCopier(data map[string][]byte) TableCopier {
	return func(_ context.Context, t TableSpec, w io.Writer) (int64, error) {
		d := data[t.Name]
		// Write in odd sized pieces so chunk boundaries do not line up.
		for len(d) > 0 {
			n := min(len(d), 100003)
			if _, err := w.Write(d[:n]); err != nil {
				return 0, err
			}
			d = d[n:]
		}
		return int64(bytes.Count(data[t.Name], []byte("\n"))), nil
	}
}

func buildArchive(t *testing.T) ([]byte, Trailer) {
	t.Helper()
	var buf bytes.Buffer
	tr, err := WriteArchive(context.Background(), &buf, testManifest(), dataCopier(testData()), nil)
	if err != nil {
		t.Fatalf("WriteArchive: %v", err)
	}
	return buf.Bytes(), tr
}

func TestRoundTrip(t *testing.T) {
	archive, trailer := buildArchive(t)
	data := testData()

	for name, d := range data {
		sum := sha256.Sum256(d)
		got := trailer.Tables[name]
		if got.Bytes != int64(len(d)) || got.SHA256 != hex.EncodeToString(sum[:]) {
			t.Errorf("trailer for %s = %+v, want %d bytes", name, got, len(d))
		}
	}

	var order []string
	out := map[string][]byte{}
	m, err := ReadTables(bytes.NewReader(archive), func(ts TableSpec, r io.Reader) (int64, error) {
		order = append(order, ts.Name)
		b, err := io.ReadAll(r)
		out[ts.Name] = b
		return int64(bytes.Count(b, []byte("\n"))), err
	})
	if err != nil {
		t.Fatalf("ReadTables: %v", err)
	}
	if m.Format != FormatVersion || m.Kind != KindManual || m.CreatedBy != "alice" || len(m.Tables) != 3 {
		t.Errorf("manifest = %+v", m)
	}
	if fmt.Sprint(order) != "[projects scans empty_one]" {
		t.Errorf("table order = %v", order)
	}
	for name, want := range data {
		if !bytes.Equal(out[name], want) {
			t.Errorf("table %s: read %d bytes, want %d identical", name, len(out[name]), len(want))
		}
	}

	m2, tr2, err := Verify(bytes.NewReader(archive), nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if m2.Schema != 42 || len(tr2.Tables) != 3 {
		t.Errorf("Verify returned %+v %+v", m2, tr2)
	}
}

func TestChunking(t *testing.T) {
	archive, _ := buildArchive(t)
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Size > ChunkSize {
			t.Errorf("%s is %d bytes, over the chunk size", h.Name, h.Size)
		}
		names = append(names, h.Name)
	}
	want := []string{
		"manifest.json",
		"tables/projects/000001.csv",
		"tables/scans/000001.csv", "tables/scans/000002.csv", "tables/scans/000003.csv",
		"tables/empty_one/000001.csv",
		"checksums.json",
	}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Errorf("entries = %v\nwant %v", names, want)
	}
}

// entry is one tar entry, used to build damaged archives.
type entry struct {
	name string
	typ  byte
	body []byte
}

func unpack(t *testing.T, archive []byte) []entry {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var es []entry
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return es
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		es = append(es, entry{h.Name, h.Typeflag, b})
	}
}

func pack(t *testing.T, es []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range es {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o600, Size: int64(len(e.body))}
		if e.typ == tar.TypeSymlink {
			h.Linkname = "/etc/passwd"
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func index(es []entry, name string) int {
	for i, e := range es {
		if e.name == name {
			return i
		}
	}
	return -1
}

func TestVerifyDetects(t *testing.T) {
	archive, _ := buildArchive(t)

	rewrite := func(f func(es []entry) []entry) []byte {
		return pack(t, f(unpack(t, archive)))
	}
	setFormat := func(es []entry, v int) []entry {
		var m map[string]any
		if err := json.Unmarshal(es[0].body, &m); err != nil {
			t.Fatal(err)
		}
		m["format"] = v
		b, _ := json.Marshal(m)
		es[0].body = b
		return es
	}

	var plainTar bytes.Buffer
	tw := tar.NewWriter(&plainTar)
	_ = tw.WriteHeader(&tar.Header{Name: "tables/x/000001.csv", Mode: 0o600, Size: 1})
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()

	var randomGz bytes.Buffer
	gz := gzip.NewWriter(&randomGz)
	_, _ = gz.Write(randomBytes(3, 64<<10))
	_ = gz.Close()

	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"truncated file", archive[:len(archive)/2], ErrCorrupt},
		{"flipped byte in a middle chunk", rewrite(func(es []entry) []entry {
			i := index(es, "tables/scans/000002.csv")
			es[i].body[len(es[i].body)/2] ^= 0xff
			return es
		}), ErrCorrupt},
		{"chunks reordered", rewrite(func(es []entry) []entry {
			i, j := index(es, "tables/scans/000002.csv"), index(es, "tables/scans/000003.csv")
			es[i], es[j] = es[j], es[i]
			return es
		}), ErrCorrupt},
		{"first chunk missing", rewrite(func(es []entry) []entry {
			i := index(es, "tables/scans/000001.csv")
			return append(es[:i], es[i+1:]...)
		}), ErrCorrupt},
		{"missing trailer", rewrite(func(es []entry) []entry { return es[:len(es)-1] }), ErrCorrupt},
		{"extra entry", rewrite(func(es []entry) []entry {
			return append(es, entry{"extra.txt", tar.TypeReg, []byte("x")})
		}), ErrCorrupt},
		{"entry between tables", rewrite(func(es []entry) []entry {
			i := index(es, "tables/scans/000001.csv")
			return append(es[:i], append([]entry{{"../../etc/passwd", tar.TypeReg, []byte("x")}}, es[i:]...)...)
		}), ErrCorrupt},
		{"symlink entry", rewrite(func(es []entry) []entry {
			i := index(es, "tables/projects/000001.csv")
			es[i] = entry{es[i].name, tar.TypeSymlink, nil}
			return es
		}), ErrCorrupt},
		{"table out of order", rewrite(func(es []entry) []entry {
			i, j := index(es, "tables/projects/000001.csv"), index(es, "tables/empty_one/000001.csv")
			es[i], es[j] = es[j], es[i]
			return es
		}), ErrCorrupt},
		{"trailer disagrees", rewrite(func(es []entry) []entry {
			i := index(es, "checksums.json")
			es[i].body = bytes.Replace(es[i].body, []byte(`"rows":2`), []byte(`"rows":3`), 1)
			es[i].body = bytes.Replace(es[i].body, []byte(`"bytes":26`), []byte(`"bytes":27`), 1)
			return es
		}), ErrCorrupt},
		{"truncated before the manifest", archive[:40], ErrCorrupt},
		{"column that injects SQL", rewrite(func(es []entry) []entry {
			es[0].body = bytes.Replace(es[0].body, []byte(`"id","name"`), []byte(`"id) FROM STDIN; --"`), 1)
			return es
		}), ErrCorrupt},
		{"table without columns", rewrite(func(es []entry) []entry {
			es[0].body = bytes.Replace(es[0].body, []byte(`["id","name"]`), []byte(`[]`), 1)
			return es
		}), ErrCorrupt},
		{"duplicate column", rewrite(func(es []entry) []entry {
			es[0].body = bytes.Replace(es[0].body, []byte(`"id","name"`), []byte(`"id","id"`), 1)
			return es
		}), ErrCorrupt},
		{"gzip of random bytes", randomGz.Bytes(), ErrNotBackup},
		{"plain tar without manifest", plainTar.Bytes(), ErrNotBackup},
		{"gzipped tar without manifest", rewrite(func(es []entry) []entry { return es[1:] }), ErrNotBackup},
		{"not gzip at all", []byte("hello, this is a text file"), ErrNotBackup},
		{"empty file", nil, ErrNotBackup},
		{"format 2", rewrite(func(es []entry) []entry { return setFormat(es, 2) }), ErrFormat},
		{"manifest with a table name that escapes", rewrite(func(es []entry) []entry {
			es[0].body = bytes.Replace(es[0].body, []byte(`"projects"`), []byte(`"../x"`), 1)
			return es
		}), ErrCorrupt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Verify(bytes.NewReader(tc.data), nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Verify error = %v, want %v", err, tc.want)
			}
			if !errors.Is(tc.want, ErrCorrupt) {
				if _, err := ReadManifest(bytes.NewReader(tc.data)); !errors.Is(err, tc.want) {
					t.Errorf("ReadManifest error = %v, want %v", err, tc.want)
				}
			}
			_, err = ReadTables(bytes.NewReader(tc.data), func(_ TableSpec, r io.Reader) (int64, error) {
				_, err := io.Copy(io.Discard, r)
				return 0, err
			})
			if !errors.Is(err, tc.want) {
				t.Errorf("ReadTables error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestErrorMessages(t *testing.T) {
	archive, _ := buildArchive(t)
	es := unpack(t, archive)
	es[0].body = bytes.Replace(es[0].body, []byte(`"format":1`), []byte(`"format":2`), 1)
	_, err := ReadManifest(bytes.NewReader(pack(t, es)))
	if err == nil || err.Error() != "the backup was made with format 2; this version reads format 1" {
		t.Errorf("format error = %v", err)
	}

	es = unpack(t, archive)
	i := index(es, "tables/projects/000001.csv")
	es[i].body[0] ^= 1
	_, _, err = Verify(bytes.NewReader(pack(t, es)), nil)
	if err == nil || err.Error() != "the file is damaged: checksum of table projects does not match" {
		t.Errorf("checksum error = %v", err)
	}

	_, err = ReadManifest(bytes.NewReader([]byte("nope")))
	if !errors.Is(err, ErrNotBackup) || err.Error()[:len(ErrNotBackup.Error())] != ErrNotBackup.Error() {
		t.Errorf("not a backup error = %v", err)
	}
}

func TestReadTablesRowMismatch(t *testing.T) {
	archive, _ := buildArchive(t)
	_, err := ReadTables(bytes.NewReader(archive), func(_ TableSpec, r io.Reader) (int64, error) {
		_, err := io.Copy(io.Discard, r)
		return 1000, err
	})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("error = %v, want ErrCorrupt for a wrong row count", err)
	}
}

func TestReadTablesLoadError(t *testing.T) {
	archive, _ := buildArchive(t)
	boom := errors.New("copy failed")
	calls := 0
	_, err := ReadTables(bytes.NewReader(archive), func(_ TableSpec, r io.Reader) (int64, error) {
		calls++
		return 0, boom
	})
	if !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("error = %v after %d calls, want the load error after 1", err, calls)
	}
}

func TestReadTablesUnreadData(t *testing.T) {
	// A loader that stops early must not break the next table or the checks.
	archive, _ := buildArchive(t)
	data := testData()
	_, err := ReadTables(bytes.NewReader(archive), func(ts TableSpec, r io.Reader) (int64, error) {
		_, _ = io.CopyN(io.Discard, r, 10)
		return int64(bytes.Count(data[ts.Name], []byte("\n"))), nil
	})
	if err != nil {
		t.Fatalf("ReadTables: %v", err)
	}
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func TestReadManifestFast(t *testing.T) {
	m := testManifest()
	m.Tables = []TableSpec{{Name: "scans", Columns: []string{"id"}}}
	data := map[string][]byte{"scans": randomBytes(9, 50<<20)}
	var buf bytes.Buffer
	if _, err := WriteArchive(context.Background(), &buf, m, dataCopier(data), nil); err != nil {
		t.Fatal(err)
	}
	if buf.Len() < 50<<20 {
		t.Fatalf("archive is only %d bytes", buf.Len())
	}
	cr := &countingReader{r: bytes.NewReader(buf.Bytes())}
	got, err := ReadManifest(cr)
	if err != nil {
		t.Fatal(err)
	}
	if got.AppVersion != "0.4.0" || got.Format != FormatVersion {
		t.Errorf("manifest = %+v", got)
	}
	if cr.n >= 1<<20 {
		t.Errorf("ReadManifest read %d bytes of input, want under 1 MiB", cr.n)
	}
}

// patternWriter feeds a table of a fixed size without holding it.
func syntheticCopier(size int64) TableCopier {
	block := randomBytes(5, 1<<20)
	return func(_ context.Context, _ TableSpec, w io.Writer) (int64, error) {
		for left := size; left > 0; {
			n := int64(len(block))
			if left < n {
				n = left
			}
			if _, err := w.Write(block[:n]); err != nil {
				return 0, err
			}
			left -= n
		}
		return 1, nil
	}
}

func TestWriteArchiveMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 500 MiB")
	}
	m := testManifest()
	m.Tables = []TableSpec{{Name: "scans", Columns: []string{"id"}}}
	copier := syntheticCopier(500 << 20)

	runtime.GC()
	var (
		mu   sync.Mutex
		peak uint64
		stop = make(chan struct{})
		done = make(chan struct{})
	)
	go func() {
		defer close(done)
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				mu.Lock()
				peak = max(peak, ms.HeapInuse)
				mu.Unlock()
			}
		}
	}()
	tr, err := WriteArchive(context.Background(), io.Discard, m, copier, nil)
	close(stop)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if tr.Tables["scans"].Bytes != 500<<20 {
		t.Errorf("wrote %d bytes", tr.Tables["scans"].Bytes)
	}
	mu.Lock()
	defer mu.Unlock()
	if peak > 48<<20 {
		t.Errorf("heap in use peaked at %d MiB, want under 48 MiB", peak>>20)
	}
	t.Logf("peak heap in use: %d MiB", peak>>20)
}

func TestWriteArchiveCancelAndErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := WriteArchive(ctx, io.Discard, testManifest(), dataCopier(testData()), nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context: error = %v", err)
	}

	boom := errors.New("copy exploded")
	_, err := WriteArchive(context.Background(), io.Discard, testManifest(),
		func(context.Context, TableSpec, io.Writer) (int64, error) { return 0, boom }, nil)
	if !errors.Is(err, boom) {
		t.Errorf("copier error = %v", err)
	}

	bad := testManifest()
	bad.Tables[0].Name = "../etc"
	if _, err := WriteArchive(context.Background(), io.Discard, bad, dataCopier(testData()), nil); err == nil {
		t.Error("a table name with a path was accepted")
	}
	dup := testManifest()
	dup.Tables[1].Name = dup.Tables[0].Name
	if _, err := WriteArchive(context.Background(), io.Discard, dup, dataCopier(testData()), nil); err == nil {
		t.Error("a duplicate table name was accepted")
	}
}

func TestProgress(t *testing.T) {
	var got []Progress
	var buf bytes.Buffer
	if _, err := WriteArchive(context.Background(), &buf, testManifest(), dataCopier(testData()), func(p Progress) {
		got = append(got, p)
	}); err != nil {
		t.Fatal(err)
	}
	last := got[len(got)-1]
	if last.TablesDone != 3 || last.TablesTotal != 3 {
		t.Errorf("last progress = %+v", last)
	}

	var seen int64
	if _, _, err := Verify(&buf, func(b int64) { seen = b }); err != nil {
		t.Fatal(err)
	}
	want := int64(len(testData()["projects"]) + len(testData()["scans"]))
	if seen != want {
		t.Errorf("Verify progress ended at %d, want %d", seen, want)
	}
}

func TestNoLibraryTextInErrors(t *testing.T) {
	archive, _ := buildArchive(t)
	bad := bytes.Clone(archive)
	bad[len(bad)/2] ^= 0xff
	for _, tail := range [][]byte{bad, bad[:len(bad)-3]} {
		_, _, err := Verify(bytes.NewReader(tail), nil)
		if !errors.Is(err, ErrCorrupt) {
			t.Fatalf("error = %v, want ErrCorrupt", err)
		}
		for _, leak := range []string{"gzip:", "flate:", "archive/tar", "unexpected EOF"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("error %q contains library text %q", err, leak)
			}
		}
	}
}

// forge builds a valid-looking archive of one table from the given chunks,
// with a correct trailer, so only the layout of the chunks is in question.
func forge(t *testing.T, chunks ...[]byte) []byte {
	t.Helper()
	m := testManifest()
	m.Format = FormatVersion
	m.Tables = []TableSpec{{Name: "scans", Columns: []string{"id"}}}
	mb, _ := json.Marshal(m)
	es := []entry{{manifestName, tar.TypeReg, mb}}
	h := sha256.New()
	var n int64
	for i, c := range chunks {
		es = append(es, entry{chunkName("scans", i+1), tar.TypeReg, c})
		h.Write(c)
		n += int64(len(c))
	}
	tb, _ := json.Marshal(Trailer{Tables: map[string]TableSum{"scans": {Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil))}}})
	return pack(t, append(es, entry{trailerName, tar.TypeReg, tb}))
}

func TestChunkLayout(t *testing.T) {
	full := randomBytes(1, ChunkSize)
	if _, _, err := Verify(bytes.NewReader(forge(t, full, full, []byte("tail"))), nil); err != nil {
		t.Fatalf("canonical layout rejected: %v", err)
	}
	if _, _, err := Verify(bytes.NewReader(forge(t, nil)), nil); err != nil {
		t.Fatalf("empty table rejected: %v", err)
	}
	cases := map[string][][]byte{
		"short chunk before the last": {[]byte("short"), []byte("tail")},
		"empty chunk after data":      {full, nil},
		"empty chunk before data":     {nil, []byte("tail")},
	}
	for name, chunks := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := Verify(bytes.NewReader(forge(t, chunks...)), nil)
			if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "chunks") {
				t.Fatalf("error = %v, want ErrCorrupt about chunks", err)
			}
		})
	}
}

func TestChunkCountBound(t *testing.T) {
	a := &archiveReader{pending: &tar.Header{Name: chunkName("x", maxChunks+1), Size: 1}}
	tr := &tableReader{a: a, name: "x", seq: maxChunks, prevSize: ChunkSize}
	if err := tr.nextChunk(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("error = %v, want ErrCorrupt", err)
	}
}

func TestSizeLimit(t *testing.T) {
	archive, _ := buildArchive(t) // about 20 MiB of table data
	const want = "the file is damaged: the file expands to more data than a backup of this size can hold"

	if _, _, err := VerifyLimited(bytes.NewReader(archive), nil, 10<<20); err == nil || !errors.Is(err, ErrCorrupt) || err.Error() != want {
		t.Errorf("VerifyLimited error = %v", err)
	}
	_, err := ReadTablesLimited(bytes.NewReader(archive), 10<<20, func(_ TableSpec, r io.Reader) (int64, error) {
		_, err := io.Copy(io.Discard, r)
		return 0, err
	})
	if !errors.Is(err, ErrCorrupt) || err.Error() != want {
		t.Errorf("ReadTablesLimited error = %v", err)
	}
	if _, _, err := VerifyLimited(bytes.NewReader(archive), nil, 64<<20); err != nil {
		t.Errorf("VerifyLimited within the limit: %v", err)
	}
	if _, _, err := VerifyLimited(bytes.NewReader(archive), nil, NoLimit); err != nil {
		t.Errorf("VerifyLimited with NoLimit: %v", err)
	}
}

func TestSizeLimitMustBePositive(t *testing.T) {
	archive, _ := buildArchive(t)
	if _, _, err := VerifyLimited(bytes.NewReader(archive), nil, 0); err == nil {
		t.Error("VerifyLimited accepted a limit of 0")
	}
	if _, err := ReadTablesLimited(bytes.NewReader(archive), -1, func(TableSpec, io.Reader) (int64, error) { return 0, nil }); err == nil {
		t.Error("ReadTablesLimited accepted a negative limit")
	}
}

func TestDefaultLimitApplies(t *testing.T) {
	old := defaultLimit
	defaultLimit = 10 << 20
	t.Cleanup(func() { defaultLimit = old })
	archive, _ := buildArchive(t)
	if _, _, err := Verify(bytes.NewReader(archive), nil); !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "expands") {
		t.Errorf("Verify error = %v, want the expansion error", err)
	}
	_, err := ReadTables(bytes.NewReader(archive), func(_ TableSpec, r io.Reader) (int64, error) {
		_, err := io.Copy(io.Discard, r)
		return 0, err
	})
	if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "expands") {
		t.Errorf("ReadTables error = %v, want the expansion error", err)
	}
	if DefaultMaxBytes <= 0 || DefaultMaxBytes == NoLimit {
		t.Errorf("DefaultMaxBytes = %d, want finite and positive", DefaultMaxBytes)
	}
}

// TestSizeLimitCountsEverything uses an archive whose table data is tiny but
// which carries a large extra amount in the manifest and in trailing padding.
func TestSizeLimitCountsEverything(t *testing.T) {
	small := forge(t, []byte("a,b\n"))
	if _, _, err := VerifyLimited(bytes.NewReader(small), nil, 1<<20); err != nil {
		t.Fatalf("small archive rejected: %v", err)
	}

	// Trailing data after the tar end marker (inside the gzip stream).
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	es := unpack(t, small)
	for _, e := range es {
		_ = tw.WriteHeader(&tar.Header{Name: e.name, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(e.body))})
		_, _ = tw.Write(e.body)
	}
	_ = tw.Close()
	_, _ = gz.Write(make([]byte, 5<<20))
	_ = gz.Close()
	// The padding is over the trailing-data cap and over a 1 MiB limit alike;
	// with the cap out of the way the byte budget must still stop it.
	_, _, err := VerifyLimited(bytes.NewReader(buf.Bytes()), nil, 1<<20)
	if !errors.Is(err, ErrCorrupt) {
		t.Errorf("trailing padding: error = %v", err)
	}

	// Drained table data: the loader reads nothing, the rest is drained.
	big := forge(t, randomBytes(2, ChunkSize), []byte("x"))
	_, err = ReadTablesLimited(bytes.NewReader(big), 4<<20, func(TableSpec, io.Reader) (int64, error) { return 0, nil })
	if err == nil || !strings.Contains(err.Error(), "expands") {
		t.Errorf("drained data: error = %v, want the expansion error", err)
	}

	// A skipped entry body: an oversized extra entry before the trailer is an
	// unexpected entry, but its bytes are charged while tar skips them.
	es = unpack(t, small)
	es = append(es[:len(es)-1], entry{"filler.bin", tar.TypeReg, make([]byte, 6<<20)}, es[len(es)-1])
	_, _, err = VerifyLimited(bytes.NewReader(pack(t, es)), nil, 1<<20)
	if !errors.Is(err, ErrCorrupt) {
		t.Errorf("skipped entry: error = %v", err)
	}
}

func TestTooManyTables(t *testing.T) {
	m := testManifest()
	m.Tables = nil
	for i := 0; i <= maxTables; i++ {
		m.Tables = append(m.Tables, TableSpec{Name: fmt.Sprintf("t%d", i), Columns: []string{"id"}})
	}
	if _, err := WriteArchive(context.Background(), io.Discard, m, dataCopier(nil), nil); err == nil {
		t.Error("WriteArchive accepted too many tables")
	}
	m.Format = FormatVersion
	mb, _ := json.Marshal(m)
	_, err := ReadManifest(bytes.NewReader(pack(t, []entry{{manifestName, tar.TypeReg, mb}})))
	if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "too many tables") {
		t.Errorf("ReadManifest error = %v", err)
	}
}

// TestSizeLimitIsExact shows that headers, the manifest, the trailer and the
// end marker are charged: the table data is 5 bytes, yet the limit that is
// just enough is the size of the whole decompressed archive.
func TestSizeLimitIsExact(t *testing.T) {
	small := forge(t, []byte("a,b\n"))
	gz, err := gzip.NewReader(bytes.NewReader(small))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	total := int64(len(raw))
	if total < 2048 {
		t.Fatalf("decompressed size %d is unexpectedly small", total)
	}
	if _, _, err := VerifyLimited(bytes.NewReader(small), nil, total); err != nil {
		t.Errorf("limit equal to the size rejected: %v", err)
	}
	_, _, err = VerifyLimited(bytes.NewReader(small), nil, total-1)
	if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "expands") {
		t.Errorf("limit one byte short: error = %v", err)
	}
	_, _, err = VerifyLimited(bytes.NewReader(small), nil, 100)
	if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "expands") {
		t.Errorf("limit below the manifest: error = %v", err)
	}
}
