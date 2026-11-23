// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/httpapi"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const (
	apiFingerprint = "api-test-fingerprint"
	roleHeader     = "X-Test-Role"
	adminActor     = "local:alice"
)

var discard = slog.New(slog.DiscardHandler)

// fakeAuth signs requests in by the X-Test-Role header: "admin", "viewer" or
// "unverified" (an admin whose second factor is not yet verified).
type fakeAuth struct{}

func (fakeAuth) Authenticate(r *http.Request) (*auth.Principal, error) {
	switch r.Header.Get(roleHeader) {
	case "admin":
		return &auth.Principal{UserID: 1, Source: "local", Username: "alice", Role: auth.RoleAdmin, MFAEnrolled: true, MFAVerified: true}, nil
	case "viewer":
		return &auth.Principal{UserID: 2, Source: "gitlab", Username: "vera", Role: auth.RoleViewer, MFAEnrolled: true, MFAVerified: true}, nil
	case "unverified":
		return &auth.Principal{UserID: 1, Source: "local", Username: "alice", Role: auth.RoleAdmin, MFAEnrolled: true}, nil
	}
	return nil, auth.ErrNoSession
}

func (fakeAuth) End(http.ResponseWriter, *http.Request) error { return nil }

type recEvent struct {
	actor, action string
	detail        map[string]any
}

type recAudit struct {
	mu     sync.Mutex
	events []recEvent
}

func (a *recAudit) Record(_ context.Context, actor, action string, detail map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, recEvent{actor, action, detail})
}

func (a *recAudit) of(action string) []recEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []recEvent
	for _, e := range a.events {
		if e.action == action {
			out = append(out, e)
		}
	}
	return out
}

type apiEnv struct {
	t     *testing.T
	dir   *Dir
	svc   *Service
	api   *API
	audit *recAudit
	url   string
	h     http.Handler
	free  *atomic.Uint64 // the free space of the folder, when the test sets it
}

// newAPIEnv serves the backup API, behind the real router, over a backup
// folder without a database. mutate may change the options.
func newAPIEnv(t *testing.T, mutate func(*APIOptions)) *apiEnv {
	t.Helper()
	dir, err := OpenDir(t.TempDir(), apiFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	free := &atomic.Uint64{}
	free.Store(100 << 30)
	dir.freeBytes = func() (uint64, error) { return free.Load(), nil }
	rec := &recAudit{}
	svc := NewService(ServiceOptions{
		Dir: dir, Log: discard, Audit: rec, KeyFingerprint: apiFingerprint,
		Dumper: &Dumper{Dir: dir, KeyFingerprint: apiFingerprint, SigningKey: testSigningKey},
	})
	t.Cleanup(svc.Close)
	e := serveAPI(t, svc, mutate, false)
	e.free = free
	return e
}

// serveAPI serves the API of svc behind the real router. With
// realTimeouts the server has the timeouts of the production server.
func serveAPI(t *testing.T, svc *Service, mutate func(*APIOptions), realTimeouts bool) *apiEnv {
	t.Helper()
	e := &apiEnv{t: t, dir: svc.o.Dir, svc: svc, audit: &recAudit{}}
	o := APIOptions{
		Service: svc, FolderPath: svc.o.Dir.Root(), Audit: e.audit, Log: discard,
		ClientIP: func(*http.Request) netip.Addr { return netip.MustParseAddr("192.0.2.7") },
		CheckCode: func(context.Context, netip.Addr, int64, string) error {
			return errors.New("no code check in this test")
		},
		CheckSetupToken: func(_ context.Context, _ netip.Addr, token string) error {
			if token != "setup-token" {
				return auth.ErrInvalidToken
			}
			return nil
		},
		RetentionDays: func() int { return 90 },
	}
	if mutate != nil {
		mutate(&o)
	}
	e.api = NewAPI(o)
	e.h = httpapi.New(httpapi.Deps{
		Log: discard, Sessions: fakeAuth{}, Audit: e.audit, ClientIP: o.ClientIP,
		Features: e.api.Handlers(), Restoring: svc.Restoring, MaxConcurrent: 4,
	})
	srv := httptest.NewUnstartedServer(e.h)
	if realTimeouts {
		srv.Config = httpapi.NewServer(e.h)
	}
	srv.Start()
	t.Cleanup(srv.Close)
	e.url = srv.URL
	return e
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (r response) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("status %d, body is not JSON: %v\n%s", r.status, err, r.body)
	}
	return m
}

func (r response) errorCode(t *testing.T) string {
	t.Helper()
	e, _ := r.json(t)["error"].(map[string]any)
	code, _ := e["code"].(string)
	if msg, _ := e["message"].(string); msg == "" {
		t.Errorf("error %q has no message: %s", code, r.body)
	}
	return code
}

func (r response) errorMessage(t *testing.T) string {
	t.Helper()
	e, _ := r.json(t)["error"].(map[string]any)
	msg, _ := e["message"].(string)
	return msg
}

// call sends a request; body may be nil, a []byte, an io.Reader or a value
// sent as JSON. hdr holds alternating header names and values.
func (e *apiEnv) call(method, path, role string, body any, hdr ...string) response {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	case io.Reader:
		rd = b
	default:
		j, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(j)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, e.url+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if role != "" {
		req.Header.Set(roleHeader, role)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatalf("%s %s: read answer: %v", method, path, err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: b}
}

const adminUploads = "/api/v1/admin/backup-uploads"

func (e *apiEnv) startUpload(fileName string, size int) string {
	e.t.Helper()
	r := e.call("POST", adminUploads, "admin", map[string]any{"file_name": fileName, "size": size})
	if r.status != http.StatusCreated {
		e.t.Fatalf("start upload = %d %s", r.status, r.body)
	}
	m := r.json(e.t)
	if m["chunk_size"] != float64(ChunkSize) || m["received"] != float64(0) || m["size"] != float64(size) {
		e.t.Fatalf("start upload answer = %v", m)
	}
	id, _ := m["id"].(string)
	if len(id) != 32 {
		e.t.Fatalf("upload id %q is not 128 bits in hex", id)
	}
	return id
}

func (e *apiEnv) put(id string, offset int, data []byte) response {
	e.t.Helper()
	return e.call("PUT", fmt.Sprintf("%s/%s?offset=%d", adminUploads, id, offset), "admin", data,
		"Content-Type", "application/octet-stream")
}

// send uploads data from offset in parts of n bytes.
func (e *apiEnv) send(id string, data []byte, from, n int) {
	e.t.Helper()
	for off := from; off < len(data); off += n {
		end := min(off+n, len(data))
		r := e.put(id, off, data[off:end])
		if r.status != http.StatusOK || r.json(e.t)["received"] != float64(end) {
			e.t.Fatalf("PUT at %d = %d %s", off, r.status, r.body)
		}
	}
}

// uploadArchive returns a signed backup made with the key fingerprint fp and
// signing key, at schema version schema, of about 300 KiB.
func uploadArchive(t *testing.T, fp string, key []byte, schema int64) []byte {
	t.Helper()
	m := Manifest{
		AppVersion: "0.4.0", AppCommit: "abc1234", Schema: schema,
		CreatedAt: time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC), Kind: KindScheduled, CreatedBy: ScheduleActor,
		KeyFingerprint: fp,
		Tables:         []TableSpec{{Name: "services", Columns: []string{"id", "name"}}},
	}
	var buf bytes.Buffer
	data := map[string][]byte{"services": randomBytes(11, 300<<10)}
	if _, err := WriteArchive(context.Background(), &buf, m, key, dataCopier(data), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func goodArchive(t *testing.T) []byte {
	t.Helper()
	return uploadArchive(t, apiFingerprint, testSigningKey, store.LatestVersion())
}

func uploadFiles(t *testing.T, d *Dir) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(d.Root(), UploadDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func (e *apiEnv) list() []Info {
	e.t.Helper()
	list, err := e.dir.List()
	if err != nil {
		e.t.Fatal(err)
	}
	return list
}

// waitNotBusy retries fn while it answers 409 upload_busy, which it does
// until the server has finished with a part whose connection dropped.
func (e *apiEnv) waitNotBusy(fn func() response) response {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		r := fn()
		if r.status != http.StatusConflict || r.errorCode(e.t) != "upload_busy" || time.Now().After(deadline) {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestUploadResume(t *testing.T) {
	e := newAPIEnv(t, nil)
	data := goodArchive(t)
	id := e.startUpload("nightly backup.tar.gz", len(data))
	const part = 100 << 10
	e.send(id, data[:2*part], 0, part)

	// The third part is cut off by a dropped connection.
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write(data[2*part : 2*part+part/2])
		_ = pw.CloseWithError(errors.New("connection dropped"))
	}()
	req, err := http.NewRequestWithContext(context.Background(), "PUT", fmt.Sprintf("%s%s/%s?offset=%d", e.url, adminUploads, id, 2*part), pr)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = part
	req.Header.Set(roleHeader, "admin")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Fatalf("the cut-off part was answered %d", resp.StatusCode)
	}

	r := e.waitNotBusy(func() response { return e.call("GET", adminUploads+"/"+id, "admin", nil) })
	if r.status != http.StatusOK || r.json(t)["received"] != float64(2*part) {
		t.Fatalf("status after the drop = %d %s, want received %d", r.status, r.body, 2*part)
	}
	if m := r.json(t); m["file_name"] != "nightly backup.tar.gz" || m["size"] != float64(len(data)) {
		t.Errorf("status = %v", m)
	}
	// The resumed upload goes on from what the server has.
	r = e.waitNotBusy(func() response { return e.put(id, 2*part, data[2*part:3*part]) })
	if r.status != http.StatusOK {
		t.Fatalf("resume = %d %s", r.status, r.body)
	}
	e.send(id, data, 3*part, part)

	before := time.Now().UTC().Add(-time.Second)
	r = e.call("POST", adminUploads+"/"+id+"/complete", "admin", nil)
	if r.status != http.StatusCreated {
		t.Fatalf("complete = %d %s", r.status, r.body)
	}
	info := r.json(t)
	name, _ := info["name"].(string)
	if !strings.Contains(name, "-uploaded-") || info["kind"] != "uploaded" || info["original_name"] != "nightly backup.tar.gz" ||
		info["key_matches"] != true || info["readable"] != true || info["uploaded_at"] == nil || info["created_by"] != ScheduleActor {
		t.Fatalf("completed upload = %v", info)
	}

	list := e.list()
	if len(list) != 1 || list[0].Name != name || list[0].Kind != KindUploaded || list[0].OriginalName != "nightly backup.tar.gz" ||
		list[0].UploadedAt == nil || list[0].UploadedAt.Before(before) || list[0].Size != int64(len(data)) {
		t.Fatalf("list = %+v", list)
	}
	p, _ := e.dir.Path(name)
	got, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("the stored file differs from the upload (err %v)", err)
	}
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode of the stored file = %v (err %v), want 0600", fi.Mode().Perm(), err)
	}
	if left := uploadFiles(t, e.dir); len(left) != 0 {
		t.Errorf("files left in the upload folder: %v", left)
	}
	if ev := e.audit.of(actionUploaded); len(ev) != 1 || ev[0].actor != adminActor || ev[0].detail["name"] != name ||
		ev[0].detail["original_name"] != "nightly backup.tar.gz" || ev[0].detail["size"] != int64(len(data)) {
		t.Errorf("audit = %+v", ev)
	}
	// The finished upload is gone.
	if r := e.call("GET", adminUploads+"/"+id, "admin", nil); r.status != http.StatusNotFound || r.errorCode(t) != "not_found" {
		t.Errorf("status of a completed upload = %d %s", r.status, r.body)
	}
	// The listing of the API shows it too.
	r = e.call("GET", "/api/v1/admin/backups", "admin", nil)
	if r.status != http.StatusOK {
		t.Fatalf("list = %d %s", r.status, r.body)
	}
	backups, _ := r.json(t)["backups"].([]any)
	if len(backups) != 1 || backups[0].(map[string]any)["original_name"] != "nightly backup.tar.gz" {
		t.Errorf("listed backups = %v", backups)
	}
}

func TestUploadConcurrent(t *testing.T) {
	e := newAPIEnv(t, nil)
	a, b := goodArchive(t), uploadArchive(t, apiFingerprint, testSigningKey, 3)
	ida, idb := e.startUpload("a.tar.gz", len(a)), e.startUpload("b.tar.gz", len(b))
	if r := e.call("POST", adminUploads, "admin", map[string]any{"file_name": "c.tar.gz", "size": 10}); r.status != http.StatusConflict || r.errorCode(t) != "too_many_uploads" {
		t.Fatalf("third upload = %d %s", r.status, r.body)
	}
	const part = 64 << 10
	var wg sync.WaitGroup
	for _, u := range []struct {
		id   string
		data []byte
	}{{ida, a}, {idb, b}} {
		wg.Go(func() {
			for off := 0; off < len(u.data); off += part {
				end := min(off+part, len(u.data))
				if r := e.put(u.id, off, u.data[off:end]); r.status != http.StatusOK {
					t.Errorf("PUT %s at %d = %d %s", u.id, off, r.status, r.body)
					return
				}
			}
		})
	}
	wg.Wait()
	sums := map[string]bool{}
	for _, id := range []string{ida, idb} {
		r := e.call("POST", adminUploads+"/"+id+"/complete", "admin", nil)
		if r.status != http.StatusCreated {
			t.Fatalf("complete %s = %d %s", id, r.status, r.body)
		}
	}
	list := e.list()
	if len(list) != 2 {
		t.Fatalf("list = %+v", list)
	}
	for _, info := range list {
		p, _ := e.dir.Path(info.Name)
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		sums[fmt.Sprintf("%x", sha256.Sum256(got))] = true
	}
	if !sums[fmt.Sprintf("%x", sha256.Sum256(a))] || !sums[fmt.Sprintf("%x", sha256.Sum256(b))] {
		t.Error("the stored files are not the two uploads")
	}
	// With both done, new uploads can start.
	e.startUpload("c.tar.gz", 10)
}

func TestUploadRejects(t *testing.T) {
	e := newAPIEnv(t, nil)
	good := goodArchive(t)

	t.Run("start", func(t *testing.T) {
		for _, body := range []any{
			map[string]any{"file_name": "", "size": 10},
			map[string]any{"file_name": "a.tar.gz", "size": 0},
			map[string]any{"file_name": "a.tar.gz", "size": -1},
			map[string]any{"file_name": strings.Repeat("a", 256), "size": 10},
			map[string]any{"file_name": "a\nb", "size": 10},
			map[string]any{"file_name": "a.tar.gz"},
			map[string]any{"file_name": "a.tar.gz", "size": 10, "extra": true},
			"not an object",
		} {
			if r := e.call("POST", adminUploads, "admin", body); r.status != http.StatusBadRequest || r.errorCode(t) != "invalid_request" {
				t.Errorf("start with %v = %d %s", body, r.status, r.body)
			}
		}
		// The file must fit the free space less 512 MiB.
		e.free.Store(1 << 30)
		r := e.call("POST", adminUploads, "admin", map[string]any{"file_name": "big.tar.gz", "size": 512<<20 + 1})
		if r.status != http.StatusInsufficientStorage || r.errorCode(t) != "no_space" || !strings.Contains(r.errorMessage(t), "1.0 GiB") {
			t.Errorf("too large = %d %s", r.status, r.body)
		}
		if r := e.call("POST", adminUploads, "admin", map[string]any{"file_name": "fits.tar.gz", "size": 512 << 20}); r.status != http.StatusCreated {
			t.Errorf("fitting upload = %d %s", r.status, r.body)
		} else {
			e.call("DELETE", adminUploads+"/"+r.json(t)["id"].(string), "admin", nil)
		}
		e.free.Store(100 << 30)
	})

	t.Run("parts", func(t *testing.T) {
		id := e.startUpload("a.tar.gz", len(good))
		defer e.call("DELETE", adminUploads+"/"+id, "admin", nil)
		e.send(id, good[:1000], 0, 1000)
		for _, off := range []int{0, 999, 1001} {
			r := e.put(id, off, good[off:off+10])
			if r.status != http.StatusConflict || r.errorCode(t) != "offset_mismatch" {
				t.Fatalf("offset %d = %d %s", off, r.status, r.body)
			}
			if e, _ := r.json(t)["error"].(map[string]any); e["received"] != float64(1000) {
				t.Errorf("offset %d: error does not give received: %s", off, r.body)
			}
		}
		for _, q := range []string{"", "?offset=", "?offset=x", "?offset=-1"} {
			r := e.call("PUT", adminUploads+"/"+id+q, "admin", []byte("x"))
			if r.status != http.StatusBadRequest || r.errorCode(t) != "invalid_request" {
				t.Errorf("PUT%s = %d %s", q, r.status, r.body)
			}
		}
		big := e.startUpload("big.tar.gz", 2*ChunkSize)
		if r := e.put(big, 0, make([]byte, ChunkSize+1)); r.status != http.StatusRequestEntityTooLarge || r.errorCode(t) != "payload_too_large" {
			t.Errorf("part over the chunk size = %d %s", r.status, r.body)
		}
		// Without Content-Length the limit applies while reading.
		r := e.call("PUT", fmt.Sprintf("%s/%s?offset=0", adminUploads, big), "admin", io.MultiReader(bytes.NewReader(make([]byte, ChunkSize)), strings.NewReader("x")))
		if r.status != http.StatusRequestEntityTooLarge || r.errorCode(t) != "payload_too_large" {
			t.Errorf("streamed part over the chunk size = %d %s", r.status, r.body)
		}
		if r := e.waitNotBusy(func() response { return e.call("GET", adminUploads+"/"+big, "admin", nil) }); r.json(t)["received"] != float64(0) {
			t.Errorf("a refused part was kept: %s", r.body)
		}
		e.call("DELETE", adminUploads+"/"+big, "admin", nil)
		if r := e.put(id, 1000, make([]byte, len(good))); r.status != http.StatusBadRequest || r.errorCode(t) != "invalid_request" {
			t.Errorf("part past the end = %d %s", r.status, r.body)
		}
		if r := e.call("GET", adminUploads+"/"+id, "admin", nil); r.json(t)["received"] != float64(1000) {
			t.Errorf("refused parts changed the upload: %s", r.body)
		}
		if r := e.call("POST", adminUploads+"/"+id+"/complete", "admin", nil); r.status != http.StatusConflict || r.errorCode(t) != "upload_incomplete" {
			t.Errorf("complete before the end = %d %s", r.status, r.body)
		}
		for _, method := range []string{"GET", "PUT", "DELETE"} {
			if r := e.call(method, adminUploads+"/0123456789abcdef0123456789abcdef?offset=0", "admin", nil); r.status != http.StatusNotFound || r.errorCode(t) != "not_found" {
				t.Errorf("%s of an unknown upload = %d %s", method, r.status, r.body)
			}
		}
	})

	newer := uploadArchive(t, apiFingerprint, testSigningKey, store.LatestVersion()+1)
	for _, c := range []struct {
		name, code, reason string
		data               []byte
	}{
		{"truncated", "invalid_backup", "ends too early", good[:len(good)/2]},
		{"not a backup", "invalid_backup", "not a Scanopsis backup", bytes.Repeat([]byte("x"), 5000)},
		{"other key", "key_mismatch", "SCANOPSIS_SECRET_KEY", uploadArchive(t, "other-fingerprint", testSigningKey, store.LatestVersion())},
		{"forged signature", "invalid_backup", "secret key", uploadArchive(t, apiFingerprint, bytes.Repeat([]byte{1}, 32), store.LatestVersion())},
		{"newer schema", "newer_schema", "upgrade Scanopsis", newer},
	} {
		t.Run(c.name, func(t *testing.T) {
			id := e.startUpload("x.tar.gz", len(c.data))
			e.send(id, c.data, 0, 64<<10)
			r := e.call("POST", adminUploads+"/"+id+"/complete", "admin", nil)
			if r.status != http.StatusUnprocessableEntity || r.errorCode(t) != c.code || !strings.Contains(r.errorMessage(t), c.reason) {
				t.Fatalf("complete = %d %s, want 422 %s naming %q", r.status, r.body, c.code, c.reason)
			}
			if r := e.call("GET", adminUploads+"/"+id, "admin", nil); r.status != http.StatusNotFound {
				t.Errorf("a refused upload is still open: %d %s", r.status, r.body)
			}
		})
	}
	if list := e.list(); len(list) != 0 {
		t.Errorf("refused uploads were listed: %+v", list)
	}
	if left := uploadFiles(t, e.dir); len(left) != 0 {
		t.Errorf("files left in the upload folder: %v", left)
	}
	if ev := e.audit.of(actionUploaded); len(ev) != 0 {
		t.Errorf("refused uploads were audited: %+v", ev)
	}
}

// An upload that a request is working on cannot be cancelled under it.
func TestUploadCancelWhileBusy(t *testing.T) {
	e := newAPIEnv(t, nil)
	id := e.startUpload("a.tar.gz", 10)
	up, err := e.api.up.acquire(scopeAdmin, id, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if r := e.call("DELETE", adminUploads+"/"+id, "admin", nil); r.status != http.StatusConflict || r.errorCode(t) != "upload_busy" {
		t.Errorf("cancel while busy = %d %s", r.status, r.body)
	}
	if len(uploadFiles(t, e.dir)) != 1 {
		t.Error("the file of a busy upload was deleted")
	}
	e.api.up.release(up, 0, time.Now())
	if r := e.call("DELETE", adminUploads+"/"+id, "admin", nil); r.status != http.StatusNoContent {
		t.Errorf("cancel after release = %d %s", r.status, r.body)
	}
}

func TestUploadCancel(t *testing.T) {
	e := newAPIEnv(t, nil)
	data := goodArchive(t)
	id := e.startUpload("a.tar.gz", len(data))
	e.send(id, data[:5000], 0, 5000)
	if len(uploadFiles(t, e.dir)) != 1 {
		t.Fatal("no partial file while uploading")
	}
	if r := e.call("DELETE", adminUploads+"/"+id, "admin", nil); r.status != http.StatusNoContent {
		t.Fatalf("cancel = %d %s", r.status, r.body)
	}
	if left := uploadFiles(t, e.dir); len(left) != 0 {
		t.Errorf("cancel left %v", left)
	}
	if r := e.call("GET", adminUploads+"/"+id, "admin", nil); r.status != http.StatusNotFound {
		t.Errorf("cancelled upload = %d", r.status)
	}
}

// Uploads that nobody continues expire, so they cannot hold the slots.
func TestUploadIdleExpires(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	e := newAPIEnv(t, func(o *APIOptions) {
		o.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	})
	a, b := e.startUpload("a.tar.gz", 10), e.startUpload("b.tar.gz", 10)
	mu.Lock()
	now = now.Add(uploadIdle + time.Minute)
	mu.Unlock()
	e.startUpload("c.tar.gz", 10)
	for _, id := range []string{a, b} {
		if r := e.call("GET", adminUploads+"/"+id, "admin", nil); r.status != http.StatusNotFound {
			t.Errorf("idle upload %s = %d", id, r.status)
		}
	}
	if left := uploadFiles(t, e.dir); len(left) != 1 {
		t.Errorf("upload folder = %v, want one file", left)
	}
}

func TestNonAdminForbidden(t *testing.T) {
	e := newAPIEnv(t, nil)
	data := goodArchive(t)
	id := e.startUpload("a.tar.gz", len(data))
	e.send(id, data, 0, len(data))
	name := e.call("POST", adminUploads+"/"+id+"/complete", "admin", nil).json(t)["name"].(string)
	id = e.startUpload("b.tar.gz", 10)
	base := "/api/v1/admin/backups/" + name

	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/admin/backups"},
		{"POST", "/api/v1/admin/backups"},
		{"GET", base + "/download"},
		{"DELETE", base},
		{"GET", base + "/preview"},
		{"POST", base + "/restore"},
		{"POST", adminUploads},
		{"GET", adminUploads + "/" + id},
		{"PUT", adminUploads + "/" + id + "?offset=0"},
		{"DELETE", adminUploads + "/" + id},
		{"POST", adminUploads + "/" + id + "/complete"},
	} {
		for role, want := range map[string]string{"": "unauthenticated", "viewer": "forbidden", "unverified": "mfa_required"} {
			body := []byte(`{"file_name":"x.tar.gz","size":1,"confirm":"RESTORE","code":"123456"}`)
			if c.method == "PUT" {
				body = []byte("x")
			}
			r := e.call(c.method, c.path, role, body)
			if (r.status != http.StatusUnauthorized && r.status != http.StatusForbidden) || r.errorCode(t) != want {
				t.Errorf("%s %s as %q = %d %s, want %s", c.method, c.path, role, r.status, r.body, want)
			}
		}
	}
	// Nothing was changed.
	if list := e.list(); len(list) != 1 || list[0].Name != name {
		t.Errorf("list = %+v", list)
	}
	if r := e.call("GET", adminUploads+"/"+id, "admin", nil); r.status != http.StatusOK || r.json(t)["received"] != float64(0) {
		t.Errorf("upload = %d %s", r.status, r.body)
	}
	if st := e.svc.Status(); st.State != JobIdle {
		t.Errorf("a job ran: %+v", st)
	}
	if ev := e.audit.of(actionDownloaded); len(ev) != 0 {
		t.Errorf("downloads audited: %+v", ev)
	}
}

func TestDownloadAndDelete(t *testing.T) {
	e := newAPIEnv(t, nil)
	data := goodArchive(t)
	id := e.startUpload("a.tar.gz", len(data))
	e.send(id, data, 0, len(data))
	name := e.call("POST", adminUploads+"/"+id+"/complete", "admin", nil).json(t)["name"].(string)
	base := "/api/v1/admin/backups/" + name

	r := e.call("GET", base+"/download", "admin", nil)
	if r.status != http.StatusOK || !bytes.Equal(r.body, data) {
		t.Fatalf("download = %d, %d bytes", r.status, len(r.body))
	}
	if cd := r.header.Get("Content-Disposition"); cd != `attachment; filename="`+name+`"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if r.header.Get("Content-Length") != fmt.Sprint(len(data)) || r.header.Get("Accept-Ranges") != "bytes" ||
		r.header.Get("Content-Type") != "application/gzip" || r.header.Get("Cache-Control") != "no-store" {
		t.Errorf("headers = %v", r.header)
	}
	r = e.call("GET", base+"/download", "admin", nil, "Range", "bytes=1000-")
	if r.status != http.StatusPartialContent || !bytes.Equal(r.body, data[1000:]) {
		t.Errorf("range = %d, %d bytes", r.status, len(r.body))
	}
	// A download is audited once: in full, or by a range from the start.
	// Resuming it, a range outside the file and a HEAD are not.
	if r := e.call("GET", base+"/download", "admin", nil, "Range", "bytes=0-99"); r.status != http.StatusPartialContent || len(r.body) != 100 {
		t.Errorf("first range = %d, %d bytes", r.status, len(r.body))
	}
	if r := e.call("GET", base+"/download", "admin", nil, "Range", "bytes=99999999-"); r.status != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("range outside the file = %d", r.status)
	}
	if r := e.call("HEAD", base+"/download", "admin", nil); r.status != http.StatusOK {
		t.Errorf("HEAD = %d", r.status)
	}
	ev := e.audit.of(actionDownloaded)
	if len(ev) != 2 || ev[0].actor != adminActor || ev[0].detail["name"] != name || ev[0].detail["size"] != int64(len(data)) {
		t.Errorf("audit = %+v", ev)
	}
	for _, x := range ev {
		if len(x.detail) != 2 {
			t.Errorf("audit detail = %v, want only name and size", x.detail)
		}
	}

	for _, bad := range []string{"scanopsis-backup-20261123T100000Z-manual-zzzzzz.tar.gz", "..%2F..%2Fetc%2Fpasswd", "x.tar.gz"} {
		for _, c := range []struct{ method, suffix string }{{"GET", "/download"}, {"DELETE", ""}, {"GET", "/preview"}} {
			if r := e.call(c.method, "/api/v1/admin/backups/"+bad+c.suffix, "admin", nil); r.status != http.StatusNotFound {
				t.Errorf("%s %s%s = %d %s", c.method, bad, c.suffix, r.status, r.body)
			}
		}
	}

	if r := e.call("DELETE", base, "admin", nil); r.status != http.StatusNoContent {
		t.Fatalf("delete = %d %s", r.status, r.body)
	}
	if list := e.list(); len(list) != 0 {
		t.Errorf("list after delete = %+v", list)
	}
	if ev := e.audit.of(actionDeleted); len(ev) != 1 || ev[0].actor != adminActor || ev[0].detail["name"] != name || ev[0].detail["size"] != int64(len(data)) {
		t.Errorf("audit = %+v", ev)
	}
	if r := e.call("DELETE", base, "admin", nil); r.status != http.StatusNotFound {
		t.Errorf("second delete = %d", r.status)
	}
	if entries, _ := os.ReadDir(e.dir.Root()); len(entries) != 1 { // the upload folder
		var names []string
		for _, en := range entries {
			names = append(names, en.Name())
		}
		t.Errorf("folder after delete = %v", names)
	}
}

// A backup being restored cannot be deleted.
func TestDeleteRefusedWhileRestoring(t *testing.T) {
	e := newAPIEnv(t, nil)
	data := goodArchive(t)
	id := e.startUpload("a.tar.gz", len(data))
	e.send(id, data, 0, len(data))
	name := e.call("POST", adminUploads+"/"+id+"/complete", "admin", nil).json(t)["name"].(string)
	if _, err := e.svc.begin(JobStatus{State: JobRestoring, Type: JobTypeRestore, Name: name}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(name); !errors.Is(err, ErrInUse) {
		t.Errorf("Delete while restoring = %v, want ErrInUse", err)
	}
	e.svc.finish(func(st *JobStatus) { st.State = JobDone })
	if err := e.svc.Delete(name); err != nil {
		t.Errorf("Delete after the restore = %v", err)
	}
}

func TestListBackups(t *testing.T) {
	e := newAPIEnv(t, nil)
	r := e.call("GET", "/api/v1/admin/backups", "admin", nil)
	if r.status != http.StatusOK {
		t.Fatalf("list = %d %s", r.status, r.body)
	}
	m := r.json(t)
	folder, _ := m["folder"].(map[string]any)
	disk, _ := m["disk"].(map[string]any)
	job, _ := m["job"].(map[string]any)
	if b, ok := m["backups"].([]any); !ok || len(b) != 0 || folder["ok"] != true || folder["path"] != e.dir.Root() ||
		disk["free_bytes"] != float64(100<<30) || disk["used_bytes"] != float64(0) || job["state"] != "idle" || m["schedule"] == nil {
		t.Errorf("list = %v", m)
	}
	if _, ok := folder["error"]; ok {
		t.Errorf("folder error on a usable folder: %v", folder)
	}
}

func TestBackupsWithoutFolder(t *testing.T) {
	folderErr := fmt.Errorf("%w: /var/lib/scanopsis/backups does not exist", ErrFolder)
	svc := NewService(ServiceOptions{Log: discard, KeyFingerprint: apiFingerprint})
	t.Cleanup(svc.Close)
	a := NewAPI(APIOptions{Service: svc, FolderPath: "/var/lib/scanopsis/backups", FolderErr: folderErr, Log: discard, Audit: &recAudit{}})
	h := httpapi.New(httpapi.Deps{Log: discard, Sessions: fakeAuth{}, Features: a.Handlers()})
	do := func(method, path string, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
		req.Header.Set(roleHeader, "admin")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	rec := do("GET", "/api/v1/admin/backups", "")
	var m struct {
		Backups []Info `json:"backups"`
		Folder  struct {
			Path  string `json:"path"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"folder"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || rec.Code != 200 || m.Backups == nil || len(m.Backups) != 0 ||
		m.Folder.OK || m.Folder.Path != "/var/lib/scanopsis/backups" || !strings.Contains(m.Folder.Error, "does not exist") {
		t.Errorf("list = %d %s", rec.Code, rec.Body)
	}
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/v1/admin/backups", ""},
		{"POST", adminUploads, `{"file_name":"a.tar.gz","size":10}`},
	} {
		rec := do(c.method, c.path, c.body)
		var body struct {
			Error struct{ Code, Message string }
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusServiceUnavailable || body.Error.Code != "folder_unavailable" || !strings.Contains(body.Error.Message, "does not exist") {
			t.Errorf("%s %s = %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
}

func TestRestoreStatusIsPublic(t *testing.T) {
	e := newAPIEnv(t, nil)
	r := e.call("GET", "/api/v1/restore/status", "", nil)
	if r.status != http.StatusOK {
		t.Fatalf("status = %d %s", r.status, r.body)
	}
	m := r.json(t)
	if m["state"] != "idle" || m["percent"] != float64(0) || m["finished_at"] != nil || len(m) != 4 {
		t.Errorf("idle status = %v", m)
	}

	// A backup job is not a restore.
	if _, err := e.svc.begin(JobStatus{State: JobBackingUp, Type: JobTypeBackup, Kind: KindManual}); err != nil {
		t.Fatal(err)
	}
	if m := e.call("GET", "/api/v1/restore/status", "", nil).json(t); m["state"] != "idle" {
		t.Errorf("status during a backup = %v", m)
	}
	e.svc.finish(func(st *JobStatus) {
		st.State, st.Name = JobDone, "scanopsis-backup-20261123T100000Z-manual-abc123.tar.gz"
	})

	// A restore shows its state, without names or actors.
	if _, err := e.svc.begin(JobStatus{State: JobRestoring, Type: JobTypeRestore, Kind: KindManual,
		Name: "scanopsis-backup-20261123T100000Z-manual-abc123.tar.gz", Message: "Restoring the backup."}); err != nil {
		t.Fatal(err)
	}
	e.svc.restoring.Store(true)
	r = e.call("GET", "/api/v1/restore/status", "", nil)
	if m := r.json(t); r.status != http.StatusOK || m["state"] != "restoring" || strings.Contains(string(r.body), "scanopsis-backup") {
		t.Errorf("status during a restore = %d %s", r.status, r.body)
	}
	// Other API requests are turned away meanwhile.
	if r := e.call("GET", "/api/v1/admin/backups", "admin", nil); r.status != http.StatusServiceUnavailable || r.errorCode(t) != "restoring" {
		t.Errorf("list during a restore = %d %s", r.status, r.body)
	}
	e.svc.needRestart.Store(true)
	e.svc.finish(func(st *JobStatus) { st.State, st.Message = JobFailed, restartMessage+" "+"local:alice" })
	r = e.call("GET", "/api/v1/restore/status", "", nil)
	if m := r.json(t); m["state"] != "failed" || m["message"] != restartMessage || m["finished_at"] == nil {
		t.Errorf("status of a restore that needs a restart = %s", r.body)
	}
}

func TestPublicRestoreMessages(t *testing.T) {
	for _, c := range []struct {
		st          JobStatus
		needRestart bool
		state       JobState
		message     string
	}{
		{JobStatus{State: JobIdle}, false, JobIdle, ""},
		{JobStatus{State: JobDone, Type: JobTypeBackup}, false, JobIdle, ""},
		{JobStatus{State: JobBackingUp, Type: JobTypeRestore, Message: "Backing up x.tar.gz"}, false, JobBackingUp, restoreBackingUpMessage},
		{JobStatus{State: JobRestoring, Type: JobTypeRestore, Message: "Restoring x.tar.gz"}, false, JobRestoring, restoreRunningMessage},
		{JobStatus{State: JobDone, Type: JobTypeRestore, Message: "saved as x.tar.gz"}, false, JobDone, restoreDoneMessage},
		{JobStatus{State: JobFailed, Type: JobTypeRestore, Message: "/var/lib/x failed"}, false, JobFailed, restoreFailedMessage},
		{JobStatus{State: JobFailed, Type: JobTypeRestore, Message: "anything"}, true, JobFailed, restartMessage},
	} {
		got := publicRestore(c.st, c.needRestart)
		if got.State != c.state || got.Message != c.message {
			t.Errorf("publicRestore(%+v, %v) = %+v, want %s %q", c.st, c.needRestart, got, c.state, c.message)
		}
	}
}

func TestSetupUploadNeedsToken(t *testing.T) {
	e := newAPIEnv(t, nil)
	const base = "/api/v1/setup/restore/uploads"
	for _, token := range []string{"", "wrong"} {
		r := e.call("POST", base, "", map[string]any{"token": token, "file_name": "a.tar.gz", "size": 10})
		if r.status != http.StatusForbidden || r.errorCode(t) != "invalid_setup_token" {
			t.Errorf("start with token %q = %d %s", token, r.status, r.body)
		}
	}
	r := e.call("POST", base, "", map[string]any{"token": "setup-token", "file_name": "a.tar.gz", "size": 10})
	if r.status != http.StatusCreated {
		t.Fatalf("start = %d %s", r.status, r.body)
	}
	id := r.json(t)["id"].(string)
	for _, c := range []struct{ method, path string }{
		{"GET", base + "/" + id}, {"PUT", base + "/" + id + "?offset=0"}, {"DELETE", base + "/" + id},
	} {
		if r := e.call(c.method, c.path, "", []byte("x"), "X-Setup-Token", "wrong"); r.status != http.StatusForbidden {
			t.Errorf("%s with a wrong token = %d %s", c.method, r.status, r.body)
		}
	}
	if r := e.call("POST", base+"/"+id+"/complete", "", map[string]any{"token": "wrong"}); r.status != http.StatusForbidden {
		t.Errorf("complete with a wrong token = %d %s", r.status, r.body)
	}
	if r := e.call("POST", "/api/v1/setup/restore", "", map[string]any{"token": "wrong", "upload_id": id, "confirm": "RESTORE"}); r.status != http.StatusForbidden {
		t.Errorf("restore with a wrong token = %d %s", r.status, r.body)
	}
	// Setup and admin uploads are kept apart.
	if r := e.call("GET", adminUploads+"/"+id, "admin", nil); r.status != http.StatusNotFound {
		t.Errorf("a setup upload seen as an admin upload: %d", r.status)
	}
	if r := e.call("GET", base+"/"+id, "", nil, "X-Setup-Token", "setup-token"); r.status != http.StatusOK || r.json(t)["received"] != float64(0) {
		t.Errorf("status with the token = %d %s", r.status, r.body)
	}
}

func TestSetupEndpointsRefuseOnceSetUp(t *testing.T) {
	e := newAPIEnv(t, func(o *APIOptions) {
		o.CheckSetupToken = func(context.Context, netip.Addr, string) error { return auth.ErrSetupDone }
	})
	const base = "/api/v1/setup/restore"
	for _, c := range []struct{ method, path string }{
		{"POST", base + "/uploads"}, {"GET", base + "/uploads/x"}, {"PUT", base + "/uploads/x?offset=0"},
		{"DELETE", base + "/uploads/x"}, {"POST", base + "/uploads/x/complete"}, {"POST", base},
	} {
		r := e.call(c.method, c.path, "", map[string]any{"token": "setup-token"}, "X-Setup-Token", "setup-token")
		if r.status != http.StatusConflict || r.errorCode(t) != "setup_done" {
			t.Errorf("%s %s = %d %s", c.method, c.path, r.status, r.body)
		}
	}
}
