// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Package settings holds the operational settings that administrators edit
// on the Settings page. Environment values seed the database on first start;
// afterwards the database is the source of truth. Reads are served from an
// immutable in-memory snapshot, so they cost no database query.
package settings

import (
	"fmt"
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
}

// Definitions lists every setting in display order.
var Definitions = []Definition{
	{Key: KeyGitLabURL, EnvVar: "SCANOPSIS_GITLAB_URL", Type: TypeURL, Default: "",
		Description: "Base URL of the GitLab instance used for sign-in, for example https://gitlab.example.com."},
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
		if d.Key == KeyGitLabAllowedGroups {
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
