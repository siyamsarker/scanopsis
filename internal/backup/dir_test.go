// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDirRejectsNames(t *testing.T) {
	d, err := OpenDir(t.TempDir(), "fp")
	if err != nil {
		t.Fatal(err)
	}
	good := d.NewName(KindManual, time.Date(2026, 11, 23, 2, 0, 0, 0, time.UTC))
	if !strings.HasPrefix(good, "scanopsis-backup-20261123T020000Z-manual-") {
		t.Errorf("NewName = %q", good)
	}
	if _, err := d.Path(good); err != nil {
		t.Errorf("Path(%q) = %v", good, err)
	}
	for _, name := range []string{
		"../x",
		"../" + good,
		good + "/../",
		good + "/",
		"sub/" + good,
		"scanopsis-backup-20261123T020000Z-bogus-abc123.tar.gz",
		"scanopsis-backup-20261123T020000Z-manual-ABC123.tar.gz",
		strings.TrimSuffix(good, ".tar.gz"),
		good + ".partial",
		"",
	} {
		if _, err := d.Path(name); err == nil {
			t.Errorf("Path(%q) accepted", name)
		}
		if err := d.Delete(name); err == nil {
			t.Errorf("Delete(%q) accepted", name)
		}
	}
}

func TestDirFolderErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	_, err := OpenDir(missing, "fp")
	if !errors.Is(err, ErrFolder) || !strings.Contains(err.Error(), missing) {
		t.Errorf("missing folder: %v", err)
	}

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenDir(file, "fp"); !errors.Is(err, ErrFolder) || !strings.Contains(err.Error(), file) {
		t.Errorf("file as folder: %v", err)
	}

	ro := t.TempDir()
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(ro, 0o700) }()
	if f, err := os.CreateTemp(ro, "x"); err == nil { // running as root
		_ = f.Close()
		t.Skip("folder permissions do not apply to this user")
	}
	if _, err := OpenDir(ro, "fp"); !errors.Is(err, ErrFolder) || !strings.Contains(err.Error(), ro) {
		t.Errorf("read-only folder: %v", err)
	}
}

func TestDirSweepsStaleFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, UploadDir), 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-25 * time.Hour)
	young := time.Now().Add(-time.Hour)
	mk := func(name string, mt time.Time) string {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
		return p
	}
	oldPartial := mk("a.tar.gz.partial", old)
	youngPartial := mk("b.tar.gz.partial", young)
	oldUpload := mk(filepath.Join(UploadDir, "u1"), old)
	youngUpload := mk(filepath.Join(UploadDir, "u2"), young)
	other := mk("notes.txt", old)
	const kept, gone = "scanopsis-backup-20261120T020000Z-uploaded-abc123.tar.gz", "scanopsis-backup-20261120T020000Z-uploaded-zzz999.tar.gz"
	mk(kept, old)
	keptMeta := mk(metaName(kept), old)
	orphanMeta := mk(metaName(gone), old)
	status := mk(statusFile, old)
	if _, err := OpenDir(root, "fp"); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{
		oldPartial: false, youngPartial: true, oldUpload: false, youngUpload: true, other: true,
		keptMeta: true, orphanMeta: false, status: true,
	} {
		_, err := os.Stat(p)
		if (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", filepath.Base(p), err == nil, want)
		}
	}
}

func TestDirListAndFree(t *testing.T) {
	root := t.TempDir()
	d, err := OpenDir(root, "fp-a")
	if err != nil {
		t.Fatal(err)
	}
	if free, err := d.FreeBytes(); err != nil || free == 0 {
		t.Errorf("FreeBytes = %d, %v", free, err)
	}
	write := func(at time.Time, key string) string {
		name := d.NewName(KindScheduled, at)
		var buf bytes.Buffer
		m := Manifest{AppVersion: "0.4.0", Schema: 10, CreatedAt: at, Kind: KindScheduled, CreatedBy: "scheduler", KeyFingerprint: key,
			Tables: []TableSpec{{Name: "t", Columns: []string{"a"}}}}
		if _, err := WriteArchive(context.Background(), &buf, m, testSigningKey, func(_ context.Context, _ TableSpec, w io.Writer) (int64, error) {
			return 0, nil
		}, nil); err != nil {
			t.Fatal(err)
		}
		p, _ := d.Path(name)
		if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		return name
	}
	t1 := time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)
	t2 := t1.Add(24 * time.Hour)
	older := write(t1, "fp-a")
	newer := write(t2, "other")
	bad := d.NewName(KindManual, t1.Add(time.Hour))
	p, _ := d.Path(bad)
	if err := os.WriteFile(p, []byte("not a backup"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := d.List()
	if err != nil || len(got) != 3 {
		t.Fatalf("List = %v, %v", got, err)
	}
	if got[0].Name != newer || got[2].Name != older {
		t.Errorf("order = %s, %s, %s", got[0].Name, got[1].Name, got[2].Name)
	}
	if got[0].KeyMatches || !got[2].KeyMatches || got[2].CreatedBy != "scheduler" || got[2].AppVersion != "0.4.0" || got[2].Schema != 10 {
		t.Errorf("info = %+v / %+v", got[0], got[2])
	}
	if got[1].Name != bad || got[1].KeyMatches || got[1].Kind != KindManual {
		t.Errorf("damaged file info = %+v", got[1])
	}
	if used, err := d.UsedBytes(); err != nil || used <= 0 {
		t.Errorf("UsedBytes = %d, %v", used, err)
	}
	if err := d.Delete(older); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.List(); len(got) != 2 {
		t.Errorf("after delete %d files", len(got))
	}
}

func TestDumpNoSpaceMessage(t *testing.T) {
	err := &spaceError{need: 600 << 20, have: 1 << 20}
	if !errors.Is(err, ErrNoSpace) || !strings.Contains(err.Error(), "600.0 MiB") || !strings.Contains(err.Error(), "1.0 MiB") {
		t.Errorf("message = %v", err)
	}
}
