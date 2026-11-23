// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/httpapi"
)

const (
	actionUploaded          = audit.ActionBackupUploaded
	actionDownloaded        = audit.ActionBackupDownloaded
	actionDeleted           = audit.ActionBackupDeleted
	actionScheduleDismissed = audit.ActionBackupScheduleAlertDismissed

	// setupActor is the actor of an upload and a restore on the setup
	// screen, where no account exists yet.
	setupActor = "setup"
	// confirmPhrase must be typed to start a restore.
	confirmPhrase = "RESTORE"
	// setupTokenHeader carries the setup token on the requests of the setup
	// screen that have no JSON body.
	setupTokenHeader = "X-Setup-Token" //nolint:gosec // a header name, not a credential

	// maxJSONBody bounds the JSON bodies of these endpoints.
	maxJSONBody = 8 << 10
	// partTimeout is how long one part of an upload may take to arrive.
	partTimeout = 5 * time.Minute
	// writeGrace is how long the server waits for a client to take more of
	// a download, or for the answer to a part; a download gets this much
	// time again whenever it moves on.
	writeGrace = 60 * time.Second
	// deadlineStep is how much of a download may be written before the
	// write deadline is moved on; it is also moved on every few seconds.
	deadlineStep = 8 << 20
	// tooManyRetryAfter is the Retry-After, in seconds, of a code or token
	// refused for too many attempts.
	tooManyRetryAfter = 30
	// previewKeep is how long a finished preview is served from memory.
	previewKeep = time.Hour
	// maxPreviews bounds the finished previews kept in memory.
	maxPreviews = 16
	// previewAbandon is how long a preview is computed without a request
	// asking for it before it is stopped: the dialog asks every few seconds.
	previewAbandon = 60 * time.Second
)

// APIOptions configure the HTTP API of the backups.
type APIOptions struct {
	// Service runs the jobs; its folder, key fingerprint and signing key
	// are the ones the API uses.
	Service *Service
	// FolderPath is the configured backup folder; FolderErr is why it cannot
	// be used, or nil. The list shows both.
	FolderPath string
	FolderErr  error
	Audit      auth.AuditRecorder
	Log        *slog.Logger
	ClientIP   func(*http.Request) netip.Addr
	// CheckCode checks a fresh two-factor code of a user, as
	// auth.MFA.CheckCode does.
	CheckCode func(ctx context.Context, ip netip.Addr, userID int64, code string) error
	// CheckSetupToken checks the setup token, as auth.Setup.CheckToken
	// does; it refuses with auth.ErrSetupDone once an administrator exists.
	CheckSetupToken func(ctx context.Context, ip netip.Addr, token string) error
	// RetentionDays returns the data retention period of this server, for
	// the preview of a restore.
	RetentionDays func() int
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// API serves the backup endpoints: list, back up, download, delete,
// preview and restore on the admin panel, uploads in parts on the admin
// panel and on the setup screen, and the public restore status.
type API struct {
	o   APIOptions
	svc *Service
	up  uploads

	// previews holds the previews being computed or computed, by file.
	pmu      sync.Mutex
	previews map[previewKey]*previewEntry
	// verify checks a complete upload and computePreview a backup in the
	// folder; tests replace them.
	verify         func(ctx context.Context, up *upload, scope uploadScope) (Preview, error)
	computePreview func(ctx context.Context, path string) (Preview, error)
	// previewAbandon is how long a preview being computed is kept without
	// a request for it.
	previewAbandon time.Duration
}

// previewKey names a backup file as it is now, and the retention period a
// preview of it counts with: a changed file or setting is a new preview.
type previewKey struct {
	name  string
	size  int64
	mtime int64
	days  int
}

// previewEntry is a preview being computed (done false) or its outcome.
type previewEntry struct {
	done bool
	pv   Preview
	err  error
	at   time.Time
	// asked is when a request last asked for the preview being computed;
	// stop stops computing it.
	asked time.Time
	stop  context.CancelFunc
}

// NewAPI returns the API.
func NewAPI(o APIOptions) *API {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.ClientIP == nil {
		o.ClientIP = func(*http.Request) netip.Addr { return netip.Addr{} }
	}
	if o.RetentionDays == nil {
		o.RetentionDays = func() int { return 0 }
	}
	a := &API{o: o, svc: o.Service, previews: map[previewKey]*previewEntry{}, previewAbandon: previewAbandon}
	a.verify, a.computePreview = a.verifyUpload, a.previewOf
	return a
}

// Handlers returns the handlers by OpenAPI operation ID. The router checks
// the session and the admin role of the admin routes; the setup routes
// check the setup token themselves.
func (a *API) Handlers() map[string]http.Handler {
	admin := func(fn func(http.ResponseWriter, *http.Request, uploadScope)) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fn(w, r, scopeAdmin) })
	}
	setup := func(fn func(http.ResponseWriter, *http.Request, uploadScope)) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fn(w, r, scopeSetup) })
	}
	return map[string]http.Handler{
		"listBackups":                http.HandlerFunc(a.list),
		"createBackup":               http.HandlerFunc(a.create),
		"downloadBackup":             http.HandlerFunc(a.download),
		"deleteBackup":               http.HandlerFunc(a.delete),
		"previewRestore":             http.HandlerFunc(a.preview),
		"restoreBackup":              http.HandlerFunc(a.restore),
		"startBackupUpload":          admin(a.startUpload),
		"getBackupUpload":            admin(a.getUpload),
		"putBackupUploadChunk":       admin(a.putPart),
		"cancelBackupUpload":         admin(a.cancelUpload),
		"completeBackupUpload":       admin(a.completeUpload),
		"startSetupRestoreUpload":    setup(a.startUpload),
		"getSetupRestoreUpload":      setup(a.getUpload),
		"putSetupRestoreUploadChunk": setup(a.putPart),
		"cancelSetupRestoreUpload":   setup(a.cancelUpload),
		"completeSetupRestoreUpload": setup(a.completeUpload),
		"checkSetupRestoreToken":     http.HandlerFunc(a.checkSetupToken),
		"restoreSetupBackup":         http.HandlerFunc(a.setupRestore),
		"getRestoreStatus":           http.HandlerFunc(a.restoreStatus),
		"dismissBackupScheduleAlert": http.HandlerFunc(a.dismissSchedule),
	}
}

// dir returns the backup folder, or nil when it cannot be used.
func (a *API) dir() *Dir { return a.svc.o.Dir }

func writeError(w http.ResponseWriter, status int, code, message string) {
	httpapi.WriteError(w, status, code, message)
}

// writeReceived answers an error that tells how much of an upload the
// server has, so the client can go on from there.
func writeReceived(w http.ResponseWriter, status int, code, message string, received int64) {
	type body struct {
		Code     string `json:"code"`
		Message  string `json:"message"`
		Received int64  `json:"received"`
	}
	httpapi.WriteJSON(w, status, struct {
		Error body `json:"error"`
	}{body{code, message, received}})
}

func (a *API) internalError(w http.ResponseWriter, r *http.Request, msg string, err error) {
	a.o.Log.ErrorContext(r.Context(), msg, "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "Something went wrong. Try again later.")
}

// sentence makes an error text of the package into a sentence for people.
func sentence(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	s = string(r)
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// abortBody makes the server give up on a request body that will not be
// read, instead of waiting for it before the answer.
func abortBody(w http.ResponseWriter, r *http.Request) {
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return
	}
	if err := http.NewResponseController(w).SetReadDeadline(time.Now()); err != nil {
		w.Header().Set("Connection", "close")
	}
}

// decode reads one JSON object of at most maxJSONBody bytes into v. With
// optional, an empty body leaves v as it is. It answers 400 itself.
func decode(w http.ResponseWriter, r *http.Request, v any, optional bool, shape string) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if optional && errors.Is(err, io.EOF) {
		return true
	}
	if err == nil {
		if _, extra := dec.Token(); !errors.Is(extra, io.EOF) {
			err = errors.New("trailing data")
		}
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", shape)
		return false
	}
	return true
}

func actor(r *http.Request) (*auth.Principal, string) {
	p, _ := auth.PrincipalFrom(r.Context())
	return p, p.Source + ":" + p.Username
}

// folderUnavailable answers 503 folder_unavailable with the reason.
func (a *API) folderUnavailable(w http.ResponseWriter) {
	msg := "The backup folder cannot be used. The server log says why."
	if a.o.FolderErr != nil {
		msg = message(a.o.FolderErr)
	}
	writeError(w, http.StatusServiceUnavailable, "folder_unavailable", msg)
}

// jobError answers an error of the service or the folder; it returns false
// for an error it does not know.
func (a *API) jobError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ErrBusy):
		writeError(w, http.StatusConflict, "busy", "A backup or restore is already running. Wait until it has finished.")
	case errors.Is(err, ErrInUse):
		writeError(w, http.StatusConflict, "in_use", sentence(err.Error()))
	case errors.Is(err, ErrKeyMismatch):
		writeError(w, http.StatusConflict, "key_mismatch", sentence(message(err)))
	case errors.Is(err, ErrNewerSchema):
		writeError(w, http.StatusConflict, "newer_schema", sentence(message(err)))
	case errors.Is(err, ErrNoSpace):
		writeError(w, http.StatusInsufficientStorage, "no_space", sentence(message(err)))
	case errors.Is(err, ErrFolder):
		if a.dir() == nil {
			a.folderUnavailable(w)
		} else {
			writeError(w, http.StatusServiceUnavailable, "folder_unavailable", sentence(message(err)))
		}
	case errors.Is(err, ErrNotBackup), errors.Is(err, ErrCorrupt), errors.Is(err, ErrFormat), errors.Is(err, ErrSignature):
		writeError(w, http.StatusUnprocessableEntity, "invalid_backup", sentence(message(err)))
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, "not_found", "No backup with this name.")
	case errors.Is(err, errNeedRestart), errors.Is(err, errClosed):
		writeError(w, http.StatusServiceUnavailable, "unavailable", sentence(err.Error()))
	default:
		return false
	}
	return true
}

// backupPath returns the path of the existing backup named in the request,
// answering 404 itself.
func (a *API) backupPath(w http.ResponseWriter, r *http.Request) (string, os.FileInfo, bool) {
	dir := a.dir()
	if dir == nil {
		a.folderUnavailable(w)
		return "", nil, false
	}
	p, err := dir.Path(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No backup with this name.")
		return "", nil, false
	}
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "not_found", "No backup with this name.")
		return "", nil, false
	}
	return p, fi, true
}

type diskJSON struct {
	FreeBytes uint64 `json:"free_bytes"`
	UsedBytes int64  `json:"used_bytes"`
}

type folderJSON struct {
	Path  string `json:"path"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type listJSON struct {
	Backups  []Info         `json:"backups"`
	Job      JobStatus      `json:"job"`
	Schedule ScheduleStatus `json:"schedule"`
	Disk     diskJSON       `json:"disk"`
	Folder   folderJSON     `json:"folder"`
}

func (a *API) list(w http.ResponseWriter, _ *http.Request) {
	out := listJSON{
		Backups: []Info{}, Job: a.svc.Status(), Schedule: a.svc.Schedule(),
		Folder: folderJSON{Path: a.o.FolderPath, OK: true},
	}
	fail := func(err error) {
		out.Folder.OK = false
		if out.Folder.Error == "" {
			out.Folder.Error = message(err)
		}
	}
	dir := a.dir()
	if dir == nil {
		err := a.o.FolderErr
		if err == nil {
			err = a.svc.usable()
		}
		fail(err)
		httpapi.WriteJSON(w, http.StatusOK, out)
		return
	}
	if list, err := dir.List(); err != nil {
		fail(err)
	} else {
		out.Backups = list
	}
	if free, err := dir.FreeBytes(); err != nil {
		fail(err)
	} else {
		out.Disk.FreeBytes = free
	}
	if used, err := dir.UsedBytes(); err == nil {
		out.Disk.UsedBytes = used
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	_, name := actor(r)
	if a.dir() == nil {
		a.folderUnavailable(w)
		return
	}
	if err := a.svc.CheckSpace(r.Context()); err != nil {
		if r.Context().Err() != nil {
			return
		}
		if !a.jobError(w, err) {
			a.internalError(w, r, "cannot estimate the size of a backup", err)
		}
		return
	}
	st, err := a.svc.StartBackup(KindManual, name)
	if err != nil {
		if !a.jobError(w, err) {
			a.internalError(w, r, "cannot start a backup", err)
		}
		return
	}
	httpapi.WriteJSON(w, http.StatusAccepted, st)
}

// deadlineWriter moves the write deadline of a download on as data is
// taken by the client, so a slow client is cut off only when it stops
// reading, not after a fixed time.
type deadlineWriter struct {
	http.ResponseWriter
	rc       *http.ResponseController
	since    int64     // bytes written since the deadline was moved
	movedAt  time.Time // when it was moved
	onHeader func(status int)
	sent     bool
}

// WriteHeader passes the status of the answer to onHeader, once.
func (d *deadlineWriter) WriteHeader(status int) {
	if !d.sent {
		d.sent = true
		if d.onHeader != nil {
			d.onHeader(status)
		}
	}
	d.ResponseWriter.WriteHeader(status)
}

func (d *deadlineWriter) extend() {
	d.since, d.movedAt = 0, time.Now()
	_ = d.rc.SetWriteDeadline(d.movedAt.Add(writeGrace))
}

func (d *deadlineWriter) Write(p []byte) (int, error) {
	if !d.sent {
		d.WriteHeader(http.StatusOK)
	}
	if d.since >= deadlineStep || time.Since(d.movedAt) > 5*time.Second {
		d.extend()
	}
	n, err := d.ResponseWriter.Write(p)
	d.since += int64(n)
	return n, err
}

func (a *API) download(w http.ResponseWriter, r *http.Request) {
	p, fi, ok := a.backupPath(w, r)
	if !ok {
		return
	}
	f, err := os.Open(p) //nolint:gosec // p comes from Dir.Path
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No backup with this name.")
		return
	}
	defer func() { _ = f.Close() }()
	name := fi.Name()
	_, who := actor(r)

	rc := http.NewResponseController(w)
	// The request has been read; a read deadline running out while the
	// file is sent would cancel the request.
	_ = rc.SetReadDeadline(time.Time{})
	dw := &deadlineWriter{ResponseWriter: w, rc: rc}
	// Every GET that sends content of the file is audited, with the bytes
	// sent as numbers and nothing taken from the request as it was written.
	// A HEAD, a 304 and a refused range send none and are not recorded.
	dw.onHeader = func(status int) {
		if r.Method != http.MethodGet {
			return
		}
		if detail, ok := downloadDetail(status, w.Header().Get("Content-Range"), r.Header.Get("Range"), name, fi.Size()); ok {
			a.o.Audit.Record(r.Context(), who, actionDownloaded, detail)
		}
	}
	dw.extend()
	h := w.Header()
	h.Set("Content-Type", "application/gzip")
	h.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	h.Set("Cache-Control", "no-store")
	http.ServeContent(dw, r, name, fi.ModTime(), f)
}

// downloadDetail is the audit detail of a download answered with status:
// the name and size of the file, and the first and last byte sent. A
// single range is read from the Content-Range the server wrote; for
// several ranges (a multipart answer, which has none) start and end span
// them all and ranges counts them. It reports false for an answer that
// sends no content of the file.
func downloadDetail(status int, contentRange, rangeHeader, name string, size int64) (map[string]any, bool) {
	detail := map[string]any{"name": name, "size": size}
	switch status {
	case http.StatusOK:
		detail["start"], detail["end"] = int64(0), size-1
	case http.StatusPartialContent:
		if start, end, ok := parseContentRange(contentRange); ok {
			detail["start"], detail["end"] = start, end
			break
		}
		spans := servedRanges(rangeHeader, size)
		if len(spans) == 0 { // not expected: the server sent a range
			detail["start"], detail["end"] = int64(0), size-1
			break
		}
		start, end := spans[0][0], spans[0][1]
		for _, s := range spans[1:] {
			start, end = min(start, s[0]), max(end, s[1])
		}
		detail["start"], detail["end"], detail["ranges"] = start, end, int64(len(spans))
	default:
		return nil, false
	}
	return detail, true
}

// parseContentRange reads "bytes <first>-<last>/<size>".
func parseContentRange(v string) (start, end int64, ok bool) {
	v, found := strings.CutPrefix(v, "bytes ")
	if !found {
		return 0, 0, false
	}
	v, _, found = strings.Cut(v, "/")
	if !found {
		return 0, 0, false
	}
	a, b, found := strings.Cut(v, "-")
	if !found {
		return 0, 0, false
	}
	start, err1 := strconv.ParseInt(a, 10, 64)
	end, err2 := strconv.ParseInt(b, 10, 64)
	return start, end, err1 == nil && err2 == nil && start >= 0 && end >= start
}

// servedRanges returns the byte ranges of a Range header of a file of size
// bytes, as first and last byte, by the rules net/http's ServeContent
// follows: "a-b", "a-" and the suffix "-n"; a range that starts past the
// end is left out. It returns nil for a header it cannot read.
func servedRanges(v string, size int64) [][2]int64 {
	v, ok := strings.CutPrefix(v, "bytes=")
	if !ok {
		return nil
	}
	var out [][2]int64
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		a, b, ok := strings.Cut(part, "-")
		if !ok {
			return nil
		}
		a, b = strings.TrimSpace(a), strings.TrimSpace(b)
		if a == "" {
			n, err := strconv.ParseInt(b, 10, 64)
			if err != nil || n < 0 {
				return nil
			}
			n = min(n, size)
			out = append(out, [2]int64{size - n, size - 1})
			continue
		}
		start, err := strconv.ParseInt(a, 10, 64)
		if err != nil || start < 0 {
			return nil
		}
		if start >= size {
			continue
		}
		end := size - 1
		if b != "" {
			if end, err = strconv.ParseInt(b, 10, 64); err != nil || end < start {
				return nil
			}
			end = min(end, size-1)
		}
		out = append(out, [2]int64{start, end})
	}
	return out
}

func (a *API) delete(w http.ResponseWriter, r *http.Request) {
	_, fi, ok := a.backupPath(w, r)
	if !ok {
		return
	}
	if err := a.svc.Delete(fi.Name()); err != nil {
		if !a.jobError(w, err) {
			a.internalError(w, r, "cannot delete a backup", err)
		}
		return
	}
	_, who := actor(r)
	a.o.Audit.Record(r.Context(), who, actionDeleted, map[string]any{"name": fi.Name(), "size": fi.Size()})
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) previewOf(ctx context.Context, path string) (Preview, error) {
	return PreviewRestore(ctx, path, a.svc.o.KeyFingerprint, a.svc.o.Dumper.SigningKey, a.o.RetentionDays(), a.o.Now())
}

// lasting reports whether a failed preview fails the same way for as long
// as the file stays the same.
func lasting(err error) bool {
	return errors.Is(err, ErrNotBackup) || errors.Is(err, ErrCorrupt) || errors.Is(err, ErrFormat) ||
		errors.Is(err, ErrSignature) || errors.Is(err, os.ErrNotExist)
}

// pruneLocked drops old finished previews and keeps at most maxPreviews of
// them. The caller holds pmu.
func (a *API) pruneLocked(now time.Time) {
	var oldest previewKey
	var oldestAt time.Time
	finished := 0
	for k, e := range a.previews {
		if !e.done {
			continue
		}
		if now.Sub(e.at) > previewKeep {
			delete(a.previews, k)
			continue
		}
		finished++
		if oldestAt.IsZero() || e.at.Before(oldestAt) {
			oldest, oldestAt = k, e.at
		}
	}
	if finished >= maxPreviews {
		delete(a.previews, oldest)
	}
}

// cachedPreviewError returns the lasting error of a finished preview of the
// backup at path as it is now, or nil.
func (a *API) cachedPreviewError(fi os.FileInfo) error {
	key := previewKey{fi.Name(), fi.Size(), fi.ModTime().UnixNano(), a.o.RetentionDays()}
	a.pmu.Lock()
	defer a.pmu.Unlock()
	if e := a.previews[key]; e != nil && e.done && lasting(e.err) {
		return e.err
	}
	return nil
}

// preview answers the preview of a backup. Reading a large backup in full
// takes longer than a proxy waits for an answer, so the preview is
// computed in the background, one check at a time, and kept while the file
// stays the same: the answer is 202 with the state checking until it is
// ready, then 200 with the preview.
func (a *API) preview(w http.ResponseWriter, r *http.Request) {
	p, fi, ok := a.backupPath(w, r)
	if !ok {
		return
	}
	key := previewKey{fi.Name(), fi.Size(), fi.ModTime().UnixNano(), a.o.RetentionDays()}
	now := a.o.Now()
	a.pmu.Lock()
	e := a.previews[key]
	if e != nil && e.done && now.Sub(e.at) > previewKeep {
		delete(a.previews, key)
		e = nil
	}
	if e == nil {
		a.pruneLocked(now)
		e = &previewEntry{}
		a.previews[key] = e
		ctx, stop := context.WithCancel(a.svc.baseContext())
		e.stop = stop
		err := a.svc.background(ctx, checkPreview, func() {
			// A panic ends the check as a failure too, which the next
			// request sees once before the file is checked again.
			pv, err := Preview{}, errInternal
			defer func() {
				stop()
				a.pmu.Lock()
				e.done, e.pv, e.err, e.at = true, pv, err, a.o.Now()
				a.pmu.Unlock()
			}()
			if ctx.Err() != nil {
				err = ctx.Err() // abandoned while it waited for its turn
				return
			}
			pv, err = a.computePreview(ctx, p)
		})
		if err != nil {
			stop()
			delete(a.previews, key)
			a.pmu.Unlock()
			a.jobError(w, err)
			return
		}
		go a.abandonPreview(ctx, key, e)
	}
	e.asked = now
	if !e.done {
		a.pmu.Unlock()
		httpapi.WriteJSON(w, http.StatusAccepted, checkingJSON{State: UploadChecking})
		return
	}
	pv, err := e.pv, e.err
	if err != nil && !lasting(err) {
		delete(a.previews, key) // the next request tries again
	}
	a.pmu.Unlock()
	if err != nil {
		if !a.jobError(w, err) {
			a.internalError(w, r, "cannot check a backup", err)
		}
		return
	}
	pv.Info = a.dir().info(fi.Name(), fi)
	httpapi.WriteJSON(w, http.StatusOK, pv)
}

// abandonPreview stops computing the preview e of key, and forgets it, once
// no request has asked for it for previewAbandon (the dialog that wanted it
// was closed), so that it does not hold up the checks after it. It returns
// when the preview has ended.
func (a *API) abandonPreview(ctx context.Context, key previewKey, e *previewEntry) {
	tick := time.NewTicker(max(a.previewAbandon/4, time.Millisecond))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		a.pmu.Lock()
		if !e.done && a.o.Now().Sub(e.asked) > a.previewAbandon {
			e.stop()
			if a.previews[key] == e {
				delete(a.previews, key)
			}
		}
		a.pmu.Unlock()
	}
}

// checkingJSON is the answer while a backup is being checked.
type checkingJSON struct {
	State UploadState `json:"state"`
}

// checkCode answers a refused two-factor code itself.
func (a *API) checkCode(w http.ResponseWriter, r *http.Request, p *auth.Principal, code string) bool {
	if code == "" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_code", "Enter a current two-factor code.")
		return false
	}
	err := a.o.CheckCode(r.Context(), a.o.ClientIP(r), p.UserID, code)
	switch {
	case err == nil:
		return true
	case errors.Is(err, auth.ErrInvalidCode):
		writeError(w, http.StatusUnprocessableEntity, "invalid_code",
			"The two-factor code is wrong or was already used. Enter a new code.")
	case errors.Is(err, auth.ErrLocked):
		// Too many wrong codes locked the second factor and ended the
		// user's sessions, this one too.
		writeError(w, http.StatusUnauthorized, "unauthenticated",
			"Too many wrong two-factor codes. You were signed out; sign in to continue.")
	case errors.Is(err, auth.ErrTooManyAttempts):
		w.Header().Set("Retry-After", strconv.Itoa(tooManyRetryAfter))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts",
			"Too many attempts. Wait "+strconv.Itoa(tooManyRetryAfter)+" seconds and try again.")
	default:
		a.internalError(w, r, "cannot check a two-factor code", err)
	}
	return false
}

func (a *API) restore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Confirm          string `json:"confirm"`
		Code             string `json:"code"`
		SkipSafetyBackup bool   `json:"skip_safety_backup"`
	}
	if !decode(w, r, &body, false, `Send a JSON object with "confirm", "code" and, if wanted, "skip_safety_backup".`) {
		return
	}
	if body.Confirm != confirmPhrase {
		writeError(w, http.StatusUnprocessableEntity, "confirmation_required", "Type RESTORE to confirm the restore.")
		return
	}
	_, fi, ok := a.backupPath(w, r)
	if !ok {
		return
	}
	// Everything that can refuse the restore is checked before the code,
	// so a refused restore does not use up a code.
	err := a.svc.CheckRestore(fi.Name())
	if err == nil {
		err = a.cachedPreviewError(fi)
	}
	if err != nil {
		if !a.jobError(w, err) {
			a.internalError(w, r, "cannot start a restore", err)
		}
		return
	}
	p, who := actor(r)
	if !a.checkCode(w, r, p, body.Code) {
		return
	}
	st, err := a.svc.StartRestore(fi.Name(), who, body.SkipSafetyBackup, true)
	if err != nil {
		if !a.jobError(w, err) {
			a.internalError(w, r, "cannot start a restore", err)
		}
		return
	}
	httpapi.WriteJSON(w, http.StatusAccepted, st)
}

func (a *API) restoreStatus(w http.ResponseWriter, _ *http.Request) {
	httpapi.WriteJSON(w, http.StatusOK, a.svc.RestoreStatus())
}

// dismissSchedule hides the banner about a failed scheduled backup until
// the next scheduled run. Audited when there was a failure to dismiss.
func (a *API) dismissSchedule(w http.ResponseWriter, r *http.Request) {
	st, changed := a.svc.DismissScheduleFailure()
	if changed {
		_, who := actor(r)
		a.o.Audit.Record(r.Context(), who, actionScheduleDismissed, map[string]any{"last_run_at": st.LastRunAt.UTC().Format(time.RFC3339), "error": st.Error})
	}
	w.WriteHeader(http.StatusNoContent)
}

// setupAllowed checks the setup token of a request of the setup screen and
// answers a refusal itself.
func (a *API) setupAllowed(w http.ResponseWriter, r *http.Request, token string) bool {
	if token == "" {
		token = r.Header.Get(setupTokenHeader)
	}
	err := a.o.CheckSetupToken(r.Context(), a.o.ClientIP(r), token)
	switch {
	case err == nil:
		return true
	case errors.Is(err, auth.ErrSetupDone):
		writeError(w, http.StatusConflict, "setup_done", "Setup is already complete. Sign in instead.")
	case errors.Is(err, auth.ErrInvalidToken):
		writeError(w, http.StatusForbidden, "invalid_setup_token", "The setup token is wrong. Copy it from the server log written at start.")
	case errors.Is(err, auth.ErrTooManyAttempts):
		w.Header().Set("Retry-After", strconv.Itoa(tooManyRetryAfter))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts",
			"Too many attempts. Wait "+strconv.Itoa(tooManyRetryAfter)+" seconds and try again.")
	default:
		a.internalError(w, r, "cannot check the setup token", err)
	}
	return false
}

// scopeAllowed checks the setup token for the setup scope; the router has
// checked the admin session for the admin scope.
func (a *API) scopeAllowed(w http.ResponseWriter, r *http.Request, scope uploadScope, token string) bool {
	return scope != scopeSetup || a.setupAllowed(w, r, token)
}

// uploadError answers an error of an upload; it returns false for an error
// it does not know.
func (a *API) uploadError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, errNoUpload):
		writeError(w, http.StatusNotFound, "not_found", "No upload with this id is in progress. Start the upload again.")
	case errors.Is(err, errTooManyUploads):
		writeError(w, http.StatusConflict, "too_many_uploads", sentence(err.Error()))
	case errors.Is(err, errUploadBusy):
		writeError(w, http.StatusConflict, "upload_busy", sentence(err.Error()))
	case errors.Is(err, ErrNoSpace):
		writeError(w, http.StatusInsufficientStorage, "no_space", sentence(err.Error()))
	default:
		return a.jobError(w, err)
	}
	return true
}

func (a *API) startUpload(w http.ResponseWriter, r *http.Request, scope uploadScope) {
	var body struct {
		Token    string  `json:"token"`
		FileName *string `json:"file_name"`
		Size     *int64  `json:"size"`
	}
	if !decode(w, r, &body, false, `Send a JSON object with "file_name" and "size".`) {
		return
	}
	if !a.scopeAllowed(w, r, scope, body.Token) {
		return
	}
	switch {
	case body.FileName == nil || body.Size == nil:
		writeError(w, http.StatusBadRequest, "invalid_request", `"file_name" and "size" are required.`)
		return
	case *body.Size < 1:
		writeError(w, http.StatusBadRequest, "invalid_request", "size must be the size of the file in bytes, at least 1.")
		return
	}
	if msg := checkFileName(*body.FileName); msg != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", msg)
		return
	}
	dir := a.dir()
	if dir == nil {
		a.folderUnavailable(w)
		return
	}
	st, err := a.up.start(dir, scope, *body.FileName, *body.Size, a.o.Now())
	if err != nil {
		if !a.uploadError(w, err) {
			a.internalError(w, r, "cannot start an upload", err)
		}
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, st)
}

func (a *API) getUpload(w http.ResponseWriter, r *http.Request, scope uploadScope) {
	if !a.scopeAllowed(w, r, scope, "") {
		return
	}
	st, err := a.up.status(scope, r.PathValue("id"), a.o.Now())
	if err != nil {
		a.uploadError(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, st)
}

func (a *API) cancelUpload(w http.ResponseWriter, r *http.Request, scope uploadScope) {
	if !a.scopeAllowed(w, r, scope, "") {
		return
	}
	if err := a.up.cancel(scope, r.PathValue("id"), a.o.Now()); err != nil {
		a.uploadError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// putPart writes one part of an upload at the offset of the query. The
// part must start where the received data ends and be at most ChunkSize
// bytes; it may take up to partTimeout to arrive. A part that fails is
// taken back out, so the client sends it again from the same offset.
func (a *API) putPart(w http.ResponseWriter, r *http.Request, scope uploadScope) {
	refuse := func(status int, code, msg string) {
		abortBody(w, r)
		writeError(w, status, code, msg)
	}
	if !a.scopeAllowed(w, r, scope, "") {
		abortBody(w, r)
		return
	}
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		refuse(http.StatusBadRequest, "invalid_request", "offset must be the number of bytes already received, 0 or more.")
		return
	}
	if r.ContentLength > ChunkSize {
		refuse(http.StatusRequestEntityTooLarge, "payload_too_large", fmt.Sprintf("A part may be at most %d bytes.", ChunkSize))
		return
	}
	up, err := a.up.acquire(scope, r.PathValue("id"), a.o.Now())
	if err != nil {
		abortBody(w, r)
		a.uploadError(w, err)
		return
	}
	received := up.received
	defer func() { a.up.release(up, received, a.o.Now()) }()
	if offset != received {
		abortBody(w, r)
		writeReceived(w, http.StatusConflict, "offset_mismatch",
			fmt.Sprintf("The server has %d bytes of this upload; send the part that starts there.", received), received)
		return
	}

	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(time.Now().Add(partTimeout)); err != nil {
		a.internalError(w, r, "cannot set the read deadline of an upload part", err)
		return
	}
	f, err := os.OpenFile(up.path, os.O_WRONLY, 0) //nolint:gosec // the name is a random id
	if err != nil {
		abortBody(w, r)
		if errors.Is(err, os.ErrNotExist) { // cancelled meanwhile
			a.uploadError(w, errNoUpload)
			return
		}
		a.internalError(w, r, "cannot open an upload", err)
		return
	}
	room := up.size - offset
	ew := &errWriter{w: io.NewOffsetWriter(f, offset)}
	n, copyErr := io.Copy(ew, io.LimitReader(http.MaxBytesReader(w, r.Body, ChunkSize), room+1))
	if copyErr == nil && n > room {
		copyErr = errPastEnd
	}
	if copyErr == nil {
		copyErr = f.Close()
		if copyErr != nil && ew.err == nil {
			ew.err = copyErr
		}
	} else {
		_ = f.Truncate(offset)
		_ = f.Close()
	}
	// The answer may come long after the server's write timeout began.
	_ = rc.SetWriteDeadline(time.Now().Add(writeGrace))
	if copyErr != nil {
		a.partError(w, r, ew.err, copyErr)
		return
	}
	received = offset + n
	httpapi.WriteJSON(w, http.StatusOK, UploadStatus{
		ID: up.id, FileName: up.fileName, Size: up.size, ChunkSize: ChunkSize, Received: received,
	})
}

var errPastEnd = errors.New("the part goes past the end of the file")

// partError answers a part that could not be written: writeErr is the
// error of the file, if any, and err the error of the copy.
func (a *API) partError(w http.ResponseWriter, r *http.Request, writeErr, err error) {
	var tooBig *http.MaxBytesError
	switch {
	case writeErr != nil && errors.Is(writeErr, syscall.ENOSPC):
		writeError(w, http.StatusInsufficientStorage, "no_space", "The backup folder is full. Free some space and send the part again.")
	case writeErr != nil:
		a.internalError(w, r, "cannot write an upload part", writeErr)
	case errors.As(err, &tooBig):
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", fmt.Sprintf("A part may be at most %d bytes.", ChunkSize))
	case errors.Is(err, errPastEnd):
		writeError(w, http.StatusBadRequest, "invalid_request", "The part goes past the size given when the upload started.")
	case errors.Is(err, os.ErrDeadlineExceeded):
		writeError(w, http.StatusRequestTimeout, "request_timeout", "The part did not arrive in time. Send it again.")
	default:
		// The client went away or sent less than it announced; nobody may
		// read this, and the part is sent again.
		writeError(w, http.StatusBadRequest, "invalid_request", "The part was cut off. Send it again.")
	}
}

// completeUpload starts the check of a fully received upload and answers
// 202 with the state checking; the client asks GET for the outcome. Reading
// a large file takes longer than a proxy waits for an answer, so the check
// runs in the background, one at a time. Completing again while it runs
// answers 202 again, and after it ended 200 with the outcome.
func (a *API) completeUpload(w http.ResponseWriter, r *http.Request, scope uploadScope) {
	var body struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &body, true, `Send an empty body or a JSON object with "token".`) {
		return
	}
	if !a.scopeAllowed(w, r, scope, body.Token) {
		return
	}
	dir := a.dir()
	if dir == nil {
		a.folderUnavailable(w)
		return
	}
	st, up, ctx, started, err := a.up.complete(a.svc.baseContext(), scope, r.PathValue("id"), a.o.Now())
	if err != nil {
		var inc *incompleteError
		if errors.As(err, &inc) {
			writeReceived(w, http.StatusConflict, "upload_incomplete", sentence(inc.Error()), inc.received)
			return
		}
		a.uploadError(w, err)
		return
	}
	if started {
		who := setupActor
		if scope == scopeAdmin {
			_, who = actor(r)
		}
		// The audit entry and the log keep the request's values, not its end.
		logCtx := context.WithoutCancel(r.Context())
		if err := a.svc.background(ctx, checkUpload, func() { a.checkUpload(logCtx, ctx, up, scope, who) }); err != nil {
			a.up.settle(up, a.o.Now(), func(u *upload) { u.state = UploadUploading })
			a.jobError(w, err)
			return
		}
	}
	status := http.StatusOK
	if st.State == UploadChecking {
		status = http.StatusAccepted
	}
	httpapi.WriteJSON(w, status, st)
}

// checkUpload checks a complete upload in the background and records the
// outcome: a file that passes is added to the folder as a backup of kind
// uploaded (with the preview of its restore for the setup screen), one
// that fails is deleted and the reason kept. A check stopped by a shutdown
// leaves the upload as it was.
func (a *API) checkUpload(logCtx, ctx context.Context, up *upload, scope uploadScope, who string) {
	var pv Preview
	err := a.svc.guard(logCtx, "checking an upload stopped with a panic", func() error {
		var err error
		pv, err = a.verify(ctx, up, scope)
		return err
	})
	var added *Info
	a.up.settle(up, a.o.Now(), func(u *upload) {
		if err != nil && a.svc.baseContext().Err() != nil {
			u.state = UploadUploading // shutting down: not the file's fault
			return
		}
		if err == nil {
			info, aerr := a.dir().adopt(u.path, u.fileName, a.o.Now())
			if aerr == nil {
				u.state, u.backup, added = UploadReady, &info, &info
				if scope == scopeSetup {
					pv.Info = info
					u.preview = &pv
				}
				return
			}
			err = aerr
		}
		_ = os.Remove(u.path) //nolint:gosec // the name is a random id
		reason := a.uploadReason(err)
		if reason.Code == "internal_error" {
			a.o.Log.ErrorContext(logCtx, "cannot check an upload", "error", err)
		}
		u.state, u.reason = UploadRefused, &reason
	})
	if added == nil {
		return
	}
	a.o.Audit.Record(logCtx, who, actionUploaded, map[string]any{"name": added.Name, "original_name": up.fileName, "size": added.Size})
	a.o.Log.InfoContext(logCtx, "backup uploaded", "name", added.Name, "size", added.Size, "actor", who)
}

// verifyUpload checks a complete upload in full: its manifest, every
// checksum and the signature, with a ceiling on how much it may expand to
// that fits its size. For the setup screen it returns the preview of the
// restore, which reads the file once and checks it the same way.
func (a *API) verifyUpload(ctx context.Context, up *upload, scope uploadScope) (Preview, error) {
	f, err := os.Open(up.path) //nolint:gosec // the name is a random id
	if err != nil {
		return Preview{}, err
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil {
		return Preview{}, a.dir().writeError(err)
	}
	m, err := ReadManifest(ctxReader{ctx, f})
	if err != nil {
		return Preview{}, err
	}
	if err := checkManifest(m, a.svc.o.KeyFingerprint); err != nil {
		return Preview{}, err
	}
	if scope == scopeSetup {
		return a.previewOf(ctx, up.path)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Preview{}, err
	}
	_, _, err = VerifyLimited(ctxReader{ctx, f}, a.svc.o.Dumper.SigningKey, nil, expansionLimit(up.size))
	return Preview{}, err
}

// uploadReason is the code and message an upload refused for err gets, as
// an error answer would carry them.
func (a *API) uploadReason(err error) UploadReason {
	reason := func(code string) UploadReason { return UploadReason{Code: code, Message: sentence(message(err))} }
	switch {
	case errors.Is(err, ErrKeyMismatch):
		return reason("key_mismatch")
	case errors.Is(err, ErrNewerSchema):
		return reason("newer_schema")
	case errors.Is(err, ErrNotBackup), errors.Is(err, ErrCorrupt), errors.Is(err, ErrFormat), errors.Is(err, ErrSignature):
		return reason("invalid_backup")
	case errors.Is(err, ErrNoSpace):
		return reason("no_space")
	case errors.Is(err, ErrFolder):
		return reason("folder_unavailable")
	}
	return UploadReason{Code: "internal_error", Message: "Something went wrong while the file was checked. Try the upload again later."}
}

// checkSetupToken tells whether the setup token is right, so the setup
// screen can ask for it before a large file is sent. It spends an attempt
// of the per-address limit on a wrong token, like every setup request.
func (a *API) checkSetupToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &body, false, `Send a JSON object with "token".`) {
		return
	}
	if !a.setupAllowed(w, r, body.Token) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) setupRestore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		UploadID string `json:"upload_id"`
		Confirm  string `json:"confirm"`
	}
	if !decode(w, r, &body, false, `Send a JSON object with "token", "upload_id" and "confirm".`) {
		return
	}
	if !a.setupAllowed(w, r, body.Token) {
		return
	}
	if body.Confirm != confirmPhrase {
		writeError(w, http.StatusUnprocessableEntity, "confirmation_required", "Type RESTORE to confirm the restore.")
		return
	}
	name, ok := a.up.backupOf(body.UploadID, a.o.Now())
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No completed upload with this id. Upload the backup first.")
		return
	}
	if _, err := a.svc.StartRestore(name, setupActor, true, false); err != nil {
		if !a.jobError(w, err) {
			a.internalError(w, r, "cannot start a restore", err)
		}
		return
	}
	httpapi.WriteJSON(w, http.StatusAccepted, a.svc.RestoreStatus())
}
