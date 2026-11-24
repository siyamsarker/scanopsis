// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package ingest

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/sonar"
)

// beforeLink runs a function just before LinkWaiting of the real linker, as
// a delivery processed while the upload commits would have done its part.
type beforeLink struct {
	*sonar.Sonar
	before func()
}

func (b beforeLink) LinkWaiting(ctx context.Context, serviceID, gitlabProjectID, pipelineID int64) error {
	if b.before != nil {
		b.before()
	}
	return b.Sonar.LinkWaiting(ctx, serviceID, gitlabProjectID, pipelineID)
}

// deliveryOf returns the outcome and detail of a delivery.
func (e *iEnv) deliveryOf(task string) (outcome, detail string) {
	e.t.Helper()
	if err := e.pool.QueryRow(context.Background(), `SELECT outcome, detail FROM sonar_deliveries WHERE task_id = $1`, task).
		Scan(&outcome, &detail); err != nil {
		e.t.Fatal(err)
	}
	return outcome, detail
}

// The upload that creates the service with a pending declared key still
// links the waiting analyses of its pipeline, by the rule of a declared key:
// an analysis of another key is not linked and now says that the service
// waits for the declared key; an analysis of the declared key that arrived
// while the upload committed is linked.
func TestIntegrationSonarKeyPendingLinksWaiting(t *testing.T) {
	for _, tc := range []struct {
		name    string
		lateKey bool // a delivery of the declared key arrives just before the link
	}{
		{"another key waits", false},
		{"the declared key arrives meanwhile", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var e *iEnv
			l := beforeLink{}
			e = newIEnv(t, func(o *Options) { o.SonarLinker = &l })
			l.Sonar = sonar.New(e.pool, e.cfg, sonar.Options{Logger: slog.New(slog.DiscardHandler)})
			e.sonarDelivery("task-x", "other:project", 42, time.Minute)
			if tc.lateKey {
				l.before = func() { e.sonarDelivery("task-k", "platform:payments-api", 42, 0) }
			}
			code, res := e.uploadRaw(upload{body: sonarKeyBody, query: "?sonar_key=platform:payments-api"})
			if code != http.StatusCreated || res["service_created"] != true || res["sonar_key_ignored"] != "sonar_key_pending" {
				t.Fatalf("%d %v", code, res)
			}
			sid := int64(res["service_id"].(float64))
			outcome, detail := e.deliveryOf("task-x")
			want := `The service of GitLab project 42 waits for SonarQube project "platform:payments-api", the key its pipeline declared in an upload, so this analysis was not linked to it. An administrator can set the SonarQube key on the service.`
			if tc.lateKey {
				want = `Not linked: the service of GitLab project 42 was linked to SonarQube project "platform:payments-api" by the analysis of the pipeline of its first scan. An administrator can change the SonarQube key on the service.`
			}
			if outcome != "unlinked" || detail != want {
				t.Errorf("other key: %s %q\nwant %q", outcome, detail, want)
			}
			wantKey := ""
			if tc.lateKey {
				wantKey = "platform:payments-api"
				if outcome, _ := e.deliveryOf("task-k"); outcome != "linked" {
					t.Errorf("declared key delivery: %s", outcome)
				}
			}
			if k := e.sonarKeyOf(sid); k != wantKey {
				t.Errorf("key %q, want %q", k, wantKey)
			}
		})
	}
}
