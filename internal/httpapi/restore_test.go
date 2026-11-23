// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// While a backup is being restored, the API answers 503 restoring with
// Retry-After, except for the version and the restore status; the probes and
// the web interface keep working.
func TestRestoreGate(t *testing.T) {
	var restoring atomic.Bool
	restoring.Store(true)
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	e := newEnv(t, func(d *Deps) {
		d.Restoring = restoring.Load
		d.SettingsList = ok
		d.Features = map[string]http.Handler{
			"ingestTrivy": ok, "getPortfolio": ok, "getRestoreStatus": ok, "putBackupUploadChunk": ok,
		}
	})
	for _, c := range []struct{ method, target, role, body string }{
		{"POST", "/api/v1/ingest/trivy", "", "{}"},
		{"GET", "/api/v1/settings", "admin", ""},
		{"GET", "/api/v1/portfolio", "viewer", ""},
		{"PUT", "/api/v1/admin/backup-uploads/abc?offset=0", "admin", "data"},
		{"GET", "/api/v1/me", "admin", ""},
		{"GET", "/api/v1/no-such-endpoint", "", ""},
	} {
		rec := e.do(c.method, c.target, c.role, c.body)
		assertError(t, rec, http.StatusServiceUnavailable, "restoring")
		if ra := rec.Header().Get("Retry-After"); ra != "30" {
			t.Errorf("%s %s: Retry-After = %q, want 30", c.method, c.target, ra)
		}
	}
	for _, target := range []string{"/api/v1/version", "/api/v1/restore/status", "/healthz", "/readyz", "/", "/services/3"} {
		if rec := e.do("GET", target, "", ""); rec.Code != http.StatusOK {
			t.Errorf("GET %s during a restore = %d, want 200", target, rec.Code)
		}
	}

	restoring.Store(false)
	for _, target := range []string{"/api/v1/portfolio", "/api/v1/settings"} {
		if rec := e.do("GET", target, "admin", ""); rec.Code != http.StatusOK {
			t.Errorf("GET %s after the restore = %d, want 200", target, rec.Code)
		}
	}
}

func TestRestoreGateOffWithoutFunc(t *testing.T) {
	e := newEnv(t, func(d *Deps) {
		d.Features = map[string]http.Handler{"getPortfolio": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})}
	})
	if rec := e.do("GET", "/api/v1/portfolio", "viewer", ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// Only the transfers themselves bypass the generic limits: sending a part
// of an upload and downloading a backup. The other upload requests read at
// most a small JSON body and are limited like any request.
func TestLimitedAPIRequestExemptsTransfers(t *testing.T) {
	const name = "/api/v1/admin/backups/scanopsis-backup-20261123T100000Z-manual-abc123.tar.gz"
	for _, c := range []struct {
		method, path string
		want         bool
	}{
		{"GET", "/api/v1/admin/backups", true},
		{"GET", name + "/preview", true},
		{"POST", name + "/restore", true},
		{"GET", name + "/download", false},
		{"HEAD", name + "/download", false},
		{"POST", name + "/download", true},
		{"GET", "/api/v1/services/3/download", true},
		{"POST", "/api/v1/admin/backup-uploads", true},
		{"PUT", "/api/v1/admin/backup-uploads/abc", false},
		{"GET", "/api/v1/admin/backup-uploads/abc", true},
		{"DELETE", "/api/v1/admin/backup-uploads/abc", true},
		{"POST", "/api/v1/admin/backup-uploads/abc/complete", true},
		{"PUT", "/api/v1/admin/backup-uploads/abc/complete", true},
		{"POST", "/api/v1/setup/restore", true},
		{"POST", "/api/v1/setup/restore/uploads", true},
		{"PUT", "/api/v1/setup/restore/uploads/abc", false},
		{"GET", "/api/v1/setup/restore/uploads/abc", true},
		{"POST", "/api/v1/setup/restore/uploads/abc/complete", true},
		{"POST", "/api/v1/ingest/trivy", false},
		{"GET", "/api/v1/portfolio", true},
		{"GET", "/healthz", false},
	} {
		if got := limitedAPIRequest(c.method, c.path); got != c.want {
			t.Errorf("limitedAPIRequest(%s %s) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

// Completing an upload is an ordinary request: it waits for a slot and is
// answered busy when none comes free.
func TestUploadCompleteIsLimited(t *testing.T) {
	hold := newGate()
	done := newGate()
	close(done.release)
	e := newEnv(t, func(d *Deps) {
		d.MaxConcurrent = 1
		d.BusyWait = 20 * time.Millisecond
		d.Features = map[string]http.Handler{"getPortfolio": hold, "completeBackupUpload": done, "completeSetupRestoreUpload": done}
	})
	finished := make(chan struct{})
	go func() { e.do("GET", "/api/v1/portfolio", "viewer", ""); close(finished) }()
	<-hold.entered
	assertError(t, e.do("POST", "/api/v1/admin/backup-uploads/abc/complete", "admin", ""), http.StatusServiceUnavailable, "busy")
	assertError(t, e.do("POST", "/api/v1/setup/restore/uploads/abc/complete", "", ""), http.StatusServiceUnavailable, "busy")
	close(hold.release)
	<-finished
	if rec := e.do("POST", "/api/v1/admin/backup-uploads/abc/complete", "admin", ""); rec.Code != http.StatusOK {
		t.Errorf("complete with a free slot = %d", rec.Code)
	}
}

// A chunk of an upload is larger than the API's body limit and is not
// buffered or counted by the concurrency limit.
func TestUploadChunksBypassTheBodyLimit(t *testing.T) {
	var got atomic.Int64
	hold := newGate()
	e := newEnv(t, func(d *Deps) {
		d.MaxConcurrent = 1
		d.BusyWait = 20 * time.Millisecond
		d.Features = map[string]http.Handler{
			"getPortfolio": hold,
			"putBackupUploadChunk": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n, err := io.Copy(io.Discard, r.Body)
				if err != nil {
					t.Errorf("reading the chunk: %v", err)
				}
				got.Store(n)
				w.WriteHeader(http.StatusOK)
			}),
		}
	})
	done := make(chan struct{})
	go func() { e.do("GET", "/api/v1/portfolio", "viewer", ""); close(done) }()
	<-hold.entered
	big := strings.Repeat("x", 3<<20)
	if rec := e.do("PUT", "/api/v1/admin/backup-uploads/abc?offset=0", "admin", big); rec.Code != http.StatusOK || got.Load() != int64(len(big)) {
		t.Errorf("chunk while every slot is taken: status %d, handler read %d bytes, want 200 and %d", rec.Code, got.Load(), len(big))
	}
	close(hold.release)
	<-done
}
