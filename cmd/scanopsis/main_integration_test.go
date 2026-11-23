// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pquerna/otp/totp"

	"github.com/siyamsarker/scanopsis/internal/config"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

// syncBuffer is a bytes.Buffer safe for the server's concurrent log writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// dbURL returns the connection URL of the test's own database. The pool's
// ConnString is the base URL it was parsed from, which names the shared
// database, not the per-test one.
func dbURL(t *testing.T, pool interface{ Config() *pgxpool.Config }) string {
	t.Helper()
	cfg := pool.Config()
	u, err := url.Parse(cfg.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + cfg.ConnConfig.Database
	return u.String()
}

func freeAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

type client struct {
	t      *testing.T
	base   string
	cookie *http.Cookie
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(c.t.Context(), method, c.base+path, rd)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	for _, ck := range resp.Cookies() {
		if ck.Name == "scanopsis_session" {
			c.cookie = ck
		}
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func setupToken(t *testing.T, logs string) string {
	t.Helper()
	sc := bufio.NewScanner(strings.NewReader(logs))
	for sc.Scan() {
		var line map[string]any
		if json.Unmarshal(sc.Bytes(), &line) == nil {
			if tok, ok := line["setup_token"].(string); ok {
				return tok
			}
		}
	}
	t.Fatalf("no setup token in the log:\n%s", logs)
	return ""
}

func TestIntegrationServerSetupAndTwoFactor(t *testing.T) {
	pool := storetest.New(t)
	addr := freeAddr(t)
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	cfg, err := config.Load(env(map[string]string{
		"SCANOPSIS_DATABASE_URL": dbURL(t, pool),
		"SCANOPSIS_PUBLIC_URL":   "http://" + addr,
		"SCANOPSIS_LISTEN_ADDR":  addr,
		"SCANOPSIS_SECRET_KEY":   key,
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveApp(ctx, cfg, slog.New(slog.NewJSONHandler(logs, nil))) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serveApp returned %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("server did not stop")
		}
	})

	c := &client{t: t, base: "http://" + addr}
	deadline := time.Now().Add(20 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(t.Context(), "GET", c.base+"/readyz", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("server not ready; log:\n%s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}

	if code, body := c.do("GET", "/api/v1/setup", nil); code != 200 || body["required"] != true {
		t.Fatalf("setup status = %d %v, want required", code, body)
	}
	token := setupToken(t, logs.String())

	if code, body := c.do("POST", "/api/v1/setup", map[string]string{"token": "wrong", "username": "root", "password": "a long enough password"}); code != 403 || errCode(body) != "invalid_setup_token" {
		t.Fatalf("wrong token = %d %v", code, body)
	}
	if code, body := c.do("POST", "/api/v1/setup", map[string]string{"token": token, "username": "root", "password": "a long enough password"}); code != 204 {
		t.Fatalf("setup = %d %v", code, body)
	}
	if c.cookie == nil {
		t.Fatal("setup did not set a session cookie")
	}

	code, me := c.do("GET", "/api/v1/me", nil)
	if code != 200 || me["username"] != "root" || me["role"] != "admin" || me["mfa_enrolled"] != false || me["mfa_verified"] != false {
		t.Fatalf("me after setup = %d %v", code, me)
	}
	if code, body := c.do("GET", "/api/v1/users", nil); code != 403 || errCode(body) != "mfa_enrollment_required" {
		t.Fatalf("users before enrolment = %d %v", code, body)
	}

	code, enr := c.do("POST", "/api/v1/auth/mfa/enrollment", nil)
	secret, _ := enr["secret"].(string)
	if code != 200 || secret == "" || !strings.HasPrefix(enr["otpauth_uri"].(string), "otpauth://totp/") {
		t.Fatalf("enrolment = %d %v", code, enr)
	}
	otp, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	code, conf := c.do("POST", "/api/v1/auth/mfa/enrollment/confirm", map[string]string{"code": otp})
	if codes, _ := conf["recovery_codes"].([]any); code != 200 || len(codes) != 10 {
		t.Fatalf("confirm = %d %v", code, conf)
	}

	code, users := c.do("GET", "/api/v1/users", nil)
	if items, _ := users["items"].([]any); code != 200 || len(items) != 1 {
		t.Fatalf("users after enrolment = %d %v", code, users)
	}
	if code, body := c.do("GET", "/api/v1/setup", nil); code != 200 || body["required"] != false {
		t.Fatalf("setup status after setup = %d %v", code, body)
	}

	// Settings apply at run time: disabling local sign-in without GitLab is
	// refused; a valid change is saved and reported with its source.
	if code, body := c.do("PATCH", "/api/v1/settings", map[string]any{"auth.local_login_enabled": false}); code != 400 || errCode(body) != "invalid_settings" {
		t.Fatalf("disable local sign-in = %d %v", code, body)
	}
	code, list := c.do("PATCH", "/api/v1/settings", map[string]any{"session.idle_timeout": "30m"})
	if code != 200 {
		t.Fatalf("change idle timeout = %d %v", code, list)
	}
	found := false
	for _, it := range list["items"].([]any) {
		s := it.(map[string]any)
		if s["key"] == "session.idle_timeout" {
			found = s["value"] == "30m" && s["source"] == "ui" && s["updated_by"] == "local:root"
		}
	}
	if !found {
		t.Fatalf("settings list does not show the change: %v", list)
	}
	if code, body := c.do("GET", "/api/v1/auth/methods", nil); code != 200 || body["gitlab"] != false || body["local"] != true {
		t.Fatalf("methods = %d %v", code, body)
	}

	if strings.Contains(logs.String(), "a long enough password") || strings.Contains(logs.String(), secret) {
		t.Fatal("a password or TOTP secret reached the log")
	}
}

// startServer runs the server against the test's database with the given
// backup folder and returns its base URL and log once it is ready.
func startServer(t *testing.T, backupDir string) (string, *syncBuffer) {
	t.Helper()
	pool := storetest.New(t)
	addr := freeAddr(t)
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	cfg, err := config.Load(env(map[string]string{
		"SCANOPSIS_DATABASE_URL": dbURL(t, pool),
		"SCANOPSIS_PUBLIC_URL":   "http://" + addr,
		"SCANOPSIS_LISTEN_ADDR":  addr,
		"SCANOPSIS_SECRET_KEY":   key,
		"SCANOPSIS_BACKUP_DIR":   backupDir,
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveApp(ctx, cfg, slog.New(slog.NewJSONHandler(logs, nil))) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serveApp returned %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("server did not stop")
		}
	})
	base := "http://" + addr
	deadline := time.Now().Add(20 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(t.Context(), "GET", base+"/readyz", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return base, logs
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("server not ready; log:\n%s", logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// backupWarnings counts the log lines that warn about the backup folder.
func backupWarnings(logs string) (n int, folder string) {
	sc := bufio.NewScanner(strings.NewReader(logs))
	for sc.Scan() {
		var line map[string]any
		if json.Unmarshal(sc.Bytes(), &line) == nil && line["level"] == "WARN" {
			if f, ok := line["folder"].(string); ok {
				n++
				folder = f
			}
		}
	}
	return n, folder
}

func TestIntegrationServerStartsWithoutBackupFolder(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-folder")
	base, logs := startServer(t, missing)
	c := &client{t: t, base: base}
	if code, body := c.do("GET", "/api/v1/setup", nil); code != 200 || body["required"] != true {
		t.Fatalf("setup status = %d %v; the server must serve", code, body)
	}
	n, folder := backupWarnings(logs.String())
	if n != 1 || folder != missing {
		t.Fatalf("%d backup folder warnings naming %q; want one naming %q\n%s", n, folder, missing, logs.String())
	}
	if !strings.Contains(logs.String(), "fix it and restart") {
		t.Fatalf("the warning does not say how to fix it:\n%s", logs.String())
	}
}

func TestIntegrationServerStartsWithBackupFolder(t *testing.T) {
	_, logs := startServer(t, t.TempDir())
	if n, _ := backupWarnings(logs.String()); n != 0 {
		t.Fatalf("%d backup folder warnings with a usable folder:\n%s", n, logs.String())
	}
}
