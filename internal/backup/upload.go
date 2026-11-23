// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// maxOpenUploads is how many uploads may be open at once, on the admin
	// panel and on the setup screen each.
	maxOpenUploads = 2
	// uploadIdle is how long an upload may go without a part before the
	// next upload request drops it and deletes its file.
	uploadIdle = time.Hour
	// maxFileNameBytes bounds the original name of an uploaded file.
	maxFileNameBytes = 255
)

// uploadScope keeps the uploads of the admin panel and of the setup screen
// apart: an id of one is unknown to the other.
type uploadScope string

const (
	scopeAdmin uploadScope = "admin"
	scopeSetup uploadScope = "setup"
)

var (
	errNoUpload       = errors.New("no upload with this id is in progress")
	errTooManyUploads = errors.New("two uploads are already in progress; finish or cancel one first")
	errUploadBusy     = errors.New("another part of this upload is still being received; try again in a moment")
)

// upload is one file being received in parts. received and touched change
// under the lock of uploads, by the request that set busy.
type upload struct {
	id       string
	scope    uploadScope
	fileName string
	size     int64
	received int64
	path     string
	busy     bool
	touched  time.Time
}

// UploadStatus describes an upload in progress. Parts are sent in order,
// each at most ChunkSize bytes, starting at Received.
type UploadStatus struct {
	ID        string `json:"id"`
	FileName  string `json:"file_name"`
	Size      int64  `json:"size"`
	ChunkSize int    `json:"chunk_size"`
	Received  int64  `json:"received"`
}

func (u *upload) status() UploadStatus {
	return UploadStatus{ID: u.id, FileName: u.fileName, Size: u.size, ChunkSize: ChunkSize, Received: u.received}
}

// uploads holds the open uploads in memory; a restart forgets them, and the
// folder removes their files after a day (see OpenDir).
type uploads struct {
	mu   sync.Mutex
	open map[string]*upload
	// done maps a completed setup upload to the backup it became, for the
	// restore that follows.
	done map[string]string
}

// checkFileName returns a message for a file name that is empty, too long or
// not plain text, or "".
func checkFileName(name string) string {
	if name == "" || len(name) > maxFileNameBytes || !utf8.ValidString(name) {
		return fmt.Sprintf("file_name must be 1 to %d bytes of text.", maxFileNameBytes)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "file_name must not contain control characters."
		}
	}
	return ""
}

// uploadSpaceError is an ErrNoSpace for an upload, with the numbers.
type uploadSpaceError struct{ size, held, have uint64 }

func (e *uploadSpaceError) Error() string {
	msg := fmt.Sprintf("%v: the upload needs %s and %s to spare, and the backup folder has %s free",
		ErrNoSpace, humanBytes(e.size), humanBytes(spaceMargin), humanBytes(e.have))
	if e.held > 0 {
		msg += fmt.Sprintf(", of which %s is held for other uploads", humanBytes(e.held))
	}
	return msg
}

func (e *uploadSpaceError) Is(target error) bool { return target == ErrNoSpace }

// expireLocked drops the uploads that have had no part for uploadIdle. The
// caller holds mu.
func (u *uploads) expireLocked(now time.Time) {
	for id, up := range u.open {
		if !up.busy && now.Sub(up.touched) > uploadIdle {
			_ = os.Remove(up.path) //nolint:gosec // the name is a random id
			delete(u.open, id)
		}
	}
}

// start opens a new upload of a file of size bytes in dir. The file must fit
// the free space of the folder less spaceMargin and less what the other open
// uploads still need.
func (u *uploads) start(dir *Dir, scope uploadScope, fileName string, size int64, now time.Time) (UploadStatus, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.expireLocked(now)
	var open int
	var held uint64
	for _, up := range u.open {
		if up.scope == scope {
			open++
		}
		held += uint64(up.size - up.received) //nolint:gosec // received never exceeds size
	}
	if open >= maxOpenUploads {
		return UploadStatus{}, errTooManyUploads
	}
	free, err := dir.FreeBytes()
	if err != nil {
		return UploadStatus{}, err
	}
	if need := uint64(size) + spaceMargin + held; free < need { //nolint:gosec // size is positive
		return UploadStatus{}, &uploadSpaceError{size: uint64(size), held: held, have: free} //nolint:gosec // size is positive
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return UploadStatus{}, fmt.Errorf("upload id: %w", err)
	}
	id := hex.EncodeToString(raw)
	folder := filepath.Join(dir.Root(), UploadDir)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return UploadStatus{}, dir.writeError(err)
	}
	path := filepath.Join(folder, id+partial)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the name is a random id
	if err != nil {
		return UploadStatus{}, dir.writeError(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return UploadStatus{}, dir.writeError(err)
	}
	if u.open == nil {
		u.open = map[string]*upload{}
	}
	up := &upload{id: id, scope: scope, fileName: fileName, size: size, path: path, touched: now}
	u.open[id] = up
	return up.status(), nil
}

// lookupLocked returns the open upload id of scope. The caller holds mu.
func (u *uploads) lookupLocked(scope uploadScope, id string, now time.Time) (*upload, error) {
	u.expireLocked(now)
	up := u.open[id]
	if up == nil || up.scope != scope {
		return nil, errNoUpload
	}
	return up, nil
}

// status returns the state of an open upload.
func (u *uploads) status(scope uploadScope, id string, now time.Time) (UploadStatus, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	up, err := u.lookupLocked(scope, id, now)
	if err != nil {
		return UploadStatus{}, err
	}
	return up.status(), nil
}

// acquire claims an open upload for one request; release gives it back.
func (u *uploads) acquire(scope uploadScope, id string, now time.Time) (*upload, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	up, err := u.lookupLocked(scope, id, now)
	if err != nil {
		return nil, err
	}
	if up.busy {
		return nil, errUploadBusy
	}
	up.busy = true
	return up, nil
}

// release gives back an upload claimed with acquire, with received bytes.
func (u *uploads) release(up *upload, received int64, now time.Time) {
	u.mu.Lock()
	defer u.mu.Unlock()
	up.busy = false
	up.received = received
	up.touched = now
}

// drop removes a claimed upload, deleting its file unless keepFile.
func (u *uploads) drop(up *upload, keepFile bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !keepFile {
		_ = os.Remove(up.path) //nolint:gosec // the name is a random id
	}
	delete(u.open, up.id)
}

// finished records that the setup upload id became the backup name.
func (u *uploads) finished(id, name string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.done == nil {
		u.done = map[string]string{}
	}
	u.done[id] = name
}

// backupOf returns the backup a completed setup upload became.
func (u *uploads) backupOf(id string) (string, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	name, ok := u.done[id]
	return name, ok
}

// cancel drops an open upload and deletes its file. A part still being
// received is written into the deleted file and then forgotten.
func (u *uploads) cancel(scope uploadScope, id string, now time.Time) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	up, err := u.lookupLocked(scope, id, now)
	if err != nil {
		return err
	}
	_ = os.Remove(up.path) //nolint:gosec // the name is a random id
	delete(u.open, id)
	return nil
}
