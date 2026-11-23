// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

// throttled reads from r at about rate bytes per second.
type throttled struct {
	r     io.Reader
	rate  float64
	start time.Time
	sent  int
}

func (t *throttled) Read(p []byte) (int, error) {
	if t.start.IsZero() {
		t.start = time.Now()
	}
	p = p[:min(len(p), 16<<10)]
	n, err := t.r.Read(p)
	t.sent += n
	if wait := time.Until(t.start.Add(time.Duration(float64(t.sent) / t.rate * float64(time.Second)))); wait > 0 {
		time.Sleep(wait)
	}
	return n, err
}

// A part of an upload that takes longer than the server's read timeout to
// arrive is still received in full.
func TestSlowChunkNotCutOff(t *testing.T) {
	t.Parallel()
	e := newSvcEnv(t, nil)
	api := serveAPI(t, e.svc, nil, true)
	data := randomBytes(3, ChunkSize)
	id := api.startUpload("slow.tar.gz", len(data))
	start := time.Now()
	r := api.call("PUT", fmt.Sprintf("%s/%s?offset=0", adminUploads, id), "admin",
		&throttled{r: bytes.NewReader(data), rate: float64(len(data)) / 40})
	if r.status != http.StatusOK || r.json(t)["received"] != float64(len(data)) {
		t.Fatalf("slow part = %d %s", r.status, r.body)
	}
	if took := time.Since(start); took < 35*time.Second {
		t.Fatalf("the part arrived in %v; the test needs it to take longer than the read timeout", took)
	}
}

// A download that the client reads more slowly than the server's write
// timeout allows still arrives in full, and a Range request resumes it.
func TestDownloadLongerThanWriteTimeout(t *testing.T) {
	t.Parallel()
	e := newSvcEnv(t, nil)
	api := serveAPI(t, e.svc, nil, true)
	data := randomBytes(5, 48<<20)
	name := e.dir.NewName(KindManual, time.Now())
	if err := os.WriteFile(filepath.Join(e.dir.Root(), name), data, 0o600); err != nil {
		t.Fatal(err)
	}
	// A small receive buffer, so the server's writes wait for the reader
	// instead of filling the buffers of the system.
	client := &http.Client{Transport: &http.Transport{DialContext: (&net.Dialer{
		Control: func(_, _ string, c syscall.RawConn) error {
			var serr error
			if err := c.Control(func(fd uintptr) {
				serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 64<<10) //nolint:gosec // a socket descriptor fits an int
			}); err != nil {
				return err
			}
			return serr
		},
	}).DialContext}}
	get := func(hdr ...string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(context.Background(), "GET", api.url+"/api/v1/admin/backups/"+name+"/download", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(roleHeader, "admin")
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		return client.Do(req)
	}

	start := time.Now()
	resp, err := get()
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(&throttled{r: resp.Body, rate: float64(len(data)) / 72})
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || !bytes.Equal(got, data) {
		t.Fatalf("slow download: status %d, %d of %d bytes, err %v", resp.StatusCode, len(got), len(data), err)
	}
	if took := time.Since(start); took < 65*time.Second {
		t.Fatalf("the download took %v; the test needs it to take longer than the write timeout", took)
	}

	half := len(data) / 2
	resp, err = get("Range", fmt.Sprintf("bytes=%d-", half))
	if err != nil {
		t.Fatal(err)
	}
	got, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusPartialContent || !bytes.Equal(got, data[half:]) {
		t.Fatalf("resumed download: status %d, %d bytes, err %v", resp.StatusCode, len(got), err)
	}
}

func TestCreateBackupAPI(t *testing.T) {
	e := newSvcEnv(t, nil)
	release := make(chan struct{})
	dump := e.svc.dump
	e.svc.dump = func(ctx context.Context, kind Kind, actor string, progress func(Progress)) (Info, error) {
		<-release
		return dump(ctx, kind, actor, progress)
	}
	api := serveAPI(t, e.svc, nil, false)
	r := api.call("POST", "/api/v1/admin/backups", "admin", nil)
	if m := r.json(t); r.status != http.StatusAccepted || m["state"] != "backing_up" || m["type"] != "backup" || m["kind"] != "manual" {
		t.Fatalf("create = %d %s", r.status, r.body)
	}
	if r := api.call("POST", "/api/v1/admin/backups", "admin", nil); r.status != http.StatusConflict || r.errorCode(t) != "busy" {
		t.Errorf("second create = %d %s", r.status, r.body)
	}
	if job := api.call("GET", "/api/v1/admin/backups", "admin", nil).json(t)["job"].(map[string]any); job["state"] != "backing_up" {
		t.Errorf("job while running = %v", job)
	}
	close(release)
	st := e.wait(t)
	if st.State != JobDone {
		t.Fatalf("backup = %+v", st)
	}
	list := api.call("GET", "/api/v1/admin/backups", "admin", nil).json(t)
	backups, _ := list["backups"].([]any)
	if len(backups) != 1 || backups[0].(map[string]any)["name"] != st.Name || backups[0].(map[string]any)["created_by"] != adminActor {
		t.Errorf("list = %v", list)
	}
	if ev := e.audit.of(audit.ActionBackupCreated); len(ev) != 1 || ev[0].actor != adminActor {
		t.Errorf("audit = %+v", ev)
	}

	// Not enough room: refused at once with the numbers.
	e.dir.freeBytes = func() (uint64, error) { return 1 << 20, nil }
	r = api.call("POST", "/api/v1/admin/backups", "admin", nil)
	if r.status != http.StatusInsufficientStorage || r.errorCode(t) != "no_space" || !strings.Contains(r.errorMessage(t), "1.0 MiB free") {
		t.Errorf("create without room = %d %s", r.status, r.body)
	}
}

// restoreAPI serves the API of a service with a database and a backup of
// it. CheckCode accepts 111111, locks the user for 222222, limits for
// 333333 and refuses every other code; calls counts its calls.
func restoreAPI(t *testing.T) (*svcEnv, *apiEnv, string, *int) {
	t.Helper()
	e := newSvcEnv(t, nil)
	seedAll(t, e.pool)
	name := e.backup(t)
	calls := new(int)
	var mu sync.Mutex
	api := serveAPI(t, e.svc, func(o *APIOptions) {
		o.CheckCode = func(_ context.Context, ip netip.Addr, userID int64, code string) error {
			mu.Lock()
			*calls++
			mu.Unlock()
			if userID != 1 || ip != netip.MustParseAddr("192.0.2.7") {
				return fmt.Errorf("unexpected user %d or address %v", userID, ip)
			}
			switch code {
			case "111111":
				return nil
			case "222222":
				return auth.ErrLocked
			case "333333":
				return auth.ErrTooManyAttempts
			}
			return auth.ErrInvalidCode
		}
	}, false)
	return e, api, name, calls
}

func TestRestoreNeedsConfirmAndCode(t *testing.T) {
	e, api, name, calls := restoreAPI(t)
	path := "/api/v1/admin/backups/" + name + "/restore"
	changeAll(t, e.pool)

	for _, c := range []struct {
		body         map[string]any
		status       int
		code         string
		checksCodeOf bool
	}{
		{map[string]any{"code": "111111"}, 422, "confirmation_required", false},
		{map[string]any{"confirm": "restore", "code": "111111"}, 422, "confirmation_required", false},
		{map[string]any{"confirm": "RESTORE "}, 422, "confirmation_required", false},
		{map[string]any{"confirm": "RESTORE"}, 422, "invalid_code", false},
		{map[string]any{"confirm": "RESTORE", "code": ""}, 422, "invalid_code", false},
		{map[string]any{"confirm": "RESTORE", "code": "999999"}, 422, "invalid_code", true},
		{map[string]any{"confirm": "RESTORE", "code": "333333"}, 429, "too_many_attempts", true},
		{map[string]any{"confirm": "RESTORE", "code": "222222"}, 401, "unauthenticated", true},
	} {
		before := *calls
		r := api.call("POST", path, "admin", c.body)
		if r.status != c.status || r.errorCode(t) != c.code {
			t.Errorf("restore with %v = %d %s, want %d %s", c.body, r.status, r.body, c.status, c.code)
		}
		if checked := *calls > before; checked != c.checksCodeOf {
			t.Errorf("restore with %v: code checked = %v", c.body, checked)
		}
	}
	for _, body := range []string{"", "[]", `{"confirm":"RESTORE","code":"111111","extra":1}`} {
		if r := api.call("POST", path, "admin", []byte(body)); r.status != http.StatusBadRequest || r.errorCode(t) != "invalid_request" {
			t.Errorf("restore with %q = %d %s", body, r.status, r.body)
		}
	}
	// An unknown backup is refused before the code is used.
	before := *calls
	if r := api.call("POST", "/api/v1/admin/backups/scanopsis-backup-20261123T100000Z-manual-zzzzzz.tar.gz/restore", "admin",
		map[string]any{"confirm": "RESTORE", "code": "111111"}); r.status != http.StatusNotFound || *calls != before {
		t.Errorf("restore of an unknown backup = %d %s (code checked: %v)", r.status, r.body, *calls != before)
	}
	if st := e.svc.Status(); st.State != JobDone || st.Type != JobTypeBackup {
		t.Fatalf("a refused restore started a job: %+v", st)
	}

	r := api.call("POST", path, "admin", map[string]any{"confirm": "RESTORE", "code": "111111"})
	if m := r.json(t); r.status != http.StatusAccepted || m["type"] != "restore" || m["state"] != "restoring" {
		t.Fatalf("restore = %d %s", r.status, r.body)
	}
	st := e.wait(t)
	if st.State != JobDone {
		t.Fatalf("restore = %+v", st)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'backup.restored' AND actor = 'local:alice'`); n != 1 {
		t.Errorf("%d backup.restored entries by the admin", n)
	}
	kinds := map[Kind]int{}
	list, _ := e.dir.List()
	for _, b := range list {
		kinds[b.Kind]++
	}
	if kinds[KindBeforeRestore] != 1 {
		t.Errorf("backups after the restore = %v, want one before-restore", kinds)
	}

	// skip_safety_backup skips it.
	if r := api.call("POST", path, "admin", map[string]any{"confirm": "RESTORE", "code": "111111", "skip_safety_backup": true}); r.status != http.StatusAccepted {
		t.Fatalf("restore without the safety backup = %d %s", r.status, r.body)
	}
	if st := e.wait(t); st.State != JobDone {
		t.Fatalf("restore = %+v", st)
	}
	list, _ = e.dir.List()
	if len(list) != 2 {
		t.Errorf("a safety backup was taken although skipped: %+v", list)
	}
}

func TestRestoreRefusals(t *testing.T) {
	e, api, _, _ := restoreAPI(t)
	write := func(data []byte) string {
		name := e.dir.NewName(KindUploaded, time.Now())
		if err := os.WriteFile(filepath.Join(e.dir.Root(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
		return name
	}
	for _, c := range []struct {
		name   string
		data   []byte
		status int
		code   string
	}{
		{"other key", uploadArchive(t, "other-fingerprint", testSigning(t), store.LatestVersion()), 409, "key_mismatch"},
		{"newer schema", uploadArchive(t, testFingerprint(t), testSigning(t), store.LatestVersion()+1), 409, "newer_schema"},
		{"not a backup", []byte("hello"), 422, "invalid_backup"},
	} {
		name := write(c.data)
		r := api.call("POST", "/api/v1/admin/backups/"+name+"/restore", "admin", map[string]any{"confirm": "RESTORE", "code": "111111"})
		if r.status != c.status || r.errorCode(t) != c.code {
			t.Errorf("%s: restore = %d %s, want %d %s", c.name, r.status, r.body, c.status, c.code)
		}
		if c.code == "key_mismatch" && (!strings.Contains(r.errorMessage(t), "set up a new installation with that secret key") ||
			!strings.Contains(r.errorMessage(t), "Never change the secret key of an installation in use")) {
			t.Errorf("%s: the message does not say how to use it: %s", c.name, r.body)
		}
	}
	// While a restore runs, another one is refused: busy while the file is
	// checked, restoring once the data is being replaced.
	checking, replace, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	e.svc.restore = func(ctx context.Context, _ *pgxpool.Pool, _ string, o RestoreOptions) error {
		close(checking)
		select {
		case <-replace:
		case <-ctx.Done():
			return ctx.Err()
		}
		o.BeforeReplace()
		select {
		case <-release:
		case <-ctx.Done():
		}
		return errors.New("stopped by the test")
	}
	good := e.backup(t)
	body := map[string]any{"confirm": "RESTORE", "code": "111111", "skip_safety_backup": true}
	if r := api.call("POST", "/api/v1/admin/backups/"+good+"/restore", "admin", body); r.status != http.StatusAccepted {
		t.Fatalf("restore = %d %s", r.status, r.body)
	}
	<-checking
	if r := api.call("POST", "/api/v1/admin/backups/"+good+"/restore", "admin", body); r.status != http.StatusConflict || r.errorCode(t) != "busy" || e.svc.Restoring() {
		t.Errorf("second restore while the file is checked = %d %s", r.status, r.body)
	}
	close(replace)
	deadline := time.Now().Add(10 * time.Second)
	for !e.svc.Restoring() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if r := api.call("GET", "/api/v1/restore/status", "", nil); r.json(t)["state"] != "restoring" {
		t.Errorf("status = %s", r.body)
	}
	if r := api.call("POST", "/api/v1/admin/backups/"+good+"/restore", "admin", body); r.status != http.StatusServiceUnavailable || r.errorCode(t) != "restoring" {
		t.Errorf("second restore = %d %s", r.status, r.body)
	}
	if err := e.svc.Delete(good); !errors.Is(err, ErrInUse) {
		t.Errorf("delete of the backup being restored = %v", err)
	}
	close(release)
	if st := e.wait(t); st.State != JobFailed {
		t.Errorf("restore = %+v", st)
	}
	if r := api.call("GET", "/api/v1/restore/status", "", nil); r.json(t)["state"] != "failed" || strings.Contains(string(r.body), "stopped by the test") {
		t.Errorf("status = %s", r.body)
	}
}

func TestPreviewAPI(t *testing.T) {
	_, api, name, _ := restoreAPI(t)
	r := api.call("GET", "/api/v1/admin/backups/"+name+"/preview", "admin", nil)
	deadline := time.Now().Add(30 * time.Second)
	for r.status == http.StatusAccepted && time.Now().Before(deadline) {
		if m := r.json(t); m["state"] != "checking" {
			t.Fatalf("preview while checking = %v", m)
		}
		time.Sleep(10 * time.Millisecond)
		r = api.call("GET", "/api/v1/admin/backups/"+name+"/preview", "admin", nil)
	}
	if r.status != http.StatusOK {
		t.Fatalf("preview = %d %s", r.status, r.body)
	}
	m := r.json(t)
	info, _ := m["info"].(map[string]any)
	if m["schema_ok"] != true || m["key_matches"] != true || info["name"] != name || info["created_by"] != adminActor ||
		m["retention_days"] == nil || m["scans_removed_by_retention"] == nil {
		t.Errorf("preview = %v", m)
	}
}

// A brand-new installation restores a backup from the setup screen: the
// setup token stands in for an account, the backup's administrator exists
// afterwards, and the setup endpoints are closed.
func TestSetupRestoreFlow(t *testing.T) {
	src := storetest.New(t)
	seedAll(t, src)
	file := dumpPool(t, src, testFingerprint(t))
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	e := newSvcEnv(t, nil)
	q := store.New(e.pool)
	setup := auth.NewSetup(q, nil, e.audit, func(*http.Request) netip.Addr { return netip.MustParseAddr("192.0.2.7") },
		auth.NewRateLimiter(12*time.Second, 5, nil), quiet)
	token, err := setup.PrepareToken(context.Background())
	if err != nil || token == "" {
		t.Fatalf("PrepareToken = %q, %v", token, err)
	}
	e.svc.o.AfterRestore = func(ctx context.Context) error {
		_, err := setup.PrepareToken(ctx)
		return err
	}
	api := serveAPI(t, e.svc, func(o *APIOptions) { o.CheckSetupToken = setup.CheckToken }, false)

	const base = "/api/v1/setup/restore"
	if r := api.call("POST", base+"/token", "", map[string]any{"token": "wrong"}); r.status != http.StatusForbidden {
		t.Fatalf("wrong token = %d %s", r.status, r.body)
	}
	if r := api.call("POST", base+"/token", "", map[string]any{"token": token}); r.status != http.StatusNoContent {
		t.Fatalf("token check = %d %s", r.status, r.body)
	}
	r := api.call("POST", base+"/uploads", "", map[string]any{"token": token, "file_name": "from-old-server.tar.gz", "size": len(data)})
	if r.status != http.StatusCreated {
		t.Fatalf("start = %d %s", r.status, r.body)
	}
	id := r.json(t)["id"].(string)
	for off := 0; off < len(data); off += 64 << 10 {
		end := min(off+64<<10, len(data))
		if r := api.call("PUT", fmt.Sprintf("%s/uploads/%s?offset=%d", base, id, off), "", data[off:end], "X-Setup-Token", token); r.status != http.StatusOK {
			t.Fatalf("PUT = %d %s", r.status, r.body)
		}
	}
	if r := api.call("GET", base+"/uploads/"+id, "", nil, "X-Setup-Token", token); r.json(t)["received"] != float64(len(data)) {
		t.Fatalf("status = %s", r.body)
	}
	if r := api.call("POST", base, "", map[string]any{"token": token, "upload_id": id, "confirm": "RESTORE"}); r.status != http.StatusNotFound {
		t.Errorf("restore of an upload not yet completed = %d %s", r.status, r.body)
	}
	outcome := api.complete(base+"/uploads/"+id, "", map[string]any{"token": token}, "X-Setup-Token", token)
	preview, _ := outcome["preview"].(map[string]any)
	info, _ := preview["info"].(map[string]any)
	if preview["key_matches"] != true || preview["schema_ok"] != true || info["kind"] != "uploaded" ||
		info["original_name"] != "from-old-server.tar.gz" || info["created_by"] != "alice" {
		t.Fatalf("preview = %v", preview)
	}
	if ev := api.audit.of(actionUploaded); len(ev) != 1 || ev[0].actor != setupActor {
		t.Errorf("audit = %+v", ev)
	}

	for _, c := range []struct {
		body map[string]any
		code string
	}{
		{map[string]any{"token": token, "upload_id": id, "confirm": "yes"}, "confirmation_required"},
		{map[string]any{"token": token, "upload_id": "0123456789abcdef0123456789abcdef", "confirm": "RESTORE"}, "not_found"},
	} {
		if r := api.call("POST", base, "", c.body); r.errorCode(t) != c.code {
			t.Errorf("restore with %v = %d %s", c.body, r.status, r.body)
		}
	}
	r = api.call("POST", base, "", map[string]any{"token": token, "upload_id": id, "confirm": "RESTORE"})
	if r.status != http.StatusAccepted {
		t.Fatalf("restore = %d %s", r.status, r.body)
	}
	if strings.Contains(string(r.body), "alice") {
		t.Errorf("the answer names an account: %s", r.body)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		m := api.call("GET", "/api/v1/restore/status", "", nil).json(t)
		if m["state"] == "done" {
			if m["message"] != restoreDoneMessage || m["percent"] != float64(100) {
				t.Errorf("status = %v", m)
			}
			break
		}
		if m["state"] == "failed" || time.Now().After(deadline) {
			t.Fatalf("restore status = %v; job %+v", m, e.svc.Status())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := e.count(t, `SELECT count(*) FROM users WHERE username = 'alice' AND role = 'admin'`); n != 1 {
		t.Fatalf("%d administrators alice after the restore", n)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'backup.restored' AND actor = 'setup'`); n != 1 {
		t.Errorf("%d backup.restored entries by setup", n)
	}
	list, _ := e.dir.List()
	if len(list) != 1 || list[0].Kind != KindUploaded {
		t.Errorf("backups = %+v, want only the upload (no safety backup)", list)
	}

	for _, c := range []struct{ method, path string }{
		{"POST", base + "/uploads"}, {"GET", base + "/uploads/" + id}, {"PUT", base + "/uploads/" + id + "?offset=0"},
		{"DELETE", base + "/uploads/" + id}, {"POST", base + "/uploads/" + id + "/complete"}, {"POST", base},
	} {
		r := api.call(c.method, c.path, "", map[string]any{"token": token}, "X-Setup-Token", token)
		if r.status != http.StatusConflict || r.errorCode(t) != "setup_done" {
			t.Errorf("%s %s after the restore = %d %s", c.method, c.path, r.status, r.body)
		}
	}
}

// The upload folder is created with the first upload and holds nothing
// after a completed one; a sha of the stored file matches.
func TestUploadStoresTheFileUnchanged(t *testing.T) {
	e := newSvcEnv(t, nil)
	seedAll(t, e.pool)
	src := e.backup(t)
	p, _ := e.dir.Path(src)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// A copy of a listed backup is refused; this one comes from elsewhere.
	if err := e.dir.Delete(src); err != nil {
		t.Fatal(err)
	}
	api := serveAPI(t, e.svc, nil, false)
	id := api.startUpload("copy.tar.gz", len(data))
	api.send(id, data, 0, ChunkSize)
	stored, _ := e.dir.Path(api.completed(id)["name"].(string))
	got, err := os.ReadFile(stored)
	if err != nil || sha256.Sum256(got) != sha256.Sum256(data) {
		t.Fatalf("stored file differs (err %v)", err)
	}
}
