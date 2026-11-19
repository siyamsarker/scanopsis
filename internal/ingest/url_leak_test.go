// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"strings"
	"testing"

	"github.com/siyamsarker/scanopsis/internal/settings"
)

func TestIngestErrorsDoNotRevealTheGitLabURL(t *testing.T) {
	// GitLab is unreachable.
	e := newHEnv(t, nil)
	e.cfg.set(func(s *settings.Snapshot) { s.GitLabURL = "http://gitlab.internal.invalid:1" })
	w := e.do(req{body: []byte(`{}`)})
	if w.Code != 503 || errCode(t, w) != "gitlab_unavailable" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, leak := range []string{"gitlab.internal.invalid", "discovery", "http://"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("body reveals %q: %s", leak, w.Body)
		}
	}
	logs := e.logs.String()
	if !strings.Contains(logs, "level=WARN") || !strings.Contains(logs, "gitlab_url=http://gitlab.internal.invalid:1") ||
		!strings.Contains(logs, "/oauth/discovery/keys") {
		t.Errorf("the log lacks the detail: %s", logs)
	}
}

func TestIngestTokenErrorsDoNotRevealTheGitLabURL(t *testing.T) {
	e := newHEnv(t, nil)
	host := strings.TrimPrefix(e.g.url, "http://")
	other := newGitLab(t)
	for name, tok := range map[string]string{
		"issuer":    e.g.token(t, func(c map[string]any) { c["iss"] = "https://gitlab.evil.example.com" }),
		"signature": other.token(t, func(c map[string]any) { c["iss"] = e.g.url }),
	} {
		w := e.do(req{noAuth: true, header: map[string]string{"Authorization": "Bearer " + tok}})
		if w.Code != 401 || errCode(t, w) != "invalid_token" || strings.Contains(w.Body.String(), host) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if !strings.Contains(e.logs.String(), "gitlab_url="+e.g.url) {
		t.Errorf("the log lacks the GitLab URL: %s", e.logs)
	}
}
