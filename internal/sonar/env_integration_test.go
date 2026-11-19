// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package sonar

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
	"github.com/siyamsarker/scanopsis/internal/testutil/fakesonar"
)

type env struct {
	pool *pgxpool.Pool
	fake *fakesonar.Server
	cfg  *fixedSettings
	s    *Sonar
	h    http.Handler
	q    *store.Queries
}

var analysisTime = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

// newEnv returns a Sonar on a fresh database with a fake SonarQube. Run is
// not started; tests start it or call process themselves.
func newEnv(t *testing.T) *env {
	t.Helper()
	pool := storetest.New(t)
	fake := fakesonar.New(testToken)
	t.Cleanup(fake.Close)
	cfg := &fixedSettings{snap: settings.Snapshot{
		SonarURL: fake.URL, SonarToken: testToken, SonarWebhookSecret: testSecret,
		SonarSyncInterval: time.Hour,
	}}
	s := New(pool, cfg, Options{HTTPClient: fake.Client()})
	s.tick = 20 * time.Millisecond
	return &env{pool: pool, fake: fake, cfg: cfg, s: s, h: s.Handlers()["sonarWebhook"], q: store.New(pool)}
}

// service inserts a service. gitlabID 0 and key "" mean unset.
func (e *env) service(t *testing.T, path string, gitlabID int64, key string) int64 {
	t.Helper()
	var gid, k any
	if gitlabID != 0 {
		gid = gitlabID
	}
	if key != "" {
		k = key
	}
	var id int64
	err := e.pool.QueryRow(context.Background(),
		`INSERT INTO services (name, gitlab_project_id, gitlab_project_path, sonar_project_key, created_via)
		 VALUES ($1, $2, $1, $3, 'manual') RETURNING id`, path, gid, k).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// project registers fake data for a key: MQR measures, a failing gate, and one
// analysis at analysisTime.
func (e *env) project(key string) {
	e.fake.Set(key, fakesonar.Project{
		Measures:   fakesonar.MQRMeasures(),
		GateStatus: "ERROR",
		Analyses:   []fakesonar.Analysis{{Key: "AY1", Date: analysisTime, Revision: "abc123"}},
	})
}

// drain processes the queued deliveries without Run.
func (e *env) drain(t *testing.T, n int) {
	t.Helper()
	for range n {
		select {
		case d := <-e.s.queue:
			e.s.process(context.Background(), d)
		default:
			t.Fatal("no queued delivery")
		}
	}
}

func (e *env) delivery(t *testing.T, taskID string) store.ListSonarDeliveriesRow {
	t.Helper()
	rows, err := e.q.ListSonarDeliveries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.TaskID == taskID {
			return r
		}
	}
	t.Fatalf("no delivery %s", taskID)
	return store.ListSonarDeliveriesRow{}
}

func (e *env) analyses(t *testing.T, serviceID int64) []store.SonarAnalysis {
	t.Helper()
	rows, err := e.q.ListSonarAnalyses(context.Background(), store.ListSonarAnalysesParams{ServiceID: serviceID, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// waitDelivery polls until a delivery has left the pending state.
func (e *env) waitDelivery(t *testing.T, taskID string) store.ListSonarDeliveriesRow {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := e.q.ListSonarDeliveries(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.TaskID == taskID && r.Outcome != "pending" {
				return r
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("delivery %s was not processed", taskID)
	return store.ListSonarDeliveriesRow{}
}
