// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	// uploadReplaceIdle is how long an upload must have gone without a part
	// before a new upload may take its place when no slot is free.
	uploadReplaceIdle = 10 * time.Minute
	// uploadResultKeep is how long the outcome of a checked upload can be
	// asked for after the check ended.
	uploadResultKeep = 24 * time.Hour
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

// UploadState is where an upload stands: parts are being received, the
// complete file is being checked in the background, or the check ended
// with the file added to the backups (ready) or deleted (refused).
type UploadState string

// Upload states.
const (
	UploadUploading UploadState = "uploading"
	UploadChecking  UploadState = "checking"
	UploadReady     UploadState = "ready"
	UploadRefused   UploadState = "refused"
	// UploadCancelled is an upload someone cancelled, so a page that still
	// follows it learns that, not that the server lost it.
	UploadCancelled UploadState = "cancelled"
)

var (
	errNoUpload        = errors.New("no upload with this id is in progress")
	errTooManyUploads  = errors.New("two uploads are already in progress; finish or cancel one first")
	errUploadBusy      = errors.New("another part of this upload is still being received; try again in a moment")
	errUploadChecking  = fmt.Errorf("%w: the file is complete and being checked", errUploadBusy)
	errUploadCancelled = errors.New("the upload was cancelled, for example from Unfinished uploads on the Backups page or in another tab")
)

// tooManyUploadsError is errTooManyUploads with the time until a new
// upload can take the place of an idle one; wait is 0 when every open
// upload is being checked. The admin panel lists the open uploads, so its
// message says where to cancel one.
type tooManyUploadsError struct {
	wait  time.Duration
	scope uploadScope
}

func (e *tooManyUploadsError) Error() string {
	if e.wait <= 0 {
		return "two uploads are already in progress and being checked; try again when a check has finished"
	}
	minutes := int((e.wait + time.Minute - 1) / time.Minute)
	unit := "minutes"
	if minutes == 1 {
		unit = "minute"
	}
	if e.scope == scopeAdmin {
		return fmt.Sprintf("two uploads are already in progress, for example from a page that was closed during an upload. Cancel one under Unfinished uploads on the Backups page, or try again in %d %s: an upload that receives nothing for %d minutes then makes room for a new one",
			minutes, unit, int(uploadReplaceIdle/time.Minute))
	}
	return fmt.Sprintf("two uploads are already in progress, for example from a page that was closed during an upload. Try again in %d %s: an upload that receives nothing for %d minutes then makes room for a new one",
		minutes, unit, int(uploadReplaceIdle/time.Minute))
}

func (e *tooManyUploadsError) Is(target error) bool { return target == errTooManyUploads }

// incompleteError refuses to check an upload that is missing bytes.
type incompleteError struct{ received, size int64 }

func (e *incompleteError) Error() string {
	return fmt.Sprintf("the server has %d of %d bytes; send the rest first", e.received, e.size)
}

// UploadReason says why a checked upload was refused, with the code and
// message an error answer would have.
type UploadReason struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// upload is one file being received in parts. Its fields change under the
// lock of uploads: received and touched by the request that set busy,
// the outcome by the check that set state to checking.
type upload struct {
	id       string
	scope    uploadScope
	fileName string
	size     int64
	received int64
	path     string
	busy     bool
	started  time.Time
	touched  time.Time

	state UploadState
	// stopCheck cancels the check while the state is checking.
	stopCheck context.CancelFunc
	reason    *UploadReason
	backup    *Info
	preview   *Preview
}

// UploadStatus describes an upload. Parts are sent in order, each at most
// ChunkSize bytes, starting at Received. Once all of it was received and
// the upload completed, State is checking until the check ends; then it is
// ready, with Backup (the admin panel) or Preview (the setup screen), or
// refused, with Reason.
type UploadStatus struct {
	ID        string        `json:"id"`
	FileName  string        `json:"file_name"`
	Size      int64         `json:"size"`
	ChunkSize int           `json:"chunk_size"`
	Received  int64         `json:"received"`
	State     UploadState   `json:"state"`
	Reason    *UploadReason `json:"reason,omitempty"`
	Backup    *Info         `json:"backup,omitempty"`
	Preview   *Preview      `json:"preview,omitempty"`
}

func (u *upload) status() UploadStatus {
	return UploadStatus{
		ID: u.id, FileName: u.fileName, Size: u.size, ChunkSize: ChunkSize, Received: u.received,
		State: u.state, Reason: u.reason, Backup: u.backup, Preview: u.preview,
	}
}

// uploads holds the open uploads in memory, and the outcome of the checked
// ones for a day; a restart forgets them, and the folder removes their
// files after a day (see OpenDir).
type uploads struct {
	mu   sync.Mutex
	open map[string]*upload // uploading or checking
	done map[string]*upload // ready or refused
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

// expireLocked drops the uploads that have had no part for uploadIdle, and
// the outcomes older than uploadResultKeep. The caller holds mu.
func (u *uploads) expireLocked(now time.Time) {
	for id, up := range u.open {
		if !up.busy && up.state == UploadUploading && now.Sub(up.touched) > uploadIdle {
			_ = os.Remove(up.path) //nolint:gosec // the name is a random id
			delete(u.open, id)
		}
	}
	for id, up := range u.done {
		if now.Sub(up.touched) > uploadResultKeep {
			delete(u.done, id)
		}
	}
}

// makeRoomLocked frees a slot of scope when maxOpenUploads are open: it
// drops the upload of scope that has gone longest without a part, if that
// is more than uploadReplaceIdle. Otherwise it returns a tooManyUploadsError
// that says when a slot can be freed. The caller holds mu.
func (u *uploads) makeRoomLocked(scope uploadScope, now time.Time) error {
	var open int
	var oldest *upload
	for _, up := range u.open {
		if up.scope != scope {
			continue
		}
		open++
		if !up.busy && up.state == UploadUploading && (oldest == nil || up.touched.Before(oldest.touched)) {
			oldest = up
		}
	}
	if open < maxOpenUploads {
		return nil
	}
	if oldest == nil {
		return &tooManyUploadsError{scope: scope}
	}
	if idle := now.Sub(oldest.touched); idle <= uploadReplaceIdle {
		return &tooManyUploadsError{wait: uploadReplaceIdle - idle, scope: scope}
	}
	_ = os.Remove(oldest.path) //nolint:gosec // the name is a random id
	delete(u.open, oldest.id)
	return nil
}

// start opens a new upload of a file of size bytes in dir. The file must fit
// the free space of the folder less spaceMargin and less what the other open
// uploads still need. When maxOpenUploads of scope are open, the one that
// has gone longest without a part makes room if that is more than
// uploadReplaceIdle.
func (u *uploads) start(dir *Dir, scope uploadScope, fileName string, size int64, now time.Time) (UploadStatus, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.expireLocked(now)
	if err := u.makeRoomLocked(scope, now); err != nil {
		return UploadStatus{}, err
	}
	var held uint64
	for _, up := range u.open {
		held += uint64(up.size - up.received) //nolint:gosec // received never exceeds size
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
	up := &upload{id: id, scope: scope, fileName: fileName, size: size, path: path, started: now, touched: now, state: UploadUploading}
	u.open[id] = up
	return up.status(), nil
}

// OpenUpload describes an upload that is open: parts are being received,
// or the complete file is being checked. UpdatedAt is when the last part
// arrived, or when the check started.
type OpenUpload struct {
	ID        string      `json:"id"`
	FileName  string      `json:"file_name"`
	Size      int64       `json:"size"`
	Received  int64       `json:"received"`
	State     UploadState `json:"state"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// openOf returns the open uploads of scope, the oldest first.
func (u *uploads) openOf(scope uploadScope, now time.Time) []OpenUpload {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.expireLocked(now)
	var ups []*upload
	for _, up := range u.open {
		if up.scope == scope {
			ups = append(ups, up)
		}
	}
	slices.SortFunc(ups, func(a, b *upload) int {
		if c := a.started.Compare(b.started); c != 0 {
			return c
		}
		return strings.Compare(a.id, b.id)
	})
	out := make([]OpenUpload, 0, len(ups))
	for _, up := range ups {
		out = append(out, OpenUpload{
			ID: up.id, FileName: up.fileName, Size: up.size, Received: up.received,
			State: up.state, UpdatedAt: up.touched.UTC(),
		})
	}
	return out
}

// lookupLocked returns the open upload id of scope; one that was cancelled
// gives errUploadCancelled. The caller holds mu.
func (u *uploads) lookupLocked(scope uploadScope, id string, now time.Time) (*upload, error) {
	u.expireLocked(now)
	up := u.open[id]
	if up == nil || up.scope != scope {
		if d := u.done[id]; d != nil && d.scope == scope && d.state == UploadCancelled {
			return nil, errUploadCancelled
		}
		return nil, errNoUpload
	}
	return up, nil
}

// cancelledLocked keeps up as cancelled among the outcomes, so a page that
// still follows it learns that. The caller holds mu.
func (u *uploads) cancelledLocked(up *upload, now time.Time) {
	delete(u.open, up.id)
	up.state, up.touched, up.stopCheck = UploadCancelled, now, nil
	if u.done == nil {
		u.done = map[string]*upload{}
	}
	u.done[up.id] = up
}

// status returns the state of an open upload, or the outcome of a checked one.
func (u *uploads) status(scope uploadScope, id string, now time.Time) (UploadStatus, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if up := u.done[id]; up != nil && up.scope == scope {
		u.expireLocked(now)
		if u.done[id] != nil {
			return up.status(), nil
		}
	}
	up, err := u.lookupLocked(scope, id, now)
	if err != nil {
		return UploadStatus{}, err
	}
	return up.status(), nil
}

// acquire claims an open upload for one request that writes a part;
// release gives it back.
func (u *uploads) acquire(scope uploadScope, id string, now time.Time) (*upload, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	up, err := u.lookupLocked(scope, id, now)
	if err != nil {
		return nil, err
	}
	if up.state == UploadChecking {
		return nil, errUploadChecking
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

// complete moves a fully received upload to the checking state and returns
// it with the context of its check, which the server's shutdown (base) and
// a cancel of the upload end. started is false when the upload was already
// being checked or has its outcome: status then tells where it stands.
func (u *uploads) complete(base context.Context, scope uploadScope, id string, now time.Time) (st UploadStatus, up *upload, ctx context.Context, started bool, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.expireLocked(now)
	if d := u.done[id]; d != nil && d.scope == scope {
		return d.status(), nil, nil, false, nil
	}
	up, err = u.lookupLocked(scope, id, now)
	if err != nil {
		return UploadStatus{}, nil, nil, false, err
	}
	switch {
	case up.state == UploadChecking:
		return up.status(), nil, nil, false, nil
	case up.busy:
		return UploadStatus{}, nil, nil, false, errUploadBusy
	case up.received != up.size:
		return UploadStatus{}, nil, nil, false, &incompleteError{received: up.received, size: up.size}
	}
	ctx, up.stopCheck = context.WithCancel(base)
	up.state, up.busy, up.touched = UploadChecking, true, now
	return up.status(), up, ctx, true, nil
}

// settle ends the check of up: decide sets the outcome (state ready or
// refused, or uploading again when the check could not run) while no
// cancel can come in between. It returns false, and deletes the file, when
// the upload was cancelled during the check; decide is not called then.
func (u *uploads) settle(up *upload, now time.Time, decide func(up *upload)) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if up.stopCheck != nil {
		up.stopCheck()
		up.stopCheck = nil
	}
	if u.open[up.id] != up {
		_ = os.Remove(up.path) //nolint:gosec // the name is a random id
		return false
	}
	decide(up)
	up.busy, up.touched = false, now
	if up.state == UploadReady || up.state == UploadRefused {
		delete(u.open, up.id)
		if u.done == nil {
			u.done = map[string]*upload{}
		}
		u.done[up.id] = up
	}
	return true
}

// backupOf returns the backup a checked setup upload became.
func (u *uploads) backupOf(id string, now time.Time) (string, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.expireLocked(now)
	up := u.done[id]
	if up == nil || up.scope != scopeSetup || up.state != UploadReady || up.backup == nil {
		return "", false
	}
	return up.backup.Name, true
}

// cancel drops an open upload and deletes its file. A check in progress is
// stopped, and the file is deleted once it has stopped. It refuses, with
// errUploadBusy, an upload whose part is being written.
func (u *uploads) cancel(scope uploadScope, id string, now time.Time) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	up, err := u.lookupLocked(scope, id, now)
	if errors.Is(err, errUploadCancelled) {
		return nil // cancelled already
	}
	if err != nil {
		return err
	}
	if up.state == UploadChecking {
		if up.stopCheck != nil {
			up.stopCheck()
		}
		u.cancelledLocked(up, now)
		return nil
	}
	if up.busy {
		return errUploadBusy
	}
	_ = os.Remove(up.path) //nolint:gosec // the name is a random id
	u.cancelledLocked(up, now)
	return nil
}
