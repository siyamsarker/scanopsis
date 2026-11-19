// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package sonar

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/testutil/fakesonar"
)

func (e *env) runSync(t *testing.T) {
	t.Helper()
	e.s.sync(context.Background(), e.cfg.Current())
}

func (e *env) state() (version string, at time.Time, lastErr string) {
	e.s.mu.Lock()
	defer e.s.mu.Unlock()
	return e.s.version, e.s.lastSyncAt, e.s.lastErr
}

// syncProject registers a project whose gate has one condition, as the Web API
// reports it.
func (e *env) syncProject(key string, analyses ...fakesonar.Analysis) {
	e.fake.Set(key, fakesonar.Project{
		Measures:   fakesonar.StandardMeasures(),
		GateStatus: "WARN",
		Conditions: []fakesonar.Condition{
			{Status: "WARN", MetricKey: "coverage", Comparator: "LT", ErrorThreshold: "85", ActualValue: "81.3"},
			{Status: "NO_VALUE", MetricKey: "new_coverage", Comparator: "LT", ErrorThreshold: "80"},
		},
		Analyses: analyses,
	})
}

func TestSyncStoresNewAnalysis(t *testing.T) {
	e := newEnv(t)
	e.s.now = func() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) }
	sid := e.service(t, "platform/payments-api", 0, "platform:payments-api")
	e.service(t, "platform/no-sonar", 0, "") // not linked: never read
	e.syncProject("platform:payments-api", fakesonar.Analysis{Key: "AY1", Date: analysisTime, Revision: "abc123"})

	e.runSync(t)

	rows := e.analyses(t, sid)
	if len(rows) != 1 {
		t.Fatalf("%d analyses", len(rows))
	}
	a := rows[0]
	if a.TaskID != "sync:AY1" || a.AnalysisKey != "AY1" || !a.AnalysedAt.Time.Equal(analysisTime) || a.Revision != "abc123" ||
		a.QualityGateStatus != "WARN" || a.Mode != "standard" || a.ReliabilityRating != "C" || a.GitlabPipelineID.Valid {
		t.Errorf("analysis: %+v", a)
	}
	var conds []Condition
	if err := json.Unmarshal(a.GateConditions, &conds); err != nil {
		t.Fatal(err)
	}
	want := []Condition{
		{Metric: "coverage", Operator: "LESS_THAN", Threshold: "85", Value: str("81.3"), Status: "WARN"},
		{Metric: "new_coverage", Operator: "LESS_THAN", Threshold: "80", Value: nil, Status: "NO_VALUE"},
	}
	if !reflect.DeepEqual(conds, want) {
		t.Errorf("conditions: %+v", conds)
	}
	v, at, lastErr := e.state()
	if v != "26.9.0.12345" || !at.Equal(time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)) || lastErr != "" {
		t.Errorf("state: %q %v %q", v, at, lastErr)
	}
	if n := e.fake.Count("api/project_analyses/search"); n != 1 {
		t.Errorf("searched %d times (an unlinked service must not be read)", n)
	}
}

func TestSyncSkipsKnownAndAddsNew(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "platform/payments-api", 0, "platform:payments-api")
	e.syncProject("platform:payments-api", fakesonar.Analysis{Key: "AY1", Date: analysisTime})
	e.runSync(t)
	e.runSync(t)
	if n := len(e.analyses(t, sid)); n != 1 {
		t.Fatalf("%d analyses after two syncs", n)
	}
	if n := e.fake.Count("api/measures/component"); n != 1 {
		t.Errorf("measures read %d times for a known analysis", n)
	}

	e.syncProject("platform:payments-api", fakesonar.Analysis{Key: "AY2", Date: analysisTime.Add(time.Hour)})
	e.runSync(t)
	rows := e.analyses(t, sid)
	if len(rows) != 2 || rows[0].AnalysisKey != "AY2" || rows[1].AnalysisKey != "AY1" {
		t.Errorf("rows: %+v", rows)
	}
}

func TestSyncSkipsAnalysisStoredByWebhook(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "platform/payments-api", 0, "platform:payments-api")
	e.project("platform:payments-api")
	wantStatus(t, e, hook("task-1", "platform:payments-api", nil), 202)
	e.drain(t, 1)
	e.runSync(t)
	if n := len(e.analyses(t, sid)); n != 1 {
		t.Errorf("%d analyses; the webhook's analysis was stored again", n)
	}
}

func TestSyncProjectWithoutAnalysis(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "platform/payments-api", 0, "platform:payments-api")
	e.syncProject("platform:payments-api")
	e.runSync(t)
	if n := len(e.analyses(t, sid)); n != 0 {
		t.Errorf("%d analyses", n)
	}
	if _, _, lastErr := e.state(); lastErr != "" {
		t.Errorf("lastErr %q", lastErr)
	}
}

func TestSyncErrors(t *testing.T) {
	e := newEnv(t)
	bad := e.service(t, "platform/bad", 0, "platform:bad")
	good := e.service(t, "platform/good", 0, "platform:good")
	e.syncProject("platform:bad", fakesonar.Analysis{Key: "B1", Date: analysisTime})
	e.syncProject("platform:good", fakesonar.Analysis{Key: "G1", Date: analysisTime})
	e.fake.Fail("api/qualitygates/project_status", 500)

	e.runSync(t)
	if len(e.analyses(t, bad)) != 0 || len(e.analyses(t, good)) != 0 {
		t.Error("a failed project must store nothing")
	}
	_, at, lastErr := e.state()
	if lastErr != "platform:good: SonarQube returned HTTP 500 for api/qualitygates/project_status" || at.IsZero() {
		t.Errorf("lastErr %q at %v", lastErr, at)
	}
	e.fake.Fail("api/qualitygates/project_status", 0)

	// The next sync recovers and clears the error.
	e.runSync(t)
	if len(e.analyses(t, good)) != 1 || len(e.analyses(t, bad)) != 1 {
		t.Error("recovery failed")
	}
	if _, _, lastErr := e.state(); lastErr != "" {
		t.Errorf("lastErr %q", lastErr)
	}

	// An unknown project is reported, the others still run.
	e.fake.Fail("api/server/version", 404)
	e.service(t, "platform/gone", 0, "platform:gone")
	e.runSync(t)
	if _, _, lastErr := e.state(); lastErr != "platform:gone: SonarQube returned HTTP 404 for api/project_analyses/search" {
		t.Errorf("lastErr %q", lastErr)
	}
}

func TestSyncWrongTokenStopsEarly(t *testing.T) {
	e := newEnv(t)
	e.service(t, "platform/a", 0, "platform:a")
	e.service(t, "platform/b", 0, "platform:b")
	e.cfg.set(func(s *settings.Snapshot) { s.SonarToken = "wrong" })
	e.runSync(t)
	if n := e.fake.Count("api/project_analyses/search"); n != 1 {
		t.Errorf("%d searches; a rejected token must stop the cycle", n)
	}
	if _, _, lastErr := e.state(); lastErr != "platform:a: SonarQube returned HTTP 401 for api/project_analyses/search" {
		t.Errorf("lastErr %q", lastErr)
	}
}

func TestSyncCancelled(t *testing.T) {
	e := newEnv(t)
	e.service(t, "platform/a", 0, "platform:a")
	e.syncProject("platform:a", fakesonar.Analysis{Key: "A1", Date: analysisTime})
	e.fake.SetDelay(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	e.s.sync(ctx, e.cfg.Current())
	if _, at, _ := e.state(); !at.IsZero() {
		t.Error("a cancelled sync must not record a completed run")
	}
}

func TestSyncLockPreventsConcurrentRuns(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "platform/payments-api", 0, "platform:payments-api")
	e.syncProject("platform:payments-api", fakesonar.Analysis{Key: "AY1", Date: analysisTime})
	e.fake.SetDelay(150 * time.Millisecond)
	other := New(e.pool, e.cfg, Options{HTTPClient: e.fake.Client()})
	other.tick = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{}, 2)
	go func() { e.s.Run(ctx); done <- struct{}{} }()
	go func() { other.Run(ctx); done <- struct{}{} }()

	deadline := time.Now().Add(10 * time.Second)
	for len(e.analyses(t, sid)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond) // let the second loop show it would have run
	cancel()
	<-done
	<-done
	if n := e.fake.Count("api/project_analyses/search"); n != 1 {
		t.Errorf("%d searches from two Run loops, want 1", n)
	}
	if n := e.fake.Count("api/server/version"); n != 1 {
		t.Errorf("%d version reads from two Run loops, want 1", n)
	}
	if n := len(e.analyses(t, sid)); n != 1 {
		t.Errorf("%d analyses", n)
	}
	// The lock was released: a later sync can take it.
	e.runSync(t)
	if n := e.fake.Count("api/project_analyses/search"); n != 2 {
		t.Errorf("%d searches after the lock was released, want 2", n)
	}
}

func TestRunPicksUpSettingChanges(t *testing.T) {
	e := newEnv(t)
	e.s.minInterval = 10 * time.Millisecond
	sid := e.service(t, "platform/payments-api", 0, "platform:payments-api")
	e.syncProject("platform:payments-api", fakesonar.Analysis{Key: "AY1", Date: analysisTime})

	// Not configured: nothing runs.
	e.cfg.set(func(s *settings.Snapshot) { s.SonarToken = "" })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.s.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	time.Sleep(150 * time.Millisecond)
	if n := e.fake.Count("api/server/version"); n != 0 {
		t.Fatalf("synced while not configured (%d)", n)
	}

	// Configuring it starts the first sync.
	e.cfg.set(func(s *settings.Snapshot) { s.SonarToken = testToken })
	waitFor(t, func() bool { return len(e.analyses(t, sid)) == 1 })

	// A new analysis waits for the one hour interval...
	e.syncProject("platform:payments-api", fakesonar.Analysis{Key: "AY2", Date: analysisTime.Add(time.Hour)})
	time.Sleep(150 * time.Millisecond)
	if n := len(e.analyses(t, sid)); n != 1 {
		t.Fatalf("%d analyses before the interval elapsed", n)
	}
	// ...until a shorter interval is set.
	e.cfg.set(func(s *settings.Snapshot) { s.SonarSyncInterval = 30 * time.Millisecond })
	waitFor(t, func() bool { return len(e.analyses(t, sid)) == 2 })
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}
