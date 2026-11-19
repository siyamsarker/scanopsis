// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func load(t *testing.T, env map[string]string, files map[string]string) (*Config, error) {
	t.Helper()
	getenv := func(k string) string { return env[k] }
	readFile := func(p string) ([]byte, error) {
		if v, ok := files[p]; ok {
			return []byte(v), nil
		}
		return nil, fs.ErrNotExist
	}
	return Load(getenv, readFile)
}

func minimal() map[string]string {
	return map[string]string{
		"SCANOPSIS_PUBLIC_URL":   "https://scanopsis.example.com",
		"SCANOPSIS_DATABASE_URL": "postgres://u:p@db/scanopsis",
	}
}

func with(base map[string]string, kv ...string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func TestLoadDefaults(t *testing.T) {
	c, err := load(t, minimal(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q", c.ListenAddr)
	}
	if c.PublicURL.String() != "https://scanopsis.example.com" || !c.Secure() {
		t.Errorf("PublicURL = %v, Secure = %v", c.PublicURL, c.Secure())
	}
	if c.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v", c.LogLevel)
	}
	if c.SessionIdleTimeout != 8*time.Hour || c.SessionAbsoluteTimeout != 24*time.Hour {
		t.Errorf("session timeouts = %v / %v", c.SessionIdleTimeout, c.SessionAbsoluteTimeout)
	}
	if !c.LocalLogin || c.LocalAdminUsername != "admin" || c.LocalAdminPassword != "" {
		t.Errorf("local login = %v %q %q", c.LocalLogin, c.LocalAdminUsername, c.LocalAdminPassword)
	}
	if c.GitLab != nil {
		t.Errorf("GitLab = %+v, want nil", c.GitLab)
	}
}

func TestLoadFull(t *testing.T) {
	env := with(minimal(),
		"SCANOPSIS_PUBLIC_URL", "http://localhost:8080/",
		"SCANOPSIS_LISTEN_ADDR", "127.0.0.1:9000",
		"SCANOPSIS_LOG_LEVEL", "debug",
		"SCANOPSIS_TRUSTED_PROXIES", "10.0.0.0/8, 192.168.1.10 ,fd00::/8",
		"SCANOPSIS_CA_FILE", "/etc/ssl/internal.pem",
		"SCANOPSIS_SESSION_IDLE_TIMEOUT", "30m",
		"SCANOPSIS_SESSION_ABSOLUTE_TIMEOUT", "12h",
		"SCANOPSIS_GITLAB_URL", "https://gitlab.example.com/",
		"SCANOPSIS_GITLAB_CLIENT_ID", "client",
		"SCANOPSIS_GITLAB_CLIENT_SECRET_FILE", "/run/secrets/gitlab",
		"SCANOPSIS_GITLAB_ALLOWED_GROUPS", "platform, security/appsec",
		"SCANOPSIS_ADMIN_USERNAMES", "alice,bob",
		"SCANOPSIS_LOCAL_LOGIN_ENABLED", "false",
		"SCANOPSIS_LOCAL_ADMIN_USERNAME", "root",
		"SCANOPSIS_LOCAL_ADMIN_PASSWORD_FILE", "/run/secrets/admin",
	)
	files := map[string]string{"/run/secrets/gitlab": "s3cret\n", "/run/secrets/admin": "correct horse battery\n"}
	c, err := load(t, env, files)
	if err != nil {
		t.Fatal(err)
	}
	if c.PublicURL.String() != "http://localhost:8080" || c.Secure() {
		t.Errorf("PublicURL = %v, Secure = %v", c.PublicURL, c.Secure())
	}
	if c.ListenAddr != "127.0.0.1:9000" || c.LogLevel != slog.LevelDebug || c.CAFile != "/etc/ssl/internal.pem" {
		t.Errorf("unexpected basic settings: %+v", c)
	}
	wantProxies := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("192.168.1.10/32"),
		netip.MustParsePrefix("fd00::/8"),
	}
	if len(c.TrustedProxies) != len(wantProxies) {
		t.Fatalf("TrustedProxies = %v", c.TrustedProxies)
	}
	for i := range wantProxies {
		if c.TrustedProxies[i] != wantProxies[i] {
			t.Errorf("TrustedProxies[%d] = %v, want %v", i, c.TrustedProxies[i], wantProxies[i])
		}
	}
	if c.SessionIdleTimeout != 30*time.Minute || c.SessionAbsoluteTimeout != 12*time.Hour {
		t.Errorf("session timeouts = %v / %v", c.SessionIdleTimeout, c.SessionAbsoluteTimeout)
	}
	g := c.GitLab
	if g == nil {
		t.Fatal("GitLab is nil")
	}
	if g.URL != "https://gitlab.example.com" || g.ClientID != "client" || g.ClientSecret != "s3cret" {
		t.Errorf("GitLab = %+v", g)
	}
	if strings.Join(g.AllowedGroups, "|") != "platform|security/appsec" || strings.Join(g.AdminUsernames, "|") != "alice|bob" {
		t.Errorf("GitLab lists = %v %v", g.AllowedGroups, g.AdminUsernames)
	}
	if c.LocalLogin || c.LocalAdminUsername != "root" || c.LocalAdminPassword != "correct horse battery" {
		t.Errorf("local = %v %q %q", c.LocalLogin, c.LocalAdminUsername, c.LocalAdminPassword)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name  string
		env   map[string]string
		files map[string]string
		want  []string
	}{
		{"missing required", map[string]string{}, nil,
			[]string{"SCANOPSIS_DATABASE_URL is required", "SCANOPSIS_PUBLIC_URL is required"}},
		{"public url with path", with(minimal(), "SCANOPSIS_PUBLIC_URL", "https://example.com/scanopsis"), nil,
			[]string{"SCANOPSIS_PUBLIC_URL must not contain a path"}},
		{"public url not http", with(minimal(), "SCANOPSIS_PUBLIC_URL", "ftp://example.com"), nil,
			[]string{"SCANOPSIS_PUBLIC_URL must be an absolute http or https URL"}},
		{"public url with credentials", with(minimal(), "SCANOPSIS_PUBLIC_URL", "https://u:p@example.com"), nil,
			[]string{"SCANOPSIS_PUBLIC_URL must not contain"}},
		{"secret and file both set", with(minimal(), "SCANOPSIS_LOCAL_ADMIN_PASSWORD", "x", "SCANOPSIS_LOCAL_ADMIN_PASSWORD_FILE", "/f"), nil,
			[]string{"SCANOPSIS_LOCAL_ADMIN_PASSWORD and SCANOPSIS_LOCAL_ADMIN_PASSWORD_FILE are both set"}},
		{"secret file missing", with(minimal(), "SCANOPSIS_LOCAL_ADMIN_PASSWORD_FILE", "/missing"), nil,
			[]string{"SCANOPSIS_LOCAL_ADMIN_PASSWORD_FILE cannot be read"}},
		{"short password", with(minimal(), "SCANOPSIS_LOCAL_ADMIN_PASSWORD", "short"), nil,
			[]string{"SCANOPSIS_LOCAL_ADMIN_PASSWORD must be at least 12 characters"}},
		{"bad bool", with(minimal(), "SCANOPSIS_LOCAL_LOGIN_ENABLED", "maybe"), nil,
			[]string{"SCANOPSIS_LOCAL_LOGIN_ENABLED must be true or false"}},
		{"bad duration", with(minimal(), "SCANOPSIS_SESSION_IDLE_TIMEOUT", "soon"), nil,
			[]string{"SCANOPSIS_SESSION_IDLE_TIMEOUT must be a duration"}},
		{"idle too short", with(minimal(), "SCANOPSIS_SESSION_IDLE_TIMEOUT", "1m"), nil,
			[]string{"SCANOPSIS_SESSION_IDLE_TIMEOUT must be at least 5m"}},
		{"absolute shorter than idle", with(minimal(), "SCANOPSIS_SESSION_IDLE_TIMEOUT", "2h", "SCANOPSIS_SESSION_ABSOLUTE_TIMEOUT", "1h"), nil,
			[]string{"SCANOPSIS_SESSION_ABSOLUTE_TIMEOUT must not be shorter"}},
		{"bad log level", with(minimal(), "SCANOPSIS_LOG_LEVEL", "loud"), nil,
			[]string{"SCANOPSIS_LOG_LEVEL must be debug, info, warn or error"}},
		{"bad proxy", with(minimal(), "SCANOPSIS_TRUSTED_PROXIES", "10.0.0.0/8,proxy.local"), nil,
			[]string{`SCANOPSIS_TRUSTED_PROXIES contains "proxy.local"`}},
		{"gitlab incomplete", with(minimal(), "SCANOPSIS_GITLAB_URL", "https://gitlab.example.com"), nil,
			[]string{"SCANOPSIS_GITLAB_CLIENT_ID is required", "SCANOPSIS_GITLAB_CLIENT_SECRET is required"}},
		{"gitlab settings without url", with(minimal(), "SCANOPSIS_GITLAB_CLIENT_ID", "x"), nil,
			[]string{"SCANOPSIS_GITLAB_URL is required when other GitLab settings are set"}},
		{"no sign-in method", with(minimal(), "SCANOPSIS_LOCAL_LOGIN_ENABLED", "false"), nil,
			[]string{"no sign-in method is enabled"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := load(t, tt.env, tt.files)
			if err == nil {
				t.Fatalf("expected an error, got config %+v", c)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
		})
	}
}

func TestLogValueOmitsSecrets(t *testing.T) {
	env := with(minimal(),
		"SCANOPSIS_DATABASE_URL", "postgres://user:dbpassword@db/x",
		"SCANOPSIS_LOCAL_ADMIN_PASSWORD", "adminpassword123",
		"SCANOPSIS_GITLAB_URL", "https://gitlab.example.com",
		"SCANOPSIS_GITLAB_CLIENT_ID", "id",
		"SCANOPSIS_GITLAB_CLIENT_SECRET", "gitlabsecret",
	)
	c, err := load(t, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	slog.New(slog.NewTextHandler(&sb, nil)).Info("config", "config", c)
	for _, secret := range []string{"dbpassword", "adminpassword123", "gitlabsecret"} {
		if strings.Contains(sb.String(), secret) {
			t.Errorf("log output contains secret %q: %s", secret, sb.String())
		}
	}
	if !strings.Contains(sb.String(), "gitlab_login=true") {
		t.Errorf("log output lacks summary: %s", sb.String())
	}
}

func TestLoadReportsAllErrors(t *testing.T) {
	_, err := load(t, map[string]string{"SCANOPSIS_LOG_LEVEL": "loud"}, nil)
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) || len(joined.Unwrap()) < 3 {
		t.Fatalf("expected at least 3 joined errors, got %v", err)
	}
}
