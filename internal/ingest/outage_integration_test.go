// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package ingest

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/settings"
)

// A database that cannot be reached and a pool that was closed are real
// errors from the driver: both are answered 503 unavailable.
func TestIntegrationDatabaseOutageIs503(t *testing.T) {
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	post := func(t *testing.T, in *Ingest, g *gitlab) *httptest.ResponseRecorder {
		t.Helper()
		hr := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/ingest/trivy", bytes.NewReader(body))
		hr.Header.Set("Authorization", "Bearer "+g.token(t, nil))
		hr.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		in.Handlers()["ingestTrivy"].ServeHTTP(w, hr)
		return w
	}
	newIngest := func(t *testing.T, pool *pgxpool.Pool) (*Ingest, *gitlab) {
		g := newGitLab(t)
		in := New(pool, &fakeSettings{snap: settings.Snapshot{GitLabURL: g.url}}, &recorder{}, Options{
			Audience: testAudience, Logger: slog.New(slog.DiscardHandler),
			ClientIP: func(*http.Request) netip.Addr { return netip.MustParseAddr("192.0.2.10") },
		})
		return in, g
	}

	t.Run("unreachable", func(t *testing.T) {
		pool, err := pgxpool.New(context.Background(), "postgres://u:p@127.0.0.1:1/db?connect_timeout=2")
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		in, g := newIngest(t, pool)
		w := post(t, in, g)
		if w.Code != http.StatusServiceUnavailable || errCode(t, w) != "unavailable" || w.Header().Get("Retry-After") != "30" {
			t.Fatalf("%d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body)
		}
	})

	t.Run("closed pool", func(t *testing.T) {
		e := newIEnv(t, nil)
		e.pool.Close()
		w := httptest.NewRecorder()
		hr := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/ingest/trivy", bytes.NewReader(body))
		hr.Header.Set("Authorization", "Bearer "+e.g.token(t, nil))
		hr.Header.Set("Content-Type", "application/json")
		e.in.Handlers()["ingestTrivy"].ServeHTTP(w, hr)
		if w.Code != http.StatusServiceUnavailable || errCode(t, w) != "unavailable" {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	})
}
