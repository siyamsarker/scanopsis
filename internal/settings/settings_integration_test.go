// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package settings

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/secretbox"
	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

func TestIntegrationSettingsLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := storetest.New(t)
	q := store.New(pool)
	tx := func(ctx context.Context, fn func(Store) error) error {
		return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return fn(q.WithTx(tx)) })
	}
	box, err := secretbox.New(bytes.Repeat([]byte{7}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	rec := &fakeAudit{}
	seeds := map[string]any{
		"gitlab.url": "https://gitlab.example.com", "gitlab.client_id": "cid", "gitlab.client_secret": "pg-secret",
		"session.idle_timeout": 30 * time.Minute,
	}
	svc, err := New(ctx, q, tx, box, seeds, rec, discard)
	if err != nil {
		t.Fatal(err)
	}
	if got := svc.Current(); !got.GitLabEnabled() || got.SessionIdleTimeout != 30*time.Minute || got.GitLabClientSecret != "pg-secret" {
		t.Fatalf("Current = %+v", got)
	}
	rows, err := q.ListSettings(ctx)
	if err != nil || len(rows) != 4 {
		t.Fatalf("rows = %v, %v", rows, err)
	}
	for _, r := range rows {
		if r.Key == "gitlab.client_secret" && (r.Value != nil || bytes.Contains(r.SecretEnc, []byte("pg-secret"))) {
			t.Errorf("secret stored in the clear: %+v", r)
		}
	}

	patch := func(body string) int {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/", strings.NewReader(body))
		req = req.WithContext(auth.WithPrincipal(req.Context(), &auth.Principal{Source: "local", Username: "root"}))
		rr := httptest.NewRecorder()
		svc.UpdateHandler().ServeHTTP(rr, req)
		return rr.Code
	}
	if code := patch(`{"auth.local_login_enabled":false,"session.idle_timeout":"1h","gitlab.client_secret":"rotated"}`); code != 200 {
		t.Fatalf("patch status %d", code)
	}
	if code := patch(`{"session.idle_timeout":"1m","session.absolute_timeout":"2h"}`); code != 400 {
		t.Fatalf("invalid patch status %d", code)
	}

	// A restart sees the stored values, not the (changed) environment.
	seeds["session.idle_timeout"] = 2 * time.Hour
	again, err := New(ctx, q, tx, box, seeds, rec, discard)
	if err != nil {
		t.Fatal(err)
	}
	got := again.Current()
	if got.LocalLoginEnabled || got.SessionIdleTimeout != time.Hour || got.GitLabClientSecret != "rotated" {
		t.Errorf("after restart = %+v", got)
	}

	// Reset deletes the row and falls back to the env value.
	if code := patch(`{"session.idle_timeout":null}`); code != 200 {
		t.Fatalf("reset status %d", code)
	}
	rows, _ = q.ListSettings(ctx)
	for _, r := range rows {
		if r.Key == "session.idle_timeout" {
			t.Errorf("row still present: %+v", r)
		}
	}
	if svc.Current().SessionIdleTimeout != 30*time.Minute {
		t.Errorf("idle = %v", svc.Current().SessionIdleTimeout)
	}
	if len(rec.events) != 2 || rec.events[0].actor != "local:root" {
		t.Errorf("events = %+v", rec.events)
	}

	// A wrong key is reported, not a crash.
	other, _ := secretbox.New(bytes.Repeat([]byte{8}, secretbox.KeySize))
	if _, err := New(ctx, q, tx, other, nil, rec, discard); err == nil || !strings.Contains(err.Error(), "SCANOPSIS_SECRET_KEY changed or is wrong") {
		t.Errorf("wrong key err = %v", err)
	}
}

func TestIntegrationRetentionDaysSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	pool := storetest.New(t)
	q := store.New(pool)
	tx := func(ctx context.Context, fn func(Store) error) error {
		return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return fn(q.WithTx(tx)) })
	}
	box, err := secretbox.New(bytes.Repeat([]byte{7}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(ctx, q, tx, box, map[string]any{"retention.days": 30}, &fakeAudit{}, discard)
	if err != nil {
		t.Fatal(err)
	}
	if got := svc.Current().RetentionDays; got != 30 {
		t.Fatalf("seeded RetentionDays = %d", got)
	}
	if fields, err := svc.update(ctx, map[string]any{"retention.days": float64(365)}, "local:root"); err != nil || len(fields) != 0 {
		t.Fatalf("update: %v, %v", fields, err)
	}
	rows, err := q.ListSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Key == "retention.days" && string(r.Value) != "365" {
			t.Errorf("stored value = %s, want the JSON number 365", r.Value)
		}
	}
	again, err := New(ctx, q, tx, box, map[string]any{"retention.days": 30}, &fakeAudit{}, discard)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Current().RetentionDays; got != 365 {
		t.Errorf("after restart RetentionDays = %d, want 365", got)
	}
}
