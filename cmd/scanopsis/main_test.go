// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestRunVersion(t *testing.T) {
	oldVersion, oldCommit := version, commit
	t.Cleanup(func() { version, commit = oldVersion, oldCommit })
	version, commit = "1.2.3", "abc1234"

	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"--version"}, &stdout, &stderr, env(nil)); code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	want := "scanopsis 1.2.3 (commit abc1234, " + runtime.Version() + ")\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"--no-such-flag"}, &stdout, &stderr, env(nil)); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "no-such-flag") {
		t.Fatalf("stderr should name the bad flag, got %q", stderr.String())
	}
}

func TestRunConfigurationError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), nil, &stdout, &stderr, env(nil)); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	for _, want := range []string{"configuration error", "SCANOPSIS_DATABASE_URL", "SCANOPSIS_SECRET_KEY"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q: %s", want, stderr.String())
		}
	}
}

func TestRunRoutesAdminCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"admin", "--help"}, &stdout, &stderr, env(nil)); code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "reset-password") {
		t.Fatalf("admin help not shown: %q", stdout.String())
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

func TestHealthcheck(t *testing.T) {
	var status int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("healthcheck requested %s", r.URL.Path)
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	cfg := env(map[string]string{"SCANOPSIS_LISTEN_ADDR": ":" + port})

	status = http.StatusOK
	if code := run(t.Context(), []string{"healthcheck"}, io.Discard, io.Discard, cfg); code != 0 {
		t.Fatalf("healthy server: exit %d, want 0", code)
	}
	status = http.StatusServiceUnavailable
	var stderr bytes.Buffer
	if code := run(t.Context(), []string{"healthcheck"}, io.Discard, &stderr, cfg); code != 1 || !strings.Contains(stderr.String(), "503") {
		t.Fatalf("unhealthy server: exit %d, stderr %q", code, stderr.String())
	}
	srv.Close()
	if code := run(t.Context(), []string{"healthcheck"}, io.Discard, io.Discard, cfg); code != 1 {
		t.Fatalf("stopped server: exit %d, want 1", code)
	}
	bad := env(map[string]string{"SCANOPSIS_LISTEN_ADDR": "no-port"})
	if code := run(t.Context(), []string{"healthcheck"}, io.Discard, io.Discard, bad); code != 1 {
		t.Fatalf("invalid listen address: exit %d, want 1", code)
	}
}

// selfSignedServer returns a TLS test server and its certificate as PEM.
func selfSignedServer(t *testing.T) (*httptest.Server, []byte) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	t.Cleanup(srv.Close)
	return srv, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
}

func TestNewHTTPClientTrustsCAFile(t *testing.T) {
	srv, caPEM := selfSignedServer(t)

	plain, err := newHTTPClient("")
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := plain.Get(srv.URL); err == nil { //nolint:noctx // test request
		_ = resp.Body.Close()
		t.Fatal("system roots trusted a self-signed certificate")
	}

	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	withCA, err := newHTTPClient(caFile)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := withCA.Get(srv.URL) //nolint:noctx // test request
	if err != nil {
		t.Fatalf("CA_FILE certificate not trusted: %v", err)
	}
	_ = resp.Body.Close()
}

func TestNewHTTPClientRejectsBadCAFile(t *testing.T) {
	if _, err := newHTTPClient(filepath.Join(t.TempDir(), "missing.pem")); err == nil || !strings.Contains(err.Error(), "SCANOPSIS_CA_FILE") {
		t.Fatalf("missing file: err = %v", err)
	}
	notPEM := filepath.Join(t.TempDir(), "x.pem")
	_ = os.WriteFile(notPEM, []byte("not a certificate"), 0o600)
	if _, err := newHTTPClient(notPEM); err == nil || !strings.Contains(err.Error(), "contains no PEM certificates") {
		t.Fatalf("non-PEM file: err = %v", err)
	}
}

type countingRemover struct{ calls chan struct{} }

func (c countingRemover) RemoveExpired(context.Context) (int64, error) {
	c.calls <- struct{}{}
	return 1, nil
}

func TestRemoveExpiredSessionsStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		removeExpiredSessions(ctx, countingRemover{calls: make(chan struct{}, 1)}, discardLogger())
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("clean-up loop did not stop when the context ended")
	}
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
	go func() { done <- serve(ctx, ln, h, discardLogger(), 5*time.Second) }()

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

// A request that outlives the grace period is cut off. That is what a
// deliberate stop does to a slow upload, so serve reports it as a warning with
// the number of requests cut off and returns nil, which makes the process
// exit 0.
func TestServeCutsOffSlowRequestsAfterGrace(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "late")
	})
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, ln, h, log, 200*time.Millisecond) }()

	clientDone := make(chan error, 1)
	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+ln.Addr().String()+"/", nil)
		resp, err := http.DefaultClient.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		clientDone <- err
	}()
	<-started
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v, want nil when only the grace period ran out", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after the grace period")
	}
	if err := <-clientDone; err == nil {
		t.Error("the slow request completed; it should have been cut off")
	}
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &rec); err != nil {
		t.Fatalf("log output %q: %v", logs.String(), err)
	}
	if rec["level"] != "WARN" || rec["requests_cut_off"] != float64(1) {
		t.Errorf("log = %v, want a WARN with requests_cut_off 1", rec)
	}
}

func TestMergeFeatures(t *testing.T) {
	h := http.NotFoundHandler()
	got, err := mergeFeatures(map[string]http.Handler{"a": h}, map[string]http.Handler{"b": h})
	if err != nil || len(got) != 2 {
		t.Fatalf("merge = %v, %v", got, err)
	}
	if _, err := mergeFeatures(map[string]http.Handler{"a": h}, map[string]http.Handler{"a": h}); err == nil || !strings.Contains(err.Error(), "operation a is handled twice") {
		t.Fatalf("duplicate: %v", err)
	}
}
