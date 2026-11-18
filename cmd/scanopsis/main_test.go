// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestRunVersion(t *testing.T) {
	oldVersion, oldCommit := version, commit
	t.Cleanup(func() { version, commit = oldVersion, oldCommit })
	version, commit = "1.2.3", "abc1234"

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	want := "scanopsis 1.2.3 (commit abc1234, " + runtime.Version() + ")\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--no-such-flag"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "no-such-flag") {
		t.Fatalf("stderr should name the bad flag, got %q", stderr.String())
	}
}

func TestResolveCommitPrefersBuildValue(t *testing.T) {
	old := commit
	t.Cleanup(func() { commit = old })

	commit = "deadbeef"
	if got := resolveCommit(); got != "deadbeef" {
		t.Fatalf("resolveCommit() = %q, want deadbeef", got)
	}
	commit = ""
	if got := resolveCommit(); got == "" {
		t.Fatal("resolveCommit() must never return an empty string")
	}
}

func TestNewHandler(t *testing.T) {
	t.Run("without web build", func(t *testing.T) {
		rec := httptest.NewRecorder()
		newHandler(nil, false).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
	})
	t.Run("with web build", func(t *testing.T) {
		assets := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>x</title>")}}
		rec := httptest.NewRecorder()
		newHandler(assets, true).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<title>x</title>") {
			t.Fatalf("body does not contain index.html: %q", rec.Body.String())
		}
	})
}

func TestServeShutsDownOnCancel(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	go func() { done <- serve(ctx, ln, h) }()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+ln.Addr().String()+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request to running server failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "ok" {
		t.Fatalf("body = %q, want ok", body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v, want nil after graceful shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after context cancellation")
	}
}
