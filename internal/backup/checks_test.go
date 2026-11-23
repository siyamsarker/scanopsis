// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/store"
)

// gatedVerify replaces the check of uploads with one that waits until it is
// released (or its context ends), then runs the real check. It counts the
// checks that run at once.
type gatedVerify struct {
	release chan struct{}
	entered chan string
	mu      sync.Mutex
	active  int
	peak    int
}

func gateVerify(e *apiEnv) *gatedVerify {
	g := &gatedVerify{release: make(chan struct{}), entered: make(chan string, 16)}
	real := e.api.verify
	e.api.verify = func(ctx context.Context, up *upload, scope uploadScope) (Preview, error) {
		g.mu.Lock()
		g.active++
		g.peak = max(g.peak, g.active)
		g.mu.Unlock()
		defer func() {
			g.mu.Lock()
			g.active--
			g.mu.Unlock()
		}()
		g.entered <- up.id
		select {
		case <-g.release:
		case <-ctx.Done():
			return Preview{}, ctx.Err()
		}
		return real(ctx, up, scope)
	}
	return g
}

func (e *apiEnv) waitState(path, role, want string, hdr ...string) map[string]any {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		r := e.call("GET", path, role, nil, hdr...)
		if r.status != http.StatusOK {
			e.t.Fatalf("GET %s = %d %s", path, r.status, r.body)
		}
		m := r.json(e.t)
		if m["state"] == want {
			return m
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("state = %v, want %s", m, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Reading a large backup takes longer than a proxy waits for an answer, so
// completing an upload answers 202 at once and the check runs in the
// background; the client asks for the outcome.
func TestUploadCheckRunsInBackground(t *testing.T) {
	e := newAPIEnv(t, nil)
	g := gateVerify(e)
	data := goodArchive(t)
	id := e.startUpload("nightly.tar.gz", len(data))
	e.send(id, data, 0, len(data))
	path := adminUploads + "/" + id

	r := e.call("POST", path+"/complete", "admin", nil)
	if m := r.json(t); r.status != http.StatusAccepted || m["state"] != "checking" || m["received"] != float64(len(data)) || m["id"] != id {
		t.Fatalf("complete = %d %s, want 202 checking", r.status, r.body)
	}
	<-g.entered
	// Completing again while the check runs is the same answer, and starts
	// no second check.
	if r := e.call("POST", path+"/complete", "admin", nil); r.status != http.StatusAccepted || r.json(t)["state"] != "checking" {
		t.Errorf("complete again = %d %s", r.status, r.body)
	}
	if r := e.call("GET", path, "admin", nil); r.status != http.StatusOK || r.json(t)["state"] != "checking" {
		t.Errorf("status while checking = %d %s", r.status, r.body)
	}
	if r := e.put(id, len(data), []byte("x")); r.status != http.StatusConflict || r.errorCode(t) != "upload_busy" {
		t.Errorf("PUT while checking = %d %s", r.status, r.body)
	}
	if len(e.list()) != 0 {
		t.Error("the backup was listed before its check ended")
	}
	close(g.release)
	m := e.waitState(path, "admin", "ready")
	b, _ := m["backup"].(map[string]any)
	name, _ := b["name"].(string)
	if !strings.Contains(name, "-uploaded-") || b["original_name"] != "nightly.tar.gz" || m["preview"] != nil || m["reason"] != nil {
		t.Errorf("outcome = %v", m)
	}
	if list := e.list(); len(list) != 1 || list[0].Name != name {
		t.Errorf("list = %+v", list)
	}
	if ev := e.audit.of(actionUploaded); len(ev) != 1 || ev[0].actor != adminActor || ev[0].detail["name"] != name {
		t.Errorf("audit = %+v", ev)
	}
	select {
	case id := <-g.entered:
		t.Errorf("a second check of %s ran", id)
	default:
	}
}

// The server checks one file at a time, whatever the number of uploads.
func TestUploadChecksOneAtATime(t *testing.T) {
	e := newAPIEnv(t, nil)
	g := gateVerify(e)
	a, b := goodArchive(t), goodArchive(t)
	ida, idb := e.startUpload("a.tar.gz", len(a)), e.startUpload("b.tar.gz", len(b))
	e.send(ida, a, 0, len(a))
	e.send(idb, b, 0, len(b))
	for _, id := range []string{ida, idb} {
		if r := e.call("POST", adminUploads+"/"+id+"/complete", "admin", nil); r.status != http.StatusAccepted {
			t.Fatalf("complete %s = %d %s", id, r.status, r.body)
		}
	}
	first := <-g.entered
	select {
	case second := <-g.entered:
		t.Fatalf("%s was checked while %s was", second, first)
	case <-time.After(100 * time.Millisecond):
	}
	close(g.release)
	for _, id := range []string{ida, idb} {
		e.waitState(adminUploads+"/"+id, "admin", "ready")
	}
	if g.peak != 1 {
		t.Errorf("%d checks ran at once, want 1", g.peak)
	}
}

// Cancelling an upload while it is checked stops the check and deletes it.
func TestUploadCancelWhileChecking(t *testing.T) {
	e := newAPIEnv(t, nil)
	g := gateVerify(e)
	data := goodArchive(t)
	id := e.startUpload("a.tar.gz", len(data))
	e.send(id, data, 0, len(data))
	if r := e.call("POST", adminUploads+"/"+id+"/complete", "admin", nil); r.status != http.StatusAccepted {
		t.Fatalf("complete = %d %s", r.status, r.body)
	}
	<-g.entered
	if r := e.call("DELETE", adminUploads+"/"+id, "admin", nil); r.status != http.StatusNoContent {
		t.Fatalf("cancel while checking = %d %s", r.status, r.body)
	}
	if r := e.call("GET", adminUploads+"/"+id, "admin", nil); r.status != http.StatusNotFound {
		t.Errorf("a cancelled upload = %d %s", r.status, r.body)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(uploadFiles(t, e.dir)) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the file of a cancelled check stays: %v", uploadFiles(t, e.dir))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if list := e.list(); len(list) != 0 {
		t.Errorf("a cancelled upload was added: %+v", list)
	}
	if ev := e.audit.of(actionUploaded); len(ev) != 0 {
		t.Errorf("audit = %+v", ev)
	}
}

// The setup screen gets the preview of the restore with the outcome.
func TestSetupUploadCheckRunsInBackground(t *testing.T) {
	e := newAPIEnv(t, nil)
	const base = "/api/v1/setup/restore/uploads"
	data := csvArchive(t)
	r := e.call("POST", base, "", map[string]any{"token": "setup-token", "file_name": "old.tar.gz", "size": len(data)})
	if r.status != http.StatusCreated {
		t.Fatalf("start = %d %s", r.status, r.body)
	}
	id := r.json(t)["id"].(string)
	if r := e.call("PUT", base+"/"+id+"?offset=0", "", data, "X-Setup-Token", "setup-token"); r.status != http.StatusOK {
		t.Fatalf("PUT = %d %s", r.status, r.body)
	}
	m := e.complete(base+"/"+id, "", map[string]any{"token": "setup-token"}, "X-Setup-Token", "setup-token")
	pv, _ := m["preview"].(map[string]any)
	info, _ := pv["info"].(map[string]any)
	b, _ := m["backup"].(map[string]any)
	if m["state"] != "ready" || pv["key_matches"] != true || pv["schema_ok"] != true || info["original_name"] != "old.tar.gz" || b["name"] != info["name"] {
		t.Fatalf("outcome = %v", m)
	}
	if ev := e.audit.of(actionUploaded); len(ev) != 1 || ev[0].actor != setupActor {
		t.Errorf("audit = %+v", ev)
	}
	if name, ok := e.api.up.backupOf(id, time.Now()); !ok || name != b["name"] {
		t.Errorf("backupOf = %q %v", name, ok)
	}
}

// An upload that nobody continues for ten minutes makes room for a new one;
// before that, the refusal says when a slot frees up.
func TestUploadReplacesAnIdleOne(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	e := newAPIEnv(t, func(o *APIOptions) {
		o.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	})
	advance := func(d time.Duration) {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
	}
	a := e.startUpload("a.tar.gz", 100)
	advance(2 * time.Minute)
	b := e.startUpload("b.tar.gz", 100)
	advance(4 * time.Minute)
	r := e.call("POST", adminUploads, "admin", map[string]any{"file_name": "c.tar.gz", "size": 100})
	if r.status != http.StatusConflict || r.errorCode(t) != "too_many_uploads" || !strings.Contains(r.errorMessage(t), "try again in 4 minutes") {
		t.Fatalf("third upload after 6 minutes = %d %s", r.status, r.body)
	}
	advance(4*time.Minute + time.Second)
	c := e.startUpload("c.tar.gz", 100)
	if r := e.call("GET", adminUploads+"/"+a, "admin", nil); r.status != http.StatusNotFound {
		t.Errorf("the idle upload was kept: %d", r.status)
	}
	for _, id := range []string{b, c} {
		if r := e.call("GET", adminUploads+"/"+id, "admin", nil); r.status != http.StatusOK {
			t.Errorf("upload %s = %d", id, r.status)
		}
	}
	if left := uploadFiles(t, e.dir); len(left) != 2 {
		t.Errorf("upload folder = %v, want two files", left)
	}
	// The setup screen has slots of its own.
	if r := e.call("POST", "/api/v1/setup/restore/uploads", "", map[string]any{"token": "setup-token", "file_name": "d.tar.gz", "size": 100}); r.status != http.StatusCreated {
		t.Errorf("setup upload = %d %s", r.status, r.body)
	}
}

// Uploads being checked are never replaced.
func TestUploadsBeingCheckedStay(t *testing.T) {
	e := newAPIEnv(t, nil)
	g := gateVerify(e)
	defer close(g.release)
	for _, name := range []string{"a.tar.gz", "b.tar.gz"} {
		data := goodArchive(t)
		id := e.startUpload(name, len(data))
		e.send(id, data, 0, len(data))
		if r := e.call("POST", adminUploads+"/"+id+"/complete", "admin", nil); r.status != http.StatusAccepted {
			t.Fatalf("complete = %d %s", r.status, r.body)
		}
	}
	r := e.call("POST", adminUploads, "admin", map[string]any{"file_name": "c.tar.gz", "size": 100})
	if r.status != http.StatusConflict || !strings.Contains(r.errorMessage(t), "when a check has finished") {
		t.Errorf("third upload = %d %s", r.status, r.body)
	}
}

// writeBackup puts data into the folder as a backup made now.
func writeBackup(t *testing.T, d *Dir, data []byte) string {
	t.Helper()
	name := d.NewName(KindManual, time.Now())
	p, err := d.Path(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

// The preview reads the whole file, so it is computed in the background and
// kept while the file stays the same.
func TestPreviewInBackground(t *testing.T) {
	e := newAPIEnv(t, nil)
	name := writeBackup(t, e.dir, csvArchive(t))
	release := make(chan struct{})
	var calls atomic.Int32
	real := e.api.previewOf
	e.api.computePreview = func(ctx context.Context, path string) (Preview, error) {
		calls.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
			return Preview{}, ctx.Err()
		}
		return real(ctx, path)
	}
	path := "/api/v1/admin/backups/" + name + "/preview"
	for range 2 {
		r := e.call("GET", path, "admin", nil)
		if m := r.json(t); r.status != http.StatusAccepted || m["state"] != "checking" || len(m) != 1 {
			t.Fatalf("preview while checking = %d %s", r.status, r.body)
		}
	}
	close(release)
	var r response
	deadline := time.Now().Add(10 * time.Second)
	for r = e.call("GET", path, "admin", nil); r.status == http.StatusAccepted; r = e.call("GET", path, "admin", nil) {
		if time.Now().After(deadline) {
			t.Fatal("the preview did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	m := r.json(t)
	info, _ := m["info"].(map[string]any)
	if r.status != http.StatusOK || m["key_matches"] != true || m["schema_ok"] != true || info["name"] != name {
		t.Fatalf("preview = %d %s", r.status, r.body)
	}
	if r := e.call("GET", path, "admin", nil); r.status != http.StatusOK || calls.Load() != 1 {
		t.Errorf("second preview = %d, %d computations, want 200 from the first", r.status, calls.Load())
	}
	// A changed file is checked again.
	p, _ := e.dir.Path(name)
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	if r := e.call("GET", path, "admin", nil); r.status != http.StatusAccepted || calls.Load() != 2 {
		t.Errorf("preview of a changed file = %d, %d computations", r.status, calls.Load())
	}
}

// A damaged file fails the preview, and keeps failing without being read
// again while it stays the same.
func TestPreviewOfADamagedFile(t *testing.T) {
	e := newAPIEnv(t, nil)
	good := goodArchive(t)
	name := writeBackup(t, e.dir, good[:len(good)/2])
	path := "/api/v1/admin/backups/" + name + "/preview"
	var r response
	deadline := time.Now().Add(10 * time.Second)
	for r = e.call("GET", path, "admin", nil); r.status == http.StatusAccepted; r = e.call("GET", path, "admin", nil) {
		if time.Now().After(deadline) {
			t.Fatal("the preview did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r.status != http.StatusUnprocessableEntity || r.errorCode(t) != "invalid_backup" {
		t.Fatalf("preview = %d %s", r.status, r.body)
	}
	e.api.computePreview = func(context.Context, string) (Preview, error) {
		t.Error("the damaged file was read again")
		return Preview{}, errors.New("read again")
	}
	if r := e.call("GET", path, "admin", nil); r.status != http.StatusUnprocessableEntity {
		t.Errorf("second preview = %d %s", r.status, r.body)
	}
}

// Everything that can refuse a restore is checked before the two-factor
// code, so a refused restore does not use one up.
func TestRestoreChecksBeforeTheCode(t *testing.T) {
	var codes atomic.Int32
	e := newAPIEnv(t, func(o *APIOptions) {
		o.CheckCode = func(context.Context, netip.Addr, int64, string) error {
			codes.Add(1)
			return errors.New("no restore in this test")
		}
	})
	good := writeBackup(t, e.dir, goodArchive(t))
	other := writeBackup(t, e.dir, uploadArchive(t, "other-fingerprint", testSigningKey, store.LatestVersion()))
	newer := writeBackup(t, e.dir, uploadArchive(t, apiFingerprint, testSigningKey, store.LatestVersion()+1))
	full := goodArchive(t)
	damaged := writeBackup(t, e.dir, full[:len(full)/2])
	restore := func(name string) response {
		return e.call("POST", "/api/v1/admin/backups/"+name+"/restore", "admin", map[string]any{"confirm": "RESTORE", "code": "123456"})
	}

	for _, c := range []struct {
		name   string
		status int
		code   string
	}{
		{other, http.StatusConflict, "key_mismatch"},
		{newer, http.StatusConflict, "newer_schema"},
		{"scanopsis-backup-20261123T100000Z-manual-abcdef.tar.gz", http.StatusNotFound, "not_found"},
	} {
		if r := restore(c.name); r.status != c.status || r.errorCode(t) != c.code {
			t.Errorf("restore %s = %d %s, want %d %s", c.name, r.status, r.body, c.status, c.code)
		}
	}
	// A preview that found the file damaged refuses the restore too.
	pdeadline := time.Now().Add(10 * time.Second)
	for e.call("GET", "/api/v1/admin/backups/"+damaged+"/preview", "admin", nil).status == http.StatusAccepted {
		if time.Now().After(pdeadline) {
			t.Fatal("the preview did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r := restore(damaged); r.status != http.StatusUnprocessableEntity || r.errorCode(t) != "invalid_backup" {
		t.Errorf("restore of a damaged file = %d %s", r.status, r.body)
	}
	// A job is running.
	if _, err := e.svc.begin(JobStatus{State: JobBackingUp, Type: JobTypeBackup, Kind: KindManual}); err != nil {
		t.Fatal(err)
	}
	if r := restore(good); r.status != http.StatusConflict || r.errorCode(t) != "busy" {
		t.Errorf("restore while busy = %d %s", r.status, r.body)
	}
	e.svc.finish(func(st *JobStatus) { st.State = JobDone })
	// Scanopsis waits for a restart.
	e.svc.needRestart.Store(true)
	if r := restore(good); r.status != http.StatusServiceUnavailable || r.errorCode(t) != "unavailable" {
		t.Errorf("restore that waits for a restart = %d %s", r.status, r.body)
	}
	e.svc.needRestart.Store(false)
	if n := codes.Load(); n != 0 {
		t.Errorf("%d codes were checked for refused restores", n)
	}
	// Only a restore that can start checks the code.
	restore(good)
	if n := codes.Load(); n != 1 {
		t.Errorf("%d codes checked, want 1", n)
	}
}

// The banner about a failed scheduled backup can be dismissed until the
// next scheduled run.
func TestDismissScheduleFailure(t *testing.T) {
	e := newAPIEnv(t, nil)
	schedule := func() map[string]any {
		t.Helper()
		r := e.call("GET", "/api/v1/admin/backups", "admin", nil)
		s, _ := r.json(t)["schedule"].(map[string]any)
		return s
	}
	const dismiss = "/api/v1/admin/backups/schedule-status/dismiss"
	// Nothing to dismiss.
	if r := e.call("POST", dismiss, "admin", nil); r.status != http.StatusNoContent {
		t.Fatalf("dismiss without a failure = %d %s", r.status, r.body)
	}
	at := time.Date(2026, 11, 23, 2, 0, 0, 0, time.UTC)
	e.svc.recordSchedule(at, fmt.Errorf("take backup: %w", &spaceError{need: 2 << 30, have: 1 << 30}))
	if s := schedule(); s["ok"] != false || s["dismissed"] != false || s["enabled"] != false || s["error"] != "Not enough free space for a backup: a backup needs about 2.0 GiB and the backup folder has 1.0 GiB free." {
		t.Fatalf("schedule = %v", s)
	}
	for range 2 {
		if r := e.call("POST", dismiss, "admin", nil); r.status != http.StatusNoContent {
			t.Fatalf("dismiss = %d %s", r.status, r.body)
		}
	}
	if s := schedule(); s["dismissed"] != true || s["ok"] != false {
		t.Errorf("schedule after dismiss = %v", s)
	}
	if ev := e.audit.of(actionScheduleDismissed); len(ev) != 1 || ev[0].actor != adminActor ||
		ev[0].detail["last_run_at"] != "2026-11-23T02:00:00Z" || ev[0].detail["error"] != "Not enough free space for a backup: a backup needs about 2.0 GiB and the backup folder has 1.0 GiB free." {
		t.Errorf("audit = %+v", ev)
	}
	// It is kept in the folder.
	b, err := os.ReadFile(filepath.Join(e.dir.Root(), statusFile))
	var kept ScheduleStatus
	if err != nil || json.Unmarshal(b, &kept) != nil || !kept.Dismissed {
		t.Errorf("status file = %s (%v)", b, err)
	}
	again := NewService(ServiceOptions{Dir: e.dir, Log: discard})
	t.Cleanup(again.Close)
	if st := again.Schedule(); !st.Dismissed {
		t.Errorf("status after a restart = %+v", st)
	}
	// The next failure shows again.
	e.svc.recordSchedule(at.Add(24*time.Hour), errors.New("take backup: the disk is full"))
	if s := schedule(); s["dismissed"] != false {
		t.Errorf("schedule after a new failure = %v", s)
	}
}

// A backup from a newer version is listed as such, not as unreadable.
func TestListNewerVersion(t *testing.T) {
	e := newAPIEnv(t, nil)
	newer := writeBackup(t, e.dir, uploadArchive(t, apiFingerprint, testSigningKey, store.LatestVersion()+1))
	format := writeBackup(t, e.dir, rewriteManifest(t, goodArchive(t), func(m map[string]any) { m["format"] = FormatVersion + 1 }))
	broken := writeBackup(t, e.dir, []byte("not a backup"))
	current := writeBackup(t, e.dir, goodArchive(t))
	r := e.call("GET", "/api/v1/admin/backups", "admin", nil)
	got := map[string]map[string]any{}
	for _, b := range r.json(t)["backups"].([]any) {
		m := b.(map[string]any)
		got[m["name"].(string)] = m
	}
	for _, c := range []struct {
		name            string
		readable, newer bool
	}{
		{newer, true, true}, {format, false, true}, {broken, false, false}, {current, true, false},
	} {
		if m := got[c.name]; m["readable"] != c.readable || m["newer_version"] != c.newer {
			t.Errorf("%s: readable %v, newer_version %v; want %v, %v", c.name, m["readable"], m["newer_version"], c.readable, c.newer)
		}
	}
}

// rewriteManifest returns archive with its manifest changed by edit.
func rewriteManifest(t *testing.T, archive []byte, edit func(m map[string]any)) []byte {
	t.Helper()
	es := unpack(t, archive)
	var m map[string]any
	if err := json.Unmarshal(es[0].body, &m); err != nil {
		t.Fatal(err)
	}
	edit(m)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	es[0].body = b
	return pack(t, es)
}

// csvArchive is a signed backup of this server's key whose table holds
// rows of CSV, so a preview can count them.
func csvArchive(t *testing.T) []byte {
	t.Helper()
	m := Manifest{
		AppVersion: "0.4.0", AppCommit: "abc1234", Schema: store.LatestVersion(),
		CreatedAt: time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC), Kind: KindScheduled, CreatedBy: ScheduleActor,
		KeyFingerprint: apiFingerprint,
		Tables:         []TableSpec{{Name: "services", Columns: []string{"id", "name"}}},
	}
	var rows strings.Builder
	for i := range 5000 {
		fmt.Fprintf(&rows, "%d,payments-api-%d\n", i+1, i)
	}
	var buf bytes.Buffer
	data := map[string][]byte{"services": []byte(rows.String())}
	if _, err := WriteArchive(context.Background(), &buf, m, testSigningKey, dataCopier(data), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pollPreview asks for the preview of name until the answer is not 202.
func (e *apiEnv) pollPreview(name string) response {
	e.t.Helper()
	path := "/api/v1/admin/backups/" + name + "/preview"
	deadline := time.Now().Add(10 * time.Second)
	for {
		r := e.call("GET", path, "admin", nil)
		if r.status != http.StatusAccepted {
			return r
		}
		if time.Now().After(deadline) {
			e.t.Fatal("the preview did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A preview that panics ends as an error, and the next request checks the
// file again instead of waiting for ever.
func TestPreviewPanic(t *testing.T) {
	e := newAPIEnv(t, nil)
	name := writeBackup(t, e.dir, csvArchive(t))
	real := e.api.previewOf
	var calls atomic.Int32
	e.api.computePreview = func(ctx context.Context, path string) (Preview, error) {
		if calls.Add(1) == 1 {
			panic("boom")
		}
		return real(ctx, path)
	}
	if r := e.pollPreview(name); r.status != http.StatusInternalServerError {
		t.Fatalf("preview that panicked = %d %s, want 500", r.status, r.body)
	}
	if r := e.pollPreview(name); r.status != http.StatusOK || calls.Load() != 2 {
		t.Errorf("preview after the panic = %d %s, %d computations", r.status, r.body, calls.Load())
	}
}

// A preview nobody asks about any more is stopped and forgotten, so it
// does not keep the server reading a file for nobody.
func TestPreviewAbandoned(t *testing.T) {
	e := newAPIEnv(t, nil)
	e.api.previewAbandon = 100 * time.Millisecond
	name := writeBackup(t, e.dir, csvArchive(t))
	stopped := make(chan struct{})
	e.api.computePreview = func(ctx context.Context, _ string) (Preview, error) {
		<-ctx.Done()
		close(stopped)
		return Preview{}, ctx.Err()
	}
	path := "/api/v1/admin/backups/" + name + "/preview"
	if r := e.call("GET", path, "admin", nil); r.status != http.StatusAccepted {
		t.Fatalf("preview = %d %s", r.status, r.body)
	}
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("an abandoned preview kept running")
	}
	e.api.pmu.Lock()
	left := len(e.api.previews)
	e.api.pmu.Unlock()
	if left != 0 {
		t.Errorf("%d previews kept, want none", left)
	}

	// One that is asked about keeps running.
	release := make(chan struct{})
	var cancelled atomic.Bool
	e.api.computePreview = func(ctx context.Context, _ string) (Preview, error) {
		select {
		case <-release:
		case <-ctx.Done():
			cancelled.Store(true)
			return Preview{}, ctx.Err()
		}
		return Preview{SchemaOK: true, KeyMatches: true}, nil
	}
	for end := time.Now().Add(400 * time.Millisecond); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if r := e.call("GET", path, "admin", nil); r.status != http.StatusAccepted {
			t.Fatalf("preview while asked about = %d %s", r.status, r.body)
		}
	}
	close(release)
	if r := e.pollPreview(name); r.status != http.StatusOK || cancelled.Load() {
		t.Errorf("preview = %d %s, cancelled %v", r.status, r.body, cancelled.Load())
	}
}

// A preview that waits for its turn and is abandoned meanwhile never reads
// the file.
func TestPreviewAbandonedWhileWaiting(t *testing.T) {
	e := newAPIEnv(t, nil)
	e.api.previewAbandon = 100 * time.Millisecond
	g := gateVerify(e)
	data := goodArchive(t)
	id := e.startUpload("a.tar.gz", len(data))
	e.send(id, data, 0, len(data))
	if r := e.call("POST", adminUploads+"/"+id+"/complete", "admin", nil); r.status != http.StatusAccepted {
		t.Fatalf("complete = %d %s", r.status, r.body)
	}
	<-g.entered
	name := writeBackup(t, e.dir, csvArchive(t))
	var calls atomic.Int32
	e.api.computePreview = func(context.Context, string) (Preview, error) {
		calls.Add(1)
		return Preview{}, nil
	}
	if r := e.call("GET", "/api/v1/admin/backups/"+name+"/preview", "admin", nil); r.status != http.StatusAccepted {
		t.Fatalf("preview = %d %s", r.status, r.body)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		e.api.pmu.Lock()
		left := len(e.api.previews)
		e.api.pmu.Unlock()
		if left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the abandoned preview was kept")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(g.release)
	e.waitState(adminUploads+"/"+id, "admin", "ready")
	time.Sleep(50 * time.Millisecond)
	if calls.Load() != 0 {
		t.Errorf("an abandoned preview read the file %d times", calls.Load())
	}
}

// When the check slot frees up, an upload that waits goes before a
// preview that waits, so a preview never holds up an upload for long.
func TestUploadChecksGoBeforePreviews(t *testing.T) {
	e := newAPIEnv(t, nil)
	g := gateVerify(e)
	a, b := goodArchive(t), goodArchive(t)
	ida, idb := e.startUpload("a.tar.gz", len(a)), e.startUpload("b.tar.gz", len(b))
	e.send(ida, a, 0, len(a))
	e.send(idb, b, 0, len(b))
	if r := e.call("POST", adminUploads+"/"+ida+"/complete", "admin", nil); r.status != http.StatusAccepted {
		t.Fatalf("complete = %d %s", r.status, r.body)
	}
	<-g.entered
	var mu sync.Mutex
	var order []string
	name := writeBackup(t, e.dir, csvArchive(t))
	e.api.computePreview = func(context.Context, string) (Preview, error) {
		mu.Lock()
		order = append(order, "preview")
		mu.Unlock()
		return Preview{SchemaOK: true, KeyMatches: true}, nil
	}
	if r := e.call("GET", "/api/v1/admin/backups/"+name+"/preview", "admin", nil); r.status != http.StatusAccepted {
		t.Fatalf("preview = %d %s", r.status, r.body)
	}
	time.Sleep(50 * time.Millisecond) // the preview waits first
	if r := e.call("POST", adminUploads+"/"+idb+"/complete", "admin", nil); r.status != http.StatusAccepted {
		t.Fatalf("complete = %d %s", r.status, r.body)
	}
	time.Sleep(50 * time.Millisecond)
	go func() {
		for id := range g.entered {
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
		}
	}()
	close(g.release)
	e.waitState(adminUploads+"/"+idb, "admin", "ready")
	if r := e.pollPreview(name); r.status != http.StatusOK {
		t.Fatalf("preview = %d %s", r.status, r.body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != idb || order[1] != "preview" {
		t.Errorf("order after the first check = %v, want [%s preview]", order, idb)
	}
}
