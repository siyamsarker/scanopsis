// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/netip"
	"reflect"
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

const testKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

func minimal() map[string]string {
	return map[string]string{
		"SCANOPSIS_PUBLIC_URL":   "https://scanopsis.example.com",
		"SCANOPSIS_DATABASE_URL": "postgres://u:p@db/scanopsis",
		"SCANOPSIS_SECRET_KEY":   testKey,
	}
}

func without(base map[string]string, keys ...string) map[string]string {
	out := with(base)
	for _, k := range keys {
		delete(out, k)
	}
	return out
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
	if len(c.Seeds) != 0 {
		t.Errorf("Seeds = %v, want empty", c.Seeds)
	}
	if len(c.SecretKey) != 32 || c.SecretKey[1] != 1 {
		t.Errorf("SecretKey = %v", c.SecretKey)
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
		"SCANOPSIS_LOCAL_LOGIN_ENABLED", "false",
		"SCANOPSIS_RETENTION_DAYS", " 180 ",
	)
	files := map[string]string{"/run/secrets/gitlab": "s3cret\n"}
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
	want := map[string]any{
		"gitlab.url":               "https://gitlab.example.com",
		"gitlab.client_id":         "client",
		"gitlab.client_secret":     "s3cret",
		"gitlab.allowed_groups":    []string{"platform", "security/appsec"},
		"auth.local_login_enabled": false,
		"session.idle_timeout":     30 * time.Minute,
		"session.absolute_timeout": 12 * time.Hour,
		"retention.days":           180,
	}
	if !reflect.DeepEqual(c.Seeds, want) {
		t.Errorf("Seeds = %#v, want %#v", c.Seeds, want)
	}
}

func TestSecretKeyFile(t *testing.T) {
	env := with(minimal(), "SCANOPSIS_SECRET_KEY", "", "SCANOPSIS_SECRET_KEY_FILE", "/k")
	c, err := load(t, env, map[string]string{"/k": testKey + "\n"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.SecretKey) != 32 {
		t.Errorf("SecretKey length = %d", len(c.SecretKey))
	}
}

func TestParseBaseURL(t *testing.T) {
	u, err := ParseBaseURL(" https://gitlab.example.com/ ")
	if err != nil || u.String() != "https://gitlab.example.com" {
		t.Errorf("got %v, %v", u, err)
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
		{"secret key missing", without(minimal(), "SCANOPSIS_SECRET_KEY"), nil,
			[]string{"SCANOPSIS_SECRET_KEY is required; generate one with: openssl rand -base64 32"}},
		{"secret key short", with(minimal(), "SCANOPSIS_SECRET_KEY", "AAEC"), nil,
			[]string{"SCANOPSIS_SECRET_KEY is not valid (key must be 32 bytes, got 3)", "openssl rand -base64 32"}},
		{"secret key not base64", with(minimal(), "SCANOPSIS_SECRET_KEY", "!!!"), nil,
			[]string{"SCANOPSIS_SECRET_KEY is not valid (key is not valid base64)"}},
		{"secret key file missing", with(without(minimal(), "SCANOPSIS_SECRET_KEY"), "SCANOPSIS_SECRET_KEY_FILE", "/missing"), nil,
			[]string{"SCANOPSIS_SECRET_KEY_FILE cannot be read"}},
		{"secret and file both set", with(minimal(), "SCANOPSIS_SECRET_KEY_FILE", "/f"), nil,
			[]string{"SCANOPSIS_SECRET_KEY and SCANOPSIS_SECRET_KEY_FILE are both set"}},
		{"database url and file both set", with(minimal(), "SCANOPSIS_DATABASE_URL_FILE", "/f"), nil,
			[]string{"SCANOPSIS_DATABASE_URL and SCANOPSIS_DATABASE_URL_FILE are both set"}},
		{"database url file missing", with(without(minimal(), "SCANOPSIS_DATABASE_URL"), "SCANOPSIS_DATABASE_URL_FILE", "/missing"), nil,
			[]string{"SCANOPSIS_DATABASE_URL_FILE cannot be read"}},
		{"gitlab secret file missing", with(minimal(), "SCANOPSIS_GITLAB_CLIENT_SECRET_FILE", "/missing"), nil,
			[]string{"SCANOPSIS_GITLAB_CLIENT_SECRET_FILE cannot be read"}},
		{"bad bool", with(minimal(), "SCANOPSIS_LOCAL_LOGIN_ENABLED", "maybe"), nil,
			[]string{"SCANOPSIS_LOCAL_LOGIN_ENABLED must be true or false"}},
		{"bad duration", with(minimal(), "SCANOPSIS_SESSION_IDLE_TIMEOUT", "soon"), nil,
			[]string{"SCANOPSIS_SESSION_IDLE_TIMEOUT must be a duration"}},
		{"idle too short", with(minimal(), "SCANOPSIS_SESSION_IDLE_TIMEOUT", "1m"), nil,
			[]string{"SCANOPSIS_SESSION_IDLE_TIMEOUT must be at least 5m"}},
		{"absolute shorter than idle", with(minimal(), "SCANOPSIS_SESSION_IDLE_TIMEOUT", "2h", "SCANOPSIS_SESSION_ABSOLUTE_TIMEOUT", "1h"), nil,
			[]string{"SCANOPSIS_SESSION_ABSOLUTE_TIMEOUT must not be shorter"}},
		{"sync interval too short", with(minimal(), "SCANOPSIS_SONAR_SYNC_INTERVAL", "30s"), nil,
			[]string{"SCANOPSIS_SONAR_SYNC_INTERVAL must be at least 1m"}},
		{"sync interval zero", with(minimal(), "SCANOPSIS_SONAR_SYNC_INTERVAL", "0s"), nil,
			[]string{"SCANOPSIS_SONAR_SYNC_INTERVAL must be at least 1m"}},
		{"sync interval negative", with(minimal(), "SCANOPSIS_SONAR_SYNC_INTERVAL", "-5m"), nil,
			[]string{"SCANOPSIS_SONAR_SYNC_INTERVAL must be at least 1m"}},
		{"retention days not a number", with(minimal(), "SCANOPSIS_RETENTION_DAYS", "ninety"), nil,
			[]string{`SCANOPSIS_RETENTION_DAYS must be a whole number from 7 to 3650, got "ninety"`}},
		{"retention days fraction", with(minimal(), "SCANOPSIS_RETENTION_DAYS", "90.5"), nil,
			[]string{"SCANOPSIS_RETENTION_DAYS must be a whole number from 7 to 3650"}},
		{"retention days too small", with(minimal(), "SCANOPSIS_RETENTION_DAYS", "6"), nil,
			[]string{"SCANOPSIS_RETENTION_DAYS must be a whole number from 7 to 3650"}},
		{"retention days too large", with(minimal(), "SCANOPSIS_RETENTION_DAYS", "3651"), nil,
			[]string{"SCANOPSIS_RETENTION_DAYS must be a whole number from 7 to 3650"}},
		{"bad log level", with(minimal(), "SCANOPSIS_LOG_LEVEL", "loud"), nil,
			[]string{"SCANOPSIS_LOG_LEVEL must be debug, info, warn or error"}},
		{"bad proxy", with(minimal(), "SCANOPSIS_TRUSTED_PROXIES", "10.0.0.0/8,proxy.local"), nil,
			[]string{`SCANOPSIS_TRUSTED_PROXIES contains "proxy.local"`}},
		{"gitlab client id without secret", with(minimal(), "SCANOPSIS_GITLAB_URL", "https://gitlab.example.com", "SCANOPSIS_GITLAB_CLIENT_ID", "x"), nil,
			[]string{"SCANOPSIS_GITLAB_CLIENT_SECRET is required when SCANOPSIS_GITLAB_CLIENT_ID is set"}},
		{"sonar url without token", with(minimal(), "SCANOPSIS_SONAR_URL", "https://sonarqube.example.com"), nil,
			[]string{"SCANOPSIS_SONAR_URL and SCANOPSIS_SONAR_TOKEN must be set together"}},
		{"sonar token without url", with(minimal(), "SCANOPSIS_SONAR_TOKEN", "x"), nil,
			[]string{"SCANOPSIS_SONAR_URL and SCANOPSIS_SONAR_TOKEN must be set together"}},
		{"gitlab settings without url", with(minimal(), "SCANOPSIS_GITLAB_CLIENT_ID", "x"), nil,
			[]string{"SCANOPSIS_GITLAB_URL is required when other GitLab settings are set"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := load(t, tt.env, tt.files)
			if err == nil {
				t.Fatalf("expected an error, got config %+v", c)
			}
			// One message per problem: a missing value is not reported
			// again when its variable or file already failed.
			if n := strings.Count(err.Error(), "SCANOPSIS_DATABASE_URL is required"); n > 0 && tt.name != "missing required" {
				t.Errorf("unexpected %q in %q", "SCANOPSIS_DATABASE_URL is required", err)
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
	for _, secret := range []string{"dbpassword", "gitlabsecret", testKey} {
		if strings.Contains(sb.String(), secret) {
			t.Errorf("log output contains secret %q: %s", secret, sb.String())
		}
	}
	if !strings.Contains(sb.String(), "seeded_settings=3") {
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

// Only the absolute timeout set: it is compared with the default idle
// timeout (30m), so 4h is accepted and 20m is not.
func TestSessionAbsoluteAloneUsesTheDefaultIdle(t *testing.T) {
	if _, err := load(t, with(minimal(), "SCANOPSIS_SESSION_ABSOLUTE_TIMEOUT", "4h"), nil); err != nil {
		t.Errorf("4h absolute with the default idle timeout: %v", err)
	}
	if _, err := load(t, with(minimal(), "SCANOPSIS_SESSION_ABSOLUTE_TIMEOUT", "20m"), nil); err == nil || !strings.Contains(err.Error(), "SESSION_ABSOLUTE_TIMEOUT must not be shorter") {
		t.Errorf("20m absolute: %v", err)
	}
}

func TestConfigBackupDir(t *testing.T) {
	c, err := load(t, minimal(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.BackupDir != "/var/lib/scanopsis/backups" {
		t.Errorf("default BackupDir = %q", c.BackupDir)
	}
	c, err = load(t, with(minimal(), "SCANOPSIS_BACKUP_DIR", " /data/backups "), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.BackupDir != "/data/backups" {
		t.Errorf("BackupDir = %q", c.BackupDir)
	}
	_, err = load(t, with(minimal(), "SCANOPSIS_BACKUP_DIR", "backups"), nil)
	if err == nil || !strings.Contains(err.Error(), "SCANOPSIS_BACKUP_DIR") || !strings.Contains(err.Error(), "absolute") {
		t.Errorf("relative path error = %v", err)
	}
}
