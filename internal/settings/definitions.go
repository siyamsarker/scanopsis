// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Package settings holds the operational settings that administrators edit
// on the Settings page. Environment values seed the database on first start;
// afterwards the database is the source of truth. Reads are served from an
// immutable in-memory snapshot, so they cost no database query.
package settings

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/siyamsarker/scanopsis/internal/config"
)

// Type is the value type of a setting.
type Type string

// Setting value types.
const (
	TypeString     Type = "string"
	TypeURL        Type = "url"
	TypeBoolean    Type = "boolean"
	TypeDuration   Type = "duration"
	TypeStringList Type = "string_list"
	TypeInteger    Type = "integer"
)

// Setting keys.
const (
	KeyGitLabURL           = "gitlab.url"
	KeyGitLabClientID      = "gitlab.client_id"
	KeyGitLabClientSecret  = "gitlab.client_secret" //nolint:gosec // a setting key, not a credential
	KeyGitLabAllowedGroups = "gitlab.allowed_groups"
	KeyLocalLoginEnabled   = "auth.local_login_enabled"
	KeySessionIdleTimeout  = "session.idle_timeout"
	KeySessionAbsTimeout   = "session.absolute_timeout"

	KeySonarURL                = "sonar.url"
	KeySonarToken              = "sonar.token"          //nolint:gosec // a setting key, not a credential
	KeySonarWebhookSecret      = "sonar.webhook_secret" //nolint:gosec // a setting key, not a credential
	KeySonarSyncInterval       = "sonar.sync_interval"
	KeyIngestAllowedNamespaces = "ingest.allowed_namespaces"
	KeyIngestProtectedRefsOnly = "ingest.protected_refs_only"
	KeyRetentionDays           = "retention.days"
)

// MinSonarSyncInterval is the shortest allowed SonarQube reconciliation interval.
const MinSonarSyncInterval = time.Minute

// Bounds of retention.days, in days.
const (
	DefaultRetentionDays = 90
	MinRetentionDays     = 7
	MaxRetentionDays     = 3650
)

// MinIdleTimeout is the shortest allowed idle session timeout.
const MinIdleTimeout = 5 * time.Minute

// Definition describes one setting.
type Definition struct {
	Key         string
	EnvVar      string
	Description string
	Type        Type
	// Secret values are encrypted at rest and never returned by the API.
	Secret  bool
	Default any
	// Min and Max bound an integer setting, inclusive.
	Min, Max int
}

// Definitions lists every setting in display order.
var Definitions = []Definition{
	{Key: KeyGitLabURL, EnvVar: "SCANOPSIS_GITLAB_URL", Type: TypeURL, Default: "",
		Description: "Base URL of the GitLab instance, for example https://gitlab.example.com. Needed to verify CI job tokens and for GitLab sign-in."},
	{Key: KeyGitLabClientID, EnvVar: "SCANOPSIS_GITLAB_CLIENT_ID", Type: TypeString, Default: "",
		Description: "Application ID of the GitLab OAuth application."},
	{Key: KeyGitLabClientSecret, EnvVar: "SCANOPSIS_GITLAB_CLIENT_SECRET", Type: TypeString, Secret: true, Default: "",
		Description: "Secret of the GitLab OAuth application, stored encrypted."},
	{Key: KeyGitLabAllowedGroups, EnvVar: "SCANOPSIS_GITLAB_ALLOWED_GROUPS", Type: TypeStringList, Default: []string{},
		Description: "GitLab groups, by full path, whose members may sign in; leave empty to allow every GitLab user."},
	{Key: KeyLocalLoginEnabled, EnvVar: "SCANOPSIS_LOCAL_LOGIN_ENABLED", Type: TypeBoolean, Default: true,
		Description: "Whether users can sign in with a local username and password."},
	{Key: KeySessionIdleTimeout, EnvVar: "SCANOPSIS_SESSION_IDLE_TIMEOUT", Type: TypeDuration, Default: "8h",
		Description: "How long a session may stay unused before the user must sign in again, at least 5m, for example 8h."},
	{Key: KeySessionAbsTimeout, EnvVar: "SCANOPSIS_SESSION_ABSOLUTE_TIMEOUT", Type: TypeDuration, Default: "24h",
		Description: "Maximum lifetime of a session regardless of activity, not shorter than the idle timeout, for example 24h."},
	{Key: KeySonarURL, EnvVar: "SCANOPSIS_SONAR_URL", Type: TypeURL, Default: "",
		Description: "Base URL of the SonarQube server, for example https://sonarqube.example.com."},
	{Key: KeySonarToken, EnvVar: "SCANOPSIS_SONAR_TOKEN", Type: TypeString, Secret: true, Default: "",
		Description: "Token of a SonarQube user with Browse permission on the projects, stored encrypted."},
	{Key: KeySonarWebhookSecret, EnvVar: "SCANOPSIS_SONAR_WEBHOOK_SECRET", Type: TypeString, Secret: true, Default: "",
		Description: "Secret of the SonarQube webhook; deliveries without a valid signature are rejected. Stored encrypted."},
	{Key: KeySonarSyncInterval, EnvVar: "SCANOPSIS_SONAR_SYNC_INTERVAL", Type: TypeDuration, Default: "15m",
		Description: "How often linked SonarQube projects are refreshed in case a webhook was missed, at least 1m."},
	{Key: KeyIngestAllowedNamespaces, EnvVar: "SCANOPSIS_INGEST_ALLOWED_NAMESPACES", Type: TypeStringList, Default: []string{},
		Description: "GitLab groups, by full path, whose projects may upload scan reports; leave empty to accept every project."},
	{Key: KeyIngestProtectedRefsOnly, EnvVar: "SCANOPSIS_INGEST_PROTECTED_REFS_ONLY", Type: TypeBoolean, Default: false,
		Description: "Accept scan reports only from pipelines on protected branches and tags."},
	{Key: KeyRetentionDays, EnvVar: "SCANOPSIS_RETENTION_DAYS", Type: TypeInteger, Default: DefaultRetentionDays, Min: MinRetentionDays, Max: MaxRetentionDays,
		Description: "How many days scan history is kept, from 7 to 3650. The latest scans of the branch a dashboard shows and of protected branches and tags are always kept, however old."},
}

var definitionByKey = func() map[string]Definition {
	m := make(map[string]Definition, len(Definitions))
	for _, d := range Definitions {
		m[d.Key] = d
	}
	return m
}()

// problem is a validation message shown to administrators.
type problem string

func (p problem) Error() string { return string(p) }

// coerce converts a decoded JSON value (or a typed seed value) to the
// canonical value of the definition: string, bool, []string, and for
// durations a canonical string. Errors are problems.
func coerce(d Definition, raw any) (any, error) {
	switch d.Type {
	case TypeBoolean:
		b, ok := raw.(bool)
		if !ok {
			return nil, problem("Must be true or false.")
		}
		return b, nil
	case TypeStringList:
		return coerceList(d, raw)
	case TypeInteger:
		return coerceInteger(d, raw)
	case TypeDuration:
		if v, ok := raw.(time.Duration); ok {
			return formatDuration(v), nil
		}
	}
	s, ok := raw.(string)
	if !ok {
		return nil, problem("Must be a string.")
	}
	switch d.Type {
	case TypeURL:
		if strings.TrimSpace(s) == "" {
			return "", nil
		}
		u, err := config.ParseBaseURL(s)
		if err != nil {
			msg := err.Error()
			return nil, problem(strings.ToUpper(msg[:1]) + msg[1:] + ".")
		}
		return u.String(), nil
	case TypeDuration:
		v, err := time.ParseDuration(strings.TrimSpace(s))
		if err != nil {
			return nil, problem("Must be a duration such as 30m or 8h.")
		}
		return formatDuration(v), nil
	}
	if d.Secret {
		if s == "" {
			return nil, problem("Must not be empty; send null to reset.")
		}
		return s, nil
	}
	return strings.TrimSpace(s), nil
}

// coerceInteger accepts a JSON number (float64) or a Go integer with no
// fractional part, within the definition's range, and returns an int.
func coerceInteger(d Definition, raw any) (any, error) {
	bad := problem(fmt.Sprintf("Must be a whole number from %d to %d.", d.Min, d.Max))
	var n int
	switch v := raw.(type) {
	case int:
		n = v
	case int64:
		if v < math.MinInt32 || v > math.MaxInt32 {
			return nil, bad
		}
		n = int(v)
	case float64:
		if v != math.Trunc(v) || v < float64(d.Min) || v > float64(d.Max) {
			return nil, bad
		}
		n = int(v)
	default:
		return nil, bad
	}
	if n < d.Min || n > d.Max {
		return nil, bad
	}
	return n, nil
}

func coerceList(d Definition, raw any) (any, error) {
	var items []string
	switch v := raw.(type) {
	case []string:
		items = v
	case []any:
		for _, it := range v {
			s, ok := it.(string)
			if !ok {
				return nil, problem("Must be a list of strings.")
			}
			items = append(items, s)
		}
	default:
		return nil, problem("Must be a list of strings.")
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		it = strings.TrimSpace(it)
		if d.Key == KeyGitLabAllowedGroups || d.Key == KeyIngestAllowedNamespaces {
			if err := checkGroup(it); err != nil {
				return nil, err
			}
		}
		out = append(out, it)
	}
	return out, nil
}

func checkGroup(g string) error {
	switch {
	case g == "":
		return problem("Group paths must not be empty.")
	case strings.HasPrefix(g, "/") || strings.HasSuffix(g, "/"):
		return problem(fmt.Sprintf("Group path %q must not start or end with a slash.", g))
	case strings.ContainsFunc(g, unicode.IsSpace):
		return problem(fmt.Sprintf("Group path %q must not contain spaces.", g))
	}
	return nil
}

// formatDuration renders d without redundant zero units: 8h, 30m, 1h30m.
func formatDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}
