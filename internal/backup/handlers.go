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
	actionUploaded   = audit.ActionBackupUploaded
	actionDownloaded = audit.ActionBackupDownloaded
	actionDeleted    = audit.ActionBackupDeleted

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
	// an answer: a download, or the answer to a request that reads a whole
	// backup first, gets this much time again whenever it moves on.
	writeGrace = 60 * time.Second
	// deadlineStep is how much of a download may be written before the
	// write deadline is moved on; it is also moved on every few seconds.
	deadlineStep = 8 << 20
	// tooManyRetryAfter is the Retry-After, in seconds, of a code or token
	// refused for too many attempts.
	tooManyRetryAfter = 30
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
	return &API{o: o, svc: o.Service}
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
		"restoreSetupBackup":         http.HandlerFunc(a.setupRestore),
		"getRestoreStatus":           http.HandlerFunc(a.restoreStatus),
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
	msg := "The backup folder cannot be used; see the server log for the reason."
	if a.o.FolderErr != nil {
		msg = sentence(a.o.FolderErr.Error())
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
			out.Folder.Error = sentence(err.Error())
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
	// A download is audited once: when the whole file or a range from its
	// start is sent. Resuming it (a later range), a HEAD and a refused range
	// are not recorded.
	dw.onHeader = func(status int) {
		fromStart := status == http.StatusOK ||
			status == http.StatusPartialContent && strings.HasPrefix(r.Header.Get("Range"), "bytes=0-")
		if r.Method == http.MethodGet && fromStart {
			a.o.Audit.Record(r.Context(), who, actionDownloaded, map[string]any{"name": name, "size": fi.Size()})
		}
	}
	dw.extend()
	h := w.Header()
	h.Set("Content-Type", "application/gzip")
	h.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	h.Set("Cache-Control", "no-store")
	http.ServeContent(dw, r, name, fi.ModTime(), f)
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

// keepAnswering lets a request that reads a whole backup before it answers
// take as long as that takes: the read deadline is lifted (its end would
// cancel the request) and the write deadline is moved on until the
// returned function is called.
func keepAnswering(w http.ResponseWriter) (stop func()) {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Now().Add(writeGrace))
	done, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		t := time.NewTicker(writeGrace / 4)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				_ = rc.SetWriteDeadline(time.Now().Add(writeGrace))
			case <-done:
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-exited
			_ = rc.SetWriteDeadline(time.Now().Add(writeGrace))
		})
	}
}

func (a *API) previewOf(ctx context.Context, path string) (Preview, error) {
	return PreviewRestore(ctx, path, a.svc.o.KeyFingerprint, a.svc.o.Dumper.SigningKey, a.o.RetentionDays(), a.o.Now())
}

func (a *API) preview(w http.ResponseWriter, r *http.Request) {
	p, fi, ok := a.backupPath(w, r)
	if !ok {
		return
	}
	stop := keepAnswering(w)
	defer stop()
	pv, err := a.previewOf(r.Context(), p)
	stop()
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		if !a.jobError(w, err) {
			a.internalError(w, r, "cannot check a backup", err)
		}
		return
	}
	pv.Info = a.dir().info(fi.Name(), fi)
	httpapi.WriteJSON(w, http.StatusOK, pv)
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

// checkUpload reads the manifest of an upload and refuses a backup of
// another key or a newer version with the error the restore would give.
func (a *API) checkUpload(ctx context.Context, f *os.File) error {
	m, err := ReadManifest(ctxReader{ctx, f})
	if err != nil {
		return err
	}
	if err := checkManifest(m, a.svc.o.KeyFingerprint); err != nil {
		return err
	}
	_, err = f.Seek(0, io.SeekStart)
	return err
}

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
	up, err := a.up.acquire(scope, r.PathValue("id"), a.o.Now())
	if err != nil {
		a.uploadError(w, err)
		return
	}
	if up.received != up.size {
		a.up.release(up, up.received, a.o.Now())
		writeReceived(w, http.StatusConflict, "upload_incomplete",
			fmt.Sprintf("The server has %d of %d bytes; send the rest first.", up.received, up.size), up.received)
		return
	}
	stop := keepAnswering(w)
	defer stop()
	pv, err := a.verifyUpload(r.Context(), up, scope)
	stop()
	if err != nil {
		if r.Context().Err() != nil {
			// The client went away; the upload stays and can be completed again.
			a.up.release(up, up.received, a.o.Now())
			return
		}
		a.up.drop(up, false)
		a.refuseUpload(w, r, err)
		return
	}
	info, err := dir.adopt(up.path, up.fileName, a.o.Now())
	if err != nil {
		a.up.drop(up, false)
		if !a.jobError(w, err) {
			a.internalError(w, r, "cannot store an upload", err)
		}
		return
	}
	a.up.drop(up, true)
	who := setupActor
	if scope == scopeAdmin {
		_, who = actor(r)
	}
	a.o.Audit.Record(r.Context(), who, actionUploaded, map[string]any{"name": info.Name, "original_name": up.fileName, "size": info.Size})
	a.o.Log.InfoContext(r.Context(), "backup uploaded", "name", info.Name, "size", info.Size, "actor", who)
	if scope == scopeSetup {
		a.up.finished(up.id, info.Name)
		pv.Info = info
		httpapi.WriteJSON(w, http.StatusCreated, pv)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, info)
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
	if err := a.checkUpload(ctx, f); err != nil {
		return Preview{}, err
	}
	if scope == scopeSetup {
		return a.previewOf(ctx, up.path)
	}
	_, _, err = VerifyLimited(ctxReader{ctx, f}, a.svc.o.Dumper.SigningKey, nil, expansionLimit(up.size))
	return Preview{}, err
}

// refuseUpload answers an upload that failed its check.
func (a *API) refuseUpload(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrKeyMismatch):
		writeError(w, http.StatusUnprocessableEntity, "key_mismatch", sentence(message(err)))
	case errors.Is(err, ErrNewerSchema):
		writeError(w, http.StatusUnprocessableEntity, "newer_schema", sentence(message(err)))
	case errors.Is(err, ErrNotBackup), errors.Is(err, ErrCorrupt), errors.Is(err, ErrFormat), errors.Is(err, ErrSignature):
		writeError(w, http.StatusUnprocessableEntity, "invalid_backup", sentence(message(err)))
	default:
		if !a.jobError(w, err) {
			a.internalError(w, r, "cannot check an upload", err)
		}
	}
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
	name, ok := a.up.backupOf(body.UploadID)
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
