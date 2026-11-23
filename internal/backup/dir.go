// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ErrFolder means the backup folder is missing or cannot be written. The
// message names the folder and says how to fix it.
var ErrFolder = errors.New("the backup folder cannot be used")

const (
	// staleAfter is how old a partial or upload file must be before OpenDir
	// removes it: a transfer that old is not running any more.
	staleAfter = 24 * time.Hour
	// UploadDir is the sub-folder, inside the backup folder, where uploads
	// are received before they are checked.
	UploadDir = ".uploads"
	partial   = ".partial"
)

var fileName = regexp.MustCompile(`^scanopsis-backup-([0-9]{8}T[0-9]{6}Z)-(manual|scheduled|uploaded|before-restore)-[a-z0-9]{6}\.tar\.gz$`)

// Info describes one backup file.
type Info struct {
	Name       string    `json:"name"`
	Kind       Kind      `json:"kind"`
	Size       int64     `json:"size"`
	CreatedAt  time.Time `json:"created_at"`
	CreatedBy  string    `json:"created_by"`
	AppVersion string    `json:"app_version"`
	Schema     int64     `json:"schema"`
	// KeyMatches is true when the backup was made with the secret key this
	// server runs with, so its encrypted values can be read after a restore.
	KeyMatches bool `json:"key_matches"`
	// Readable is false when the manifest of the file cannot be read; the
	// file is listed with the kind and time of its name so it can be
	// deleted, and cannot be restored.
	Readable bool `json:"readable"`
	// NewerVersion is true when a newer version of Scanopsis made the
	// backup: its format or database schema is newer than this version
	// knows. Such a backup cannot be restored here; one in a newer format
	// is also not Readable.
	NewerVersion bool `json:"newer_version"`
	// UploadedAt and OriginalName describe an uploaded file; they are nil and
	// empty for backups made here.
	UploadedAt   *time.Time `json:"uploaded_at"`
	OriginalName string     `json:"original_name"`
}

type cached struct {
	size   int64
	mtime  time.Time
	m      Manifest
	ok     bool
	newer  bool // the manifest is of a newer format
	upload uploadMeta
}

// uploadMeta is kept next to an uploaded backup, in a hidden file named
// after it, so the list can tell when the file arrived and what it was
// called. The backup file itself is never changed.
type uploadMeta struct {
	UploadedAt   time.Time `json:"uploaded_at"`
	OriginalName string    `json:"original_name"`
}

// metaName is the name of the file that holds the uploadMeta of the backup
// called name.
func metaName(name string) string { return "." + name + ".json" }

// Dir is the folder that holds backup files.
type Dir struct {
	path        string
	fingerprint string
	freeBytes   func() (uint64, error) // replaced in tests

	mu    sync.Mutex
	cache map[string]cached
}

// OpenDir checks that path is a writable folder, removes partial files and
// uploads older than 24 hours, and returns the Dir. keyFingerprint is the
// fingerprint of the running secret key (see KeyFingerprint).
func OpenDir(path string, keyFingerprint string) (*Dir, error) {
	st, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("%w: %s does not exist; create it and make it writable for the user the server runs as (uid 65532 in the container image), or mount a volume there", ErrFolder, path)
	case err != nil:
		return nil, fmt.Errorf("%w: %s: %w", ErrFolder, path, err)
	case !st.IsDir():
		return nil, fmt.Errorf("%w: %s is not a folder; set SCANOPSIS_BACKUP_DIR to a folder", ErrFolder, path)
	}
	probe, err := os.CreateTemp(path, ".write-test-*")
	if err != nil {
		return nil, fmt.Errorf("%w: %s is not writable (%w); make it writable for the user the server runs as (uid 65532 in the container image) and check that the disk has free space and is not read-only", ErrFolder, path, err)
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())

	d := &Dir{path: path, fingerprint: keyFingerprint, freeBytes: func() (uint64, error) { return statfsFree(path) }, cache: map[string]cached{}}
	d.sweep(time.Now())
	return d, nil
}

// sweep removes stale partial files and uploads. Failures are ignored: a
// file that cannot be removed now is tried again at the next start.
func (d *Dir) sweep(now time.Time) {
	remove := func(dir string, match func(string) bool) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() || !match(e.Name()) {
				continue
			}
			if fi, err := e.Info(); err == nil && now.Sub(fi.ModTime()) > staleAfter {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	remove(d.path, func(n string) bool { return strings.HasSuffix(n, partial) })
	remove(filepath.Join(d.path, UploadDir), func(string) bool { return true })
	// The upload details of a backup that was removed by hand.
	remove(d.path, func(n string) bool {
		name, ok := strings.CutSuffix(strings.TrimPrefix(n, "."), ".json")
		if !ok || !strings.HasPrefix(n, ".") || !fileName.MatchString(name) {
			return false
		}
		_, err := os.Stat(filepath.Join(d.path, name))
		return errors.Is(err, os.ErrNotExist)
	})
}

// Root returns the folder path.
func (d *Dir) Root() string { return d.path }

// NewName returns a new unique file name for a backup of the given kind.
func (d *Dir) NewName(kind Kind, t time.Time) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic("backup: random source failed: " + err.Error())
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return fmt.Sprintf("scanopsis-backup-%s-%s-%s.tar.gz", t.UTC().Format("20060102T150405Z"), kind, b)
}

// Path returns the full path of the backup called name. It accepts only
// names NewName can produce, so a name from a request cannot leave the folder.
func (d *Dir) Path(name string) (string, error) {
	if !fileName.MatchString(name) {
		return "", fmt.Errorf("%q is not the name of a backup file", name)
	}
	return filepath.Join(d.path, name), nil
}

// Delete removes the backup called name.
func (d *Dir) Delete(name string) error {
	p, err := d.Path(name)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		return fmt.Errorf("delete backup: %w", err)
	}
	_ = os.Remove(filepath.Join(d.path, metaName(name))) //nolint:gosec // name matched fileName in Path
	d.mu.Lock()
	delete(d.cache, name)
	d.mu.Unlock()
	return nil
}

// FreeBytes returns the space available to the server in the folder.
func (d *Dir) FreeBytes() (uint64, error) { return d.freeBytes() }

func statfsFree(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("%w: read free space of %s: %w; make sure the folder exists and is mounted", ErrFolder, path, err)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil //nolint:unconvert // field types differ between platforms
}

func (d *Dir) files() ([]os.DirEntry, error) {
	entries, err := os.ReadDir(d.path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFolder, err)
	}
	out := entries[:0]
	for _, e := range entries {
		if !e.IsDir() && fileName.MatchString(e.Name()) {
			out = append(out, e)
		}
	}
	return out, nil
}

// UsedBytes returns the total size of the backup files.
func (d *Dir) UsedBytes() (int64, error) {
	entries, err := d.files()
	if err != nil {
		return 0, err
	}
	var n int64
	for _, e := range entries {
		if fi, err := e.Info(); err == nil {
			n += fi.Size()
		}
	}
	return n, nil
}

// List returns the backups, newest first. The manifest of a file is read once
// and remembered while its size and modification time stay the same. A file
// whose manifest cannot be read is still listed, with the kind and time of
// its name, so it can be deleted.
func (d *Dir) List() ([]Info, error) {
	entries, err := d.files()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(entries))
	out := make([]Info, 0, len(entries))
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			continue // removed since the listing
		}
		name := e.Name()
		seen[name] = true
		out = append(out, d.info(name, fi))
	}
	d.mu.Lock()
	for n := range d.cache {
		if !seen[n] {
			delete(d.cache, n)
		}
	}
	d.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].Name > out[j].Name
	})
	return out, nil
}

func (d *Dir) info(name string, fi os.FileInfo) Info {
	d.mu.Lock()
	c, hit := d.cache[name]
	d.mu.Unlock()
	if !hit || c.size != fi.Size() || !c.mtime.Equal(fi.ModTime()) {
		c = cached{size: fi.Size(), mtime: fi.ModTime()}
		if f, err := os.Open(filepath.Join(d.path, name)); err == nil { //nolint:gosec // name matched fileName
			m, err := ReadManifest(f)
			_ = f.Close()
			c.m, c.ok, c.newer = m, err == nil, errors.Is(err, ErrFormat)
		}
		if b, err := os.ReadFile(filepath.Join(d.path, metaName(name))); err == nil { //nolint:gosec // name matched fileName
			_ = json.Unmarshal(b, &c.upload)
		}
		d.mu.Lock()
		d.cache[name] = c
		d.mu.Unlock()
	}
	return d.build(name, fi.Size(), c)
}

func (d *Dir) build(name string, size int64, c cached) Info {
	sub := fileName.FindStringSubmatch(name)
	var info Info
	if c.ok {
		info = infoOf(name, size, c.m, d.fingerprint)
	} else {
		at, _ := time.Parse("20060102T150405Z", sub[1])
		info = Info{Name: name, Kind: Kind(sub[2]), Size: size, CreatedAt: at, NewerVersion: c.newer}
	}
	if info.Kind == KindUploaded && !c.upload.UploadedAt.IsZero() {
		at := c.upload.UploadedAt
		info.UploadedAt, info.OriginalName = &at, c.upload.OriginalName
	}
	return info
}

// adopt moves the checked upload at src, a file inside the folder, into the
// folder as a backup of kind uploaded, and records when it arrived and the
// name it had. It returns the new backup's description.
func (d *Dir) adopt(src, originalName string, at time.Time) (Info, error) {
	name := d.NewName(KindUploaded, at)
	final := filepath.Join(d.path, name)
	meta := filepath.Join(d.path, metaName(name))
	// The details come first, so a listed upload always has them.
	if err := writeFileAtomic(meta, uploadMeta{UploadedAt: at.UTC(), OriginalName: originalName}); err != nil {
		return Info{}, d.writeError(err)
	}
	if err := os.Rename(src, final); err != nil { //nolint:gosec // src is an upload named by a random id, final a name from NewName
		_ = os.Remove(meta)
		return Info{}, d.writeError(err)
	}
	if err := syncDir(d.path); err != nil {
		// Without a synced folder the rename may be lost after a crash.
		_ = os.Remove(final)
		_ = os.Remove(meta)
		return Info{}, d.writeError(err)
	}
	fi, err := os.Stat(final)
	if err != nil {
		return Info{}, d.writeError(err)
	}
	return d.info(name, fi), nil
}

// writeError reports a failure to write into the folder.
func (d *Dir) writeError(err error) error {
	return fmt.Errorf("%w: %s could not be written (%w); make sure it is writable for the user the server runs as (uid 65532 in the container image) and has free space", ErrFolder, d.path, err)
}

// syncDir makes the entries of the folder at path durable.
func syncDir(path string) error {
	dir, err := os.Open(path) //nolint:gosec // the backup folder
	if err != nil {
		return err
	}
	err = dir.Sync()
	if cerr := dir.Close(); err == nil {
		err = cerr
	}
	return err
}
