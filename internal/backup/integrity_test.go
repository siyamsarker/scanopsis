// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// listed returns the entry of the backup called name from the list API.
func (e *apiEnv) listed(name string) map[string]any {
	e.t.Helper()
	r := e.call("GET", "/api/v1/admin/backups", "admin", nil)
	list, _ := r.json(e.t)["backups"].([]any)
	for _, b := range list {
		if m, _ := b.(map[string]any); m["name"] == name {
			return m
		}
	}
	e.t.Fatalf("%s is not listed: %s", name, r.body)
	return nil
}

// A backup copied into the folder in part, as after a full disk or an
// interrupted copy, is checked in the background and listed as damaged,
// also after a restart; the restore is refused before the code.
func TestListMarksADamagedFile(t *testing.T) {
	e := newAPIEnv(t, nil)
	good := goodArchive(t)
	goodName := writeBackup(t, e.dir, good)
	badName := writeBackup(t, e.dir, good[:len(good)/2])
	const problem = "The file is damaged: the file ends too early."

	deadline := time.Now().Add(5 * time.Second)
	bad := e.listed(badName)
	for bad["readable"] != false && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		bad = e.listed(badName)
	}
	if bad["readable"] != false || bad["problem"] != problem {
		t.Fatalf("damaged backup listed as %v", bad)
	}
	if ok := e.listed(goodName); ok["readable"] != true || ok["problem"] != nil {
		t.Errorf("good backup listed as %v", ok)
	}

	r := e.call("POST", "/api/v1/admin/backups/"+badName+"/restore", "admin", map[string]any{"confirm": "RESTORE", "code": "123456"})
	if r.status != http.StatusUnprocessableEntity || r.errorCode(t) != "invalid_backup" || r.errorMessage(t) != problem {
		t.Errorf("restore of the damaged backup = %d %s", r.status, r.body)
	}

	// After a restart the outcome is known without reading the file again.
	dir, err := OpenDir(e.dir.Root(), apiFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	list, err := dir.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range list {
		if b.Name == badName && (b.Readable || b.Problem != problem) {
			t.Errorf("after a restart the damaged backup is %+v", b)
		}
	}
}

// Backups made or checked here are not read again for the list.
func TestCheckedFilesAreNotReadAgain(t *testing.T) {
	e := newAPIEnv(t, nil)
	var reads atomic.Int32
	real := e.svc.verifyFile
	e.svc.verifyFile = func(ctx context.Context, path string) error {
		reads.Add(1)
		return real(ctx, path)
	}
	data := goodArchive(t)
	id := e.startUpload("good.tar.gz", len(data))
	e.send(id, data, 0, len(data))
	done := e.complete(adminUploads+"/"+id, "admin", nil)
	backup, _ := done["backup"].(map[string]any)
	name, _ := backup["name"].(string)
	for range 3 {
		if b := e.listed(name); b["readable"] != true {
			t.Fatalf("uploaded backup listed as %v", b)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if n := reads.Load(); n != 0 {
		t.Errorf("the uploaded backup was read %d times for the list", n)
	}
}

// The list shows the open uploads of the admin panel, so an administrator
// can cancel one that was left behind, and the refusal of a third upload
// says so.
func TestListShowsOpenUploads(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	e := newAPIEnv(t, func(o *APIOptions) {
		o.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	})
	a := e.startUpload("left-behind.tar.gz", 100)
	e.send(a, make([]byte, 40), 0, 40)
	mu.Lock()
	now = now.Add(3 * time.Minute)
	mu.Unlock()
	b := e.startUpload("other-tab.tar.gz", 100)
	if r := e.call("POST", "/api/v1/setup/restore/uploads", "", map[string]any{"token": "setup-token", "file_name": "setup.tar.gz", "size": 100}); r.status != http.StatusCreated {
		t.Fatalf("setup upload = %d %s", r.status, r.body)
	}

	r := e.call("GET", "/api/v1/admin/backups", "admin", nil)
	ups, _ := r.json(t)["uploads"].([]any)
	if len(ups) != 2 {
		t.Fatalf("uploads = %v, want the two of the admin panel", ups)
	}
	first, _ := ups[0].(map[string]any)
	second, _ := ups[1].(map[string]any)
	if first["id"] != a || first["file_name"] != "left-behind.tar.gz" || first["received"] != float64(40) ||
		first["size"] != float64(100) || first["state"] != "uploading" || first["updated_at"] != "2026-11-23T10:00:00Z" {
		t.Errorf("first upload = %v", first)
	}
	if second["id"] != b {
		t.Errorf("second upload = %v, want the newer one second", second)
	}

	r = e.call("POST", adminUploads, "admin", map[string]any{"file_name": "c.tar.gz", "size": 100})
	if r.status != http.StatusConflict || !strings.Contains(r.errorMessage(t), "Unfinished uploads on the Backups page") {
		t.Errorf("third upload = %d %s", r.status, r.body)
	}
	if r := e.call("DELETE", adminUploads+"/"+a, "admin", nil); r.status != http.StatusNoContent {
		t.Fatalf("cancel = %d %s", r.status, r.body)
	}
	if r := e.call("GET", "/api/v1/admin/backups", "admin", nil); len(r.json(t)["uploads"].([]any)) != 1 {
		t.Errorf("uploads after cancelling one = %s", r.body)
	}
}
