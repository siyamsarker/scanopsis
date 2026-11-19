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
)

// Prefix is the prefix of every environment variable read by Scanopsis.
const Prefix = "SCANOPSIS_"

// MinPasswordLength is the minimum length of the local administrator password.
const MinPasswordLength = 12

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

	SessionIdleTimeout     time.Duration
	SessionAbsoluteTimeout time.Duration

	// GitLab is nil when GitLab sign-in is not configured.
	GitLab *GitLab

	LocalLogin         bool
	LocalAdminUsername string
	// LocalAdminPassword is used only to create the local administrator on
	// a start where no administrator exists. It may be empty.
	LocalAdminPassword string
}

// GitLab holds the GitLab OpenID Connect settings.
type GitLab struct {
	// URL is the GitLab base URL without a trailing slash. It is also the
	// OpenID Connect issuer.
	URL            string
	ClientID       string
	ClientSecret   string
	AllowedGroups  []string
	AdminUsernames []string
}

// Secure reports whether Scanopsis is served over HTTPS, which decides the
// Secure cookie attribute and HSTS.
func (c *Config) Secure() bool { return c.PublicURL.Scheme == "https" }

// LogValue describes the configuration for logs without any secret values.
func (c *Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("listen_addr", c.ListenAddr),
		slog.String("public_url", c.PublicURL.String()),
		slog.Bool("gitlab_login", c.GitLab != nil),
		slog.Bool("local_login", c.LocalLogin),
		slog.Int("trusted_proxies", len(c.TrustedProxies)),
		slog.Bool("custom_ca", c.CAFile != ""),
	)
}

// Load reads the configuration. getenv and readFile are normally os.Getenv
// and os.ReadFile; tests pass fakes. All problems are reported together.
func Load(getenv func(string) string, readFile func(string) ([]byte, error)) (*Config, error) {
	l := loader{getenv: getenv, readFile: readFile}
	c := &Config{
		ListenAddr:         l.str("LISTEN_ADDR", ":8080"),
		DatabaseURL:        l.secret("DATABASE_URL"),
		CAFile:             l.str("CA_FILE", ""),
		LocalLogin:         l.boolean("LOCAL_LOGIN_ENABLED", true),
		LocalAdminUsername: l.str("LOCAL_ADMIN_USERNAME", "admin"),
		LocalAdminPassword: l.secret("LOCAL_ADMIN_PASSWORD"),
	}
	c.PublicURL = l.publicURL("PUBLIC_URL")
	c.LogLevel = l.logLevel("LOG_LEVEL")
	c.TrustedProxies = l.prefixes("TRUSTED_PROXIES")
	c.SessionIdleTimeout = l.duration("SESSION_IDLE_TIMEOUT", 8*time.Hour)
	c.SessionAbsoluteTimeout = l.duration("SESSION_ABSOLUTE_TIMEOUT", 24*time.Hour)
	c.GitLab = l.gitlab()

	if c.DatabaseURL == "" {
		l.fail("DATABASE_URL", "is required")
	}
	if c.SessionIdleTimeout < 5*time.Minute {
		l.fail("SESSION_IDLE_TIMEOUT", "must be at least 5m")
	}
	if c.SessionAbsoluteTimeout < c.SessionIdleTimeout {
		l.fail("SESSION_ABSOLUTE_TIMEOUT", "must not be shorter than "+Prefix+"SESSION_IDLE_TIMEOUT")
	}
	if c.LocalAdminUsername == "" {
		l.fail("LOCAL_ADMIN_USERNAME", "must not be empty")
	}
	if c.LocalAdminPassword != "" && len(c.LocalAdminPassword) < MinPasswordLength {
		l.fail("LOCAL_ADMIN_PASSWORD", fmt.Sprintf("must be at least %d characters", MinPasswordLength))
	}
	if !c.LocalLogin && c.GitLab == nil {
		l.errs = append(l.errs, errors.New("no sign-in method is enabled: configure GitLab sign-in or set "+Prefix+"LOCAL_LOGIN_ENABLED=true"))
	}
	if err := errors.Join(l.errs...); err != nil {
		return nil, err
	}
	return c, nil
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

// baseURL parses an absolute http(s) URL without path, query or fragment and
// returns it without a trailing slash.
func (l *loader) baseURL(name string) *url.URL {
	v := strings.TrimSuffix(l.str(name, ""), "/")
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		l.fail(name, fmt.Sprintf("must be an absolute http or https URL, got %q", v))
		return nil
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		l.fail(name, fmt.Sprintf("must not contain a path, query, fragment or credentials, got %q", v))
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

func (l *loader) gitlab() *GitLab {
	u := l.baseURL("GITLAB_URL")
	g := &GitLab{
		ClientID:       l.str("GITLAB_CLIENT_ID", ""),
		ClientSecret:   l.secret("GITLAB_CLIENT_SECRET"),
		AllowedGroups:  l.list("GITLAB_ALLOWED_GROUPS"),
		AdminUsernames: l.list("ADMIN_USERNAMES"),
	}
	if u == nil && l.str("GITLAB_URL", "") == "" {
		if g.ClientID != "" || g.ClientSecret != "" || len(g.AllowedGroups) > 0 {
			l.fail("GITLAB_URL", "is required when other GitLab settings are set")
		}
		return nil
	}
	if u == nil {
		return nil // invalid URL, already reported
	}
	g.URL = u.String()
	if g.ClientID == "" {
		l.fail("GITLAB_CLIENT_ID", "is required when "+Prefix+"GITLAB_URL is set")
	}
	if g.ClientSecret == "" {
		l.fail("GITLAB_CLIENT_SECRET", "is required when "+Prefix+"GITLAB_URL is set")
	}
	return g
}
