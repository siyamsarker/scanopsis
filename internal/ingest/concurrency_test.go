// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/settings"
)

// blockingSaver holds every save until it is released.
type blockingSaver struct {
	fakeSaver
	entered chan struct{}
	release chan struct{}
}

func (b *blockingSaver) save(ctx context.Context, in *saveInput) (Result, error) {
	b.entered <- struct{}{}
	<-b.release
	return b.fakeSaver.save(ctx, in)
}

// uploadAsync sends a valid upload in the background.
func uploadAsync(ctx context.Context, e *hEnv, body []byte) <-chan *httptest.ResponseRecorder {
	tok := e.g.token(e.t, nil)
	out := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		hr := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/ingest/trivy", bytes.NewReader(body))
		hr.Header.Set("Authorization", "Bearer "+tok)
		hr.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.in.Handlers()["ingestTrivy"].ServeHTTP(w, hr)
		out <- w
	}()
	return out
}

func TestIngestLimitsConcurrentUploads(t *testing.T) {
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	e := newHEnv(t, nil)
	bs := &blockingSaver{entered: make(chan struct{}, 16), release: make(chan struct{})}
	e.in.store = bs
	if cap(e.in.sem) != DefaultMaxConcurrentUploads {
		t.Fatalf("default limit %d", cap(e.in.sem))
	}
	var results []<-chan *httptest.ResponseRecorder
	for range DefaultMaxConcurrentUploads {
		results = append(results, uploadAsync(context.Background(), e, body))
		<-bs.entered
	}
	// The next upload waits for a slot.
	fifth := uploadAsync(context.Background(), e, body)
	select {
	case <-bs.entered:
		t.Fatal("a fifth upload was processed concurrently")
	case <-fifth:
		t.Fatal("a fifth upload finished while the others hold the slots")
	case <-time.After(100 * time.Millisecond):
	}
	// A waiter whose request ends gets 503 busy with Retry-After.
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := uploadAsync(ctx, e, body)
	time.Sleep(50 * time.Millisecond)
	cancel()
	w := <-cancelled
	if w.Code != http.StatusServiceUnavailable || errCode(t, w) != "busy" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("%d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
	if !strings.Contains(w.Body.String(), "The server is processing other uploads. Retry later.") {
		t.Errorf("message: %s", w.Body)
	}
	// The token was valid, so the rejection is recorded.
	if ev := e.audit.list(); len(ev) != 1 || ev[0].detail["reason"] != "busy" || ev[0].detail["project_path"] != "platform/payments-api" {
		t.Errorf("audit = %+v", ev)
	}
	// A freed slot lets the waiter proceed.
	bs.release <- struct{}{}
	<-bs.entered
	close(bs.release)
	for _, r := range append(results, fifth) {
		if w := <-r; w.Code != http.StatusCreated {
			t.Errorf("%d %s", w.Code, w.Body)
		}
	}
	// Every slot was released.
	if len(e.in.sem) != 0 {
		t.Errorf("%d slots still held", len(e.in.sem))
	}
}

func TestIngestConcurrencyLimitIsConfigurable(t *testing.T) {
	e := newHEnv(t, func(o *Options) { o.MaxConcurrentUploads = 1 })
	if cap(e.in.sem) != 1 {
		t.Fatalf("limit %d", cap(e.in.sem))
	}
	e = newHEnv(t, func(o *Options) { o.MaxConcurrentUploads = -3 })
	if cap(e.in.sem) != DefaultMaxConcurrentUploads {
		t.Fatalf("limit %d", cap(e.in.sem))
	}
}

func TestIngestSlotIsReleasedAfterFailures(t *testing.T) {
	e := newHEnv(t, func(o *Options) { o.MaxConcurrentUploads = 1 })
	for range 3 {
		if w := e.do(req{body: []byte(`{"SchemaVersion":`)}); w.Code != 400 {
			t.Fatalf("%d", w.Code)
		}
	}
	if w := e.do(req{body: []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)}); w.Code != 201 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(e.in.sem) != 0 {
		t.Errorf("%d slots still held", len(e.in.sem))
	}
}

// Uploads that fail the token, namespace or ref check do not wait for a slot.
func TestIngestRejectedUploadsDoNotWaitForASlot(t *testing.T) {
	e := newHEnv(t, func(o *Options) { o.MaxConcurrentUploads = 1 })
	e.in.sem <- struct{}{} // the only slot is taken
	if w := e.do(req{noAuth: true}); w.Code != 401 {
		t.Fatalf("bad token: %d", w.Code)
	}
	e.cfg.set(func(s *settings.Snapshot) { s.IngestAllowedNamespaces = []string{"other"} })
	if w := e.do(req{body: []byte(`{}`)}); w.Code != 403 {
		t.Fatalf("namespace: %d", w.Code)
	}
}
