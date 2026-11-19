// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Package config reads and validates the server configuration from
// environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/siyamsarker/scanopsis/internal/secretbox"
)

// Prefix is the prefix of every environment variable read by Scanopsis.
const Prefix = "SCANOPSIS_"

// Config is the validated server configuration.
type Config struct {
	ListenAddr string
	// PublicURL is the address users and pipelines use to reach Scanopsis,
	// for example https://scanopsis.example.com. It has no path.
	PublicURL      *url.URL
	DatabaseURL    string
	LogLevel       slog.Level
	TrustedProxies []netip.Prefix
	// CAFile is an optional PEM bundle trusted in addition to the system
	// roots for outgoing TLS connections (GitLab, SonarQube).
	CAFile string
	// SecretKey is the 32-byte key that encrypts secrets stored in the
	// database (see package secretbox).
	SecretKey []byte

	// Seeds holds the operational settings given through environment
	// variables, keyed by setting key (for example "gitlab.url"). Only
	// variables that are set appear. Values are string (string, url and
	// secret settings), bool, []string, time.Duration or int. The settings
	// package stores them on first start; afterwards the database wins.
	Seeds map[string]any
}

// Secure reports whether Scanopsis is served over HTTPS, which decides the
// Secure cookie attribute and HSTS.
func (c *Config) Secure() bool { return c.PublicURL.Scheme == "https" }

// LogValue describes the configuration for logs without any secret values.
func (c *Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("listen_addr", c.ListenAddr),
		slog.String("public_url", c.PublicURL.String()),
		slog.Int("seeded_settings", len(c.Seeds)),
		slog.Int("trusted_proxies", len(c.TrustedProxies)),
		slog.Bool("custom_ca", c.CAFile != ""),
	)
}

// Load reads the configuration. getenv and readFile are normally os.Getenv
// and os.ReadFile; tests pass fakes. All problems are reported together.
func Load(getenv func(string) string, readFile func(string) ([]byte, error)) (*Config, error) {
	l := loader{getenv: getenv, readFile: readFile}
	c := &Config{
		ListenAddr:  l.str("LISTEN_ADDR", ":8080"),
		DatabaseURL: l.secret("DATABASE_URL"),
		CAFile:      l.str("CA_FILE", ""),
	}
	c.PublicURL = l.publicURL("PUBLIC_URL")
	c.LogLevel = l.logLevel("LOG_LEVEL")
	c.TrustedProxies = l.prefixes("TRUSTED_PROXIES")
	c.SecretKey = l.secretKey("SECRET_KEY")
	c.Seeds = map[string]any{}
	l.seeds(c.Seeds)

	// A problem with the variable or its file is already reported.
	if c.DatabaseURL == "" && !l.hasErr("DATABASE_URL") {
		l.fail("DATABASE_URL", "is required")
	}
	if err := errors.Join(l.errs...); err != nil {
		return nil, err
	}
	return c, nil
}

// ParseBaseURL checks an absolute http(s) URL without path, query, fragment
// or credentials and returns it without a trailing slash. The Settings page
// applies the same rules as the environment.
func ParseBaseURL(v string) (*url.URL, error) {
	v = strings.TrimSuffix(strings.TrimSpace(v), "/")
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("must be an absolute http or https URL, got %q", v)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("must not contain a path, query, fragment or credentials, got %q", v)
	}
	return u, nil
}

type loader struct {
	getenv   func(string) string
	readFile func(string) ([]byte, error)
	errs     []error
}

func (l *loader) fail(name, msg string) {
	l.errs = append(l.errs, fmt.Errorf("%s%s %s", Prefix, name, msg))
}

func (l *loader) str(name, def string) string {
	if v := strings.TrimSpace(l.getenv(Prefix + name)); v != "" {
		return v
	}
	return def
}

// secret reads NAME or NAME_FILE (a path whose content is the value, for
// Docker secrets). Setting both is an error.
func (l *loader) secret(name string) string {
	v := l.getenv(Prefix + name)
	path := strings.TrimSpace(l.getenv(Prefix + name + "_FILE"))
	switch {
	case v != "" && path != "":
		l.fail(name, "and "+Prefix+name+"_FILE are both set; set only one")
		return ""
	case path != "":
		b, err := l.readFile(path)
		if err != nil {
			l.fail(name+"_FILE", fmt.Sprintf("cannot be read: %v", err))
			return ""
		}
		return strings.TrimRight(string(b), "\r\n")
	default:
		return v
	}
}

func (l *loader) list(name string) []string {
	var out []string
	for _, item := range strings.Split(l.getenv(Prefix+name), ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func (l *loader) boolean(name string, def bool) bool {
	v := l.str(name, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.fail(name, fmt.Sprintf("must be true or false, got %q", v))
		return def
	}
	return b
}

func (l *loader) duration(name string, def time.Duration) time.Duration {
	v := l.str(name, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		l.fail(name, fmt.Sprintf("must be a duration such as 30m or 8h, got %q", v))
		return def
	}
	return d
}

// integer parses an optional whole number in [lo, hi]; ok is false when the
// variable is unset or invalid (invalid values are reported).
func (l *loader) integer(name string, lo, hi int) (n int, ok bool) {
	v := l.str(name, "")
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		l.fail(name, fmt.Sprintf("must be a whole number from %d to %d, got %q", lo, hi, v))
		return 0, false
	}
	return n, true
}

func (l *loader) logLevel(name string) slog.Level {
	var lvl slog.Level
	v := l.str(name, "info")
	if err := lvl.UnmarshalText([]byte(v)); err != nil {
		l.fail(name, fmt.Sprintf("must be debug, info, warn or error, got %q", v))
		return slog.LevelInfo
	}
	return lvl
}

func (l *loader) prefixes(name string) []netip.Prefix {
	var out []netip.Prefix
	for _, item := range l.list(name) {
		if !strings.Contains(item, "/") {
			addr, err := netip.ParseAddr(item)
			if err != nil {
				l.fail(name, fmt.Sprintf("contains %q, which is not an IP address or CIDR range", item))
				continue
			}
			out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(item)
		if err != nil {
			l.fail(name, fmt.Sprintf("contains %q, which is not an IP address or CIDR range", item))
			continue
		}
		out = append(out, p.Masked())
	}
	return out
}

// baseURL parses the variable name as a base URL; nil when unset or invalid
// (invalid values are reported).
func (l *loader) baseURL(name string) *url.URL {
	v := l.str(name, "")
	if v == "" {
		return nil
	}
	u, err := ParseBaseURL(v)
	if err != nil {
		l.fail(name, err.Error())
		return nil
	}
	return u
}

func (l *loader) publicURL(name string) *url.URL {
	if u := l.baseURL(name); u != nil {
		return u
	}
	if l.str(name, "") == "" {
		l.fail(name, "is required, for example https://scanopsis.example.com")
	}
	return &url.URL{Scheme: "http", Host: "invalid"}
}

// secretKey reads the encryption key from NAME or NAME_FILE.
func (l *loader) secretKey(name string) []byte {
	v := l.secret(name)
	if v == "" {
		if len(l.errs) == 0 || !l.hasErr(name) {
			l.fail(name, "is required; generate one with: openssl rand -base64 32")
		}
		return nil
	}
	k, err := secretbox.ParseKey(v)
	if err != nil {
		l.fail(name, fmt.Sprintf("is not valid (%v); generate one with: openssl rand -base64 32", err))
		return nil
	}
	return k
}

func (l *loader) hasErr(name string) bool {
	for _, e := range l.errs {
		if strings.HasPrefix(e.Error(), Prefix+name) {
			return true
		}
	}
	return false
}

// seeds reads the operational variables that seed the settings table.
func (l *loader) seeds(m map[string]any) {
	if u := l.baseURL("GITLAB_URL"); u != nil {
		m["gitlab.url"] = u.String()
	}
	if v := l.str("GITLAB_CLIENT_ID", ""); v != "" {
		m["gitlab.client_id"] = v
	}
	if v := l.secret("GITLAB_CLIENT_SECRET"); v != "" {
		m["gitlab.client_secret"] = v
	}
	if g := l.list("GITLAB_ALLOWED_GROUPS"); len(g) > 0 {
		m["gitlab.allowed_groups"] = g
	}
	if l.str("LOCAL_LOGIN_ENABLED", "") != "" {
		m["auth.local_login_enabled"] = l.boolean("LOCAL_LOGIN_ENABLED", true)
	}
	if l.str("SESSION_IDLE_TIMEOUT", "") != "" {
		m["session.idle_timeout"] = l.duration("SESSION_IDLE_TIMEOUT", 8*time.Hour)
	}
	if l.str("SESSION_ABSOLUTE_TIMEOUT", "") != "" {
		m["session.absolute_timeout"] = l.duration("SESSION_ABSOLUTE_TIMEOUT", 24*time.Hour)
	}

	if u := l.baseURL("SONAR_URL"); u != nil {
		m["sonar.url"] = u.String()
	}
	if v := l.secret("SONAR_TOKEN"); v != "" {
		m["sonar.token"] = v
	}
	if v := l.secret("SONAR_WEBHOOK_SECRET"); v != "" {
		m["sonar.webhook_secret"] = v
	}
	if l.str("SONAR_SYNC_INTERVAL", "") != "" {
		m["sonar.sync_interval"] = l.duration("SONAR_SYNC_INTERVAL", 15*time.Minute)
	}
	if g := l.list("INGEST_ALLOWED_NAMESPACES"); len(g) > 0 {
		m["ingest.allowed_namespaces"] = g
	}
	if l.str("INGEST_PROTECTED_REFS_ONLY", "") != "" {
		m["ingest.protected_refs_only"] = l.boolean("INGEST_PROTECTED_REFS_ONLY", false)
	}
	if n, ok := l.integer("RETENTION_DAYS", 7, 3650); ok {
		m["retention.days"] = n
	}

	// The GitLab URL alone is enough to verify CI job tokens; GitLab sign-in
	// additionally needs the client ID and secret, both or neither.
	_, hasURL := m["gitlab.url"]
	_, hasID := m["gitlab.client_id"]
	_, hasSecret := m["gitlab.client_secret"]
	if !hasURL && l.str("GITLAB_URL", "") == "" && (hasID || hasSecret || m["gitlab.allowed_groups"] != nil) {
		l.fail("GITLAB_URL", "is required when other GitLab settings are set")
	}
	if hasID != hasSecret {
		if !hasID {
			l.fail("GITLAB_CLIENT_ID", "is required when "+Prefix+"GITLAB_CLIENT_SECRET is set")
		} else {
			l.fail("GITLAB_CLIENT_SECRET", "is required when "+Prefix+"GITLAB_CLIENT_ID is set")
		}
	}
	// SonarQube: the token is needed to read results once a URL is set.
	_, hasSonarURL := m["sonar.url"]
	_, hasSonarToken := m["sonar.token"]
	if hasSonarURL != hasSonarToken && l.str("SONAR_URL", "") != "" || (!hasSonarURL && l.str("SONAR_URL", "") == "" && (hasSonarToken || m["sonar.webhook_secret"] != nil)) {
		l.fail("SONAR_URL", "and "+Prefix+"SONAR_TOKEN must be set together (the webhook secret needs both)")
	}

	idle, abs := 8*time.Hour, 24*time.Hour
	if d, ok := m["session.idle_timeout"].(time.Duration); ok {
		idle = d
	}
	if d, ok := m["session.absolute_timeout"].(time.Duration); ok {
		abs = d
	}
	if idle < 5*time.Minute {
		l.fail("SESSION_IDLE_TIMEOUT", "must be at least 5m")
	}
	if abs < idle {
		l.fail("SESSION_ABSOLUTE_TIMEOUT", "must not be shorter than "+Prefix+"SESSION_IDLE_TIMEOUT")
	}
}
