// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package ingest

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/auth"
)

// One job may store MaxArtifactsPerJob different artifacts. The next is
// answered 429, stores nothing and is audited. Replacing an artifact the job
// has is still allowed, a different kind of the same name counts as another
// artifact, and another job has its own count.
func TestIntegrationJobArtifactLimit(t *testing.T) {
	e := newIEnv(t, func(o *Options) { o.ProjectLimiter = auth.NewRateLimiter(time.Second, 1000, nil) })
	body := func(i int) []byte {
		return fmt.Appendf(nil, `{"SchemaVersion":2,"ArtifactName":"registry.example.com/app%d:1"}`, i)
	}

	for i := range MaxArtifactsPerJob {
		if code, resp := e.upload(upload{body: body(i)}); code != http.StatusCreated {
			t.Fatalf("artifact %d: %d %s", i, code, resp)
		}
	}
	code, resp := e.upload(upload{body: body(MaxArtifactsPerJob)})
	if code != http.StatusTooManyRequests {
		t.Fatalf("one over: %d %s", code, resp)
	}
	if n := e.count("scans"); n != MaxArtifactsPerJob {
		t.Errorf("%d scans stored", n)
	}

	// The answer names the code and tells the client not to wait.
	hr := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/ingest/trivy", bytes.NewReader(body(MaxArtifactsPerJob)))
	hr.Header.Set("Authorization", "Bearer "+e.g.token(t, nil))
	hr.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.in.Handlers()["ingestTrivy"].ServeHTTP(w, hr)
	if w.Code != http.StatusTooManyRequests || errCode(t, w) != "job_artifact_limit" || w.Header().Get("Retry-After") != "3600" {
		t.Errorf("%d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body)
	}

	// Replacing an artifact the job already has does not count.
	if code, resp := e.upload(upload{body: body(3)}); code != http.StatusCreated {
		t.Errorf("replacement: %d %s", code, resp)
	}
	// The same artifact name with the other kind is a different artifact.
	if code, _ := e.upload(upload{body: body(3), query: "?kind=secret-fs"}); code != http.StatusTooManyRequests {
		t.Errorf("other kind over the limit: %d", code)
	}
	// Another job of the project has its own count.
	if code, resp := e.upload(upload{body: body(MaxArtifactsPerJob), claims: func(c map[string]any) { c["job_id"] = "9999" }}); code != http.StatusCreated {
		t.Errorf("another job: %d %s", code, resp)
	}

	var reason, actor string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT detail->>'reason', actor FROM audit_log WHERE action='ingest.rejected' ORDER BY id LIMIT 1`).Scan(&reason, &actor); err != nil {
		t.Fatal(err)
	}
	if reason != "job_artifact_limit" || actor != "ci:platform/payments-api" {
		t.Errorf("audit: %s %s", reason, actor)
	}
}
