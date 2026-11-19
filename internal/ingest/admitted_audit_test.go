// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"strings"
	"testing"

	"github.com/siyamsarker/scanopsis/internal/settings"
)

// A project the instance accepts cannot flood the audit log with rejections
// that come after the allow-list checks: they are all recorded.
func TestIngestAdmittedRejectionsAreAlwaysAudited(t *testing.T) {
	e := newHEnv(t, nil)
	const n = 3 * DefaultAuditBurst
	for range n {
		if w := e.do(req{body: []byte(`{"SchemaVersion":`)}); w.Code != 400 || errCode(t, w) != "invalid_report" {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	if got := len(e.audit.list()); got != n {
		t.Fatalf("%d audit rows for %d rejections of an allowed project", got, n)
	}
	if got := strings.Count(e.logs.String(), "level=WARN"); got != n {
		t.Errorf("%d warnings, want %d", got, n)
	}
	// Rejections before the checks from the same address are still bounded.
	for range 50 {
		e.do(req{noAuth: true})
	}
	if got := len(e.audit.list()); got != n+DefaultAuditBurst {
		t.Errorf("%d rows after bad-token requests, want %d", got, n+DefaultAuditBurst)
	}
}

// A valid ID token is not enough to be audited without bound: tokens can be
// reused and, on a public GitLab, anyone can get one.
func TestIngestRejectionsBeforeTheAllowListAreBounded(t *testing.T) {
	for name, mutate := range map[string]func(*settings.Snapshot){
		"namespace_not_allowed": func(s *settings.Snapshot) { s.IngestAllowedNamespaces = []string{"other"} },
		"ref_not_protected":     func(s *settings.Snapshot) { s.IngestProtectedRefsOnly = true },
	} {
		t.Run(name, func(t *testing.T) {
			e := newHEnv(t, nil)
			e.cfg.set(mutate)
			tok := e.g.token(t, func(c map[string]any) { c["ref_protected"] = "false" })
			for range 100 {
				w := e.do(req{noAuth: true, body: []byte(`{}`), header: map[string]string{"Authorization": "Bearer " + tok}})
				if w.Code != 403 || errCode(t, w) != name {
					t.Fatalf("%d %s", w.Code, w.Body)
				}
			}
			if got := len(e.audit.list()); got != DefaultAuditBurst {
				t.Errorf("%d audit rows, want %d", got, DefaultAuditBurst)
			}
		})
	}
}
