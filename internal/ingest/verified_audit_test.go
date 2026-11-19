// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"strings"
	"testing"
)

// A rejection of a request with a valid ID token is not free to cause, so the
// per-address bound does not apply to it.
func TestIngestVerifiedRejectionsAreAlwaysAudited(t *testing.T) {
	e := newHEnv(t, nil)
	const n = 3 * DefaultAuditBurst
	for range n {
		if w := e.do(req{body: []byte(`{}`), query: "?kind=sbom"}); w.Code != 400 || errCode(t, w) != "invalid_parameter" {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	if got := len(e.audit.list()); got != n {
		t.Fatalf("%d audit rows for %d rejections with a valid token", got, n)
	}
	if got := strings.Count(e.logs.String(), "level=WARN"); got != n {
		t.Errorf("%d warnings, want %d", got, n)
	}
	// Rejections without a token from the same address are still bounded.
	for range 50 {
		e.do(req{noAuth: true})
	}
	if got := len(e.audit.list()); got != n+DefaultAuditBurst {
		t.Errorf("%d rows after bad-token requests, want %d", got, n+DefaultAuditBurst)
	}
}
