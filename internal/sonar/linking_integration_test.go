// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package sonar

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/store"
)

func (e *env) serviceKey(t *testing.T, id int64) (string, bool) {
	t.Helper()
	k, err := e.q.SonarServiceKey(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return k.String, k.Valid
}

func (e *env) archive(t *testing.T, id int64) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `UPDATE services SET archived = true WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookPropertyCannotHijackLinkedService(t *testing.T) {
	e := newEnv(t)
	victim := e.service(t, "team-a/svc", 1, "team-a:svc")
	e.project("evil:project")
	props := map[string]string{"sonar.analysis.gitlabProjectId": "1"}
	wantStatus(t, e, hook("task-h", "evil:project", map[string]any{"properties": props}), 202)
	e.drain(t, 1)
	d := e.delivery(t, "task-h")
	want := `The service named by sonar.analysis.gitlabProjectId is linked to a different SonarQube project, so project "evil:project" was not linked to it. Change the SonarQube key on the service if the project was renamed.`
	if d.Outcome != "unlinked" || d.ServiceID.Valid || d.Detail != want {
		t.Errorf("delivery: %+v", d)
	}
	if n := len(e.analyses(t, victim)); n != 0 {
		t.Errorf("%d analyses stored on another team's service", n)
	}
	if k, _ := e.serviceKey(t, victim); k != "team-a:svc" {
		t.Errorf("key changed to %q", k)
	}
	if n := e.fake.Count("api/measures/component"); n != 0 {
		t.Errorf("%d Web API calls for an unlinked delivery", n)
	}
}

func TestWebhookPropertyPinsUnlinkedService(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "platform/new", 2, "")
	e.project("new:key")
	props := map[string]string{"sonar.analysis.gitlabProjectId": "2"}
	wantStatus(t, e, hook("task-p1", "new:key", map[string]any{"properties": props}), 202)
	e.drain(t, 1)
	if d := e.delivery(t, "task-p1"); d.Outcome != "linked" || d.ServiceID.Int64 != sid {
		t.Errorf("delivery: %+v", d)
	}
	if k, ok := e.serviceKey(t, sid); !ok || k != "new:key" {
		t.Errorf("key %q %v, want it pinned", k, ok)
	}
	// Later deliveries link by key alone; another project can no longer take over.
	wantStatus(t, e, hook("task-p2", "new:key", nil), 202)
	e.drain(t, 1)
	if d := e.delivery(t, "task-p2"); d.Outcome != "linked" || d.ServiceID.Int64 != sid {
		t.Errorf("by key: %+v", d)
	}
	e.project("other:key")
	wantStatus(t, e, hook("task-p3", "other:key", map[string]any{"properties": props}), 202)
	e.drain(t, 1)
	if d := e.delivery(t, "task-p3"); d.Outcome != "unlinked" {
		t.Errorf("takeover: %+v", d)
	}
}

// Two deliveries of the same project, both naming the unlinked service of
// their GitLab project, are processed at the same time: both find the
// service unlinked, one links it and the other then finds it linked to the
// very same key. Both end linked to the service, and the link is recorded
// once.
func TestWebhookConcurrentDeliveriesOfOneProjectBothLink(t *testing.T) {
	tests := []struct {
		name, secondAt string
		analyses       int
		secondDetail   string
	}{
		{"same analysis", "2026-11-19T10:00:00+0000", 1,
			"The analysis was already stored while this delivery was processed."},
		{"older analysis", "2026-11-19T09:00:00+0000", 2,
			"A newer analysis (2026-11-19T10:00:00Z) already exists in SonarQube; the measures are from it. The next sync will add it."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			sid := e.service(t, "platform/payments-api", 5, "")
			e.project("platform:payments-api")
			// Both deliveries are held at their first SonarQube call until
			// both have read the service, so both find it unlinked.
			var arrived atomic.Int32
			both := make(chan struct{})
			e.intercept(func(r *http.Request, next http.RoundTripper) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "api/measures/component") && arrived.Add(1) == 2 {
					close(both)
				}
				select {
				case <-both:
				case <-time.After(10 * time.Second):
				}
				return next.RoundTrip(r)
			})
			props := map[string]string{"sonar.analysis.gitlabProjectId": "5", "sonar.analysis.pipelineId": "77"}
			wantStatus(t, e, hook("task-r1", "platform:payments-api", map[string]any{"properties": props}), 202)
			wantStatus(t, e, hook("task-r2", "platform:payments-api", map[string]any{"properties": props, "analysedAt": tt.secondAt}), 202)
			var wg sync.WaitGroup
			for range 2 {
				d := <-e.s.queue
				wg.Go(func() { e.s.process(context.Background(), d) })
			}
			wg.Wait()
			if arrived.Load() != 2 {
				t.Fatalf("%d measures calls, want 2", arrived.Load())
			}

			details := map[string]bool{}
			for _, task := range []string{"task-r1", "task-r2"} {
				d := e.delivery(t, task)
				if d.Outcome != "linked" || d.ServiceID.Int64 != sid {
					t.Errorf("%s: %+v", task, d)
				}
				details[d.Detail] = true
			}
			if !details[tt.secondDetail] {
				t.Errorf("details %v, want one to be %q", details, tt.secondDetail)
			}
			if k, ok := e.serviceKey(t, sid); !ok || k != "platform:payments-api" {
				t.Errorf("key %q %v", k, ok)
			}
			if n := len(e.analyses(t, sid)); n != tt.analyses {
				t.Errorf("%d analyses, want %d", n, tt.analyses)
			}
			var pins int
			if err := e.pool.QueryRow(context.Background(),
				`SELECT count(*) FROM audit_log WHERE action = 'service.updated' AND detail->>'sonar_project_key' = 'platform:payments-api'`).Scan(&pins); err != nil {
				t.Fatal(err)
			}
			if pins != 1 {
				t.Errorf("%d link records, want 1", pins)
			}
		})
	}
}

// A delivery whose link finds the service taken stores its analysis only if
// the service is still linked to its key, read under the service's row lock
// in the transaction that stores. Here another link takes the service first,
// and an administrator changes the key just as the delivery looks again: the
// delivery is not linked and stores nothing on the service.
func TestWebhookLinkedMeanwhileFollowsTheLockedKey(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sid := e.service(t, "platform/payments-api", 5, "")
	e.project("platform:payments-api")
	props := map[string]string{"sonar.analysis.gitlabProjectId": "5"}
	wantStatus(t, e, hook("task-k1", "platform:payments-api", map[string]any{"properties": props}), 202)

	// The other link holds the service's row until the delivery waits for it.
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := e.q.WithTx(tx).PinSonarKey(ctx, store.PinSonarKeyParams{
		ID: sid, SonarProjectKey: pgtype.Text{String: "platform:payments-api", Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	adminDone := make(chan error, 1)
	e.s.beforeStoreLinked = func() {
		admin, err := e.pool.Begin(ctx)
		if err != nil {
			adminDone <- err
			return
		}
		if _, err := admin.Exec(ctx, `UPDATE services SET sonar_project_key = 'platform:other' WHERE id = $1`, sid); err != nil {
			_ = admin.Rollback(ctx)
			adminDone <- err
			return
		}
		go func() {
			waitFor(t, func() bool {
				select {
				case <-done:
					return true
				default:
				}
				return e.lockWaits(t, "") == 1
			})
			adminDone <- admin.Commit(ctx)
		}()
	}
	d := <-e.s.queue
	go func() { e.s.process(ctx, d); close(done) }()
	waitFor(t, func() bool { return e.lockWaits(t, "UPDATE services SET sonar_project_key") == 1 })
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-adminDone; err != nil {
		t.Fatal(err)
	}
	<-done

	got := e.delivery(t, "task-k1")
	want := `The service named by sonar.analysis.gitlabProjectId cannot be linked to SonarQube project "platform:payments-api" because the service or the key is already linked, or the service's pipeline declared another key in an upload.`
	if got.Outcome != "unlinked" || got.ServiceID.Valid || got.Detail != want {
		t.Errorf("delivery: %+v", got)
	}
	if n := len(e.analyses(t, sid)); n != 0 {
		t.Errorf("%d analyses stored on a service linked to another key", n)
	}
	if k, _ := e.serviceKey(t, sid); k != "platform:other" {
		t.Errorf("key %q", k)
	}
}

func TestWebhookPinConflict(t *testing.T) {
	e := newEnv(t)
	old := e.service(t, "platform/old", 0, "shared:key")
	e.archive(t, old) // archived services are not looked up, but still own the key
	sid := e.service(t, "platform/new", 3, "")
	e.project("shared:key")
	props := map[string]string{"sonar.analysis.gitlabProjectId": "3"}
	wantStatus(t, e, hook("task-c", "shared:key", map[string]any{"properties": props}), 202)
	e.drain(t, 1)
	d := e.delivery(t, "task-c")
	want := `The service named by sonar.analysis.gitlabProjectId cannot be linked to SonarQube project "shared:key" because the service or the key is already linked, or the service's pipeline declared another key in an upload.`
	if d.Outcome != "unlinked" || d.Detail != want {
		t.Errorf("delivery: %+v", d)
	}
	if _, ok := e.serviceKey(t, sid); ok {
		t.Error("the key must stay unset")
	}
	if n := len(e.analyses(t, sid)); n != 0 {
		t.Errorf("%d analyses", n)
	}
}

func TestWebhookIgnoresArchivedServices(t *testing.T) {
	e := newEnv(t)
	byKey := e.service(t, "platform/a", 0, "a:key")
	byID := e.service(t, "platform/b", 7, "")
	e.archive(t, byKey)
	e.archive(t, byID)
	e.project("a:key")
	e.project("b:key")
	wantStatus(t, e, hook("task-a1", "a:key", nil), 202)
	wantStatus(t, e, hook("task-a2", "b:key", map[string]any{"properties": map[string]string{"sonar.analysis.gitlabProjectId": "7"}}), 202)
	e.drain(t, 2)
	for _, task := range []string{"task-a1", "task-a2"} {
		if d := e.delivery(t, task); d.Outcome != "unlinked" {
			t.Errorf("%s: %+v", task, d)
		}
	}
	if _, ok := e.serviceKey(t, byID); ok {
		t.Error("an archived service must not be pinned")
	}
}

func TestWebhookFailedTaskDoesNotPin(t *testing.T) {
	e := newEnv(t)
	sid := e.service(t, "platform/new", 4, "")
	e.project("new:key")
	props := map[string]string{"sonar.analysis.gitlabProjectId": "4"}
	wantStatus(t, e, hook("task-f", "new:key", map[string]any{"properties": props, "status": "FAILED"}), 202)
	e.drain(t, 1)
	if _, ok := e.serviceKey(t, sid); ok {
		t.Error("a failed task must not link the service")
	}
}

func TestDeliveryDetailHasNoInternals(t *testing.T) {
	e := newEnv(t)
	e.service(t, "platform/payments-api", 0, "platform:payments-api")
	e.project("platform:payments-api")
	e.fake.Close() // connection refused: the raw error names the address
	wantStatus(t, e, hook("task-n", "platform:payments-api", nil), 202)
	e.drain(t, 1)
	d := e.delivery(t, "task-n")
	want := "Reading measures failed: the request failed or the answer was unreadable; see the server log. The next sync will retry."
	if d.Outcome != "failed" || d.Detail != want {
		t.Errorf("delivery: %+v", d)
	}
}

func TestSyncErrorHasNoInternals(t *testing.T) {
	e := newEnv(t)
	e.service(t, "platform/a", 0, "platform:a")
	e.fake.Close()
	e.runSync(t)
	want := "the request failed or the answer was unreadable; see the server log"
	if _, _, lastErr := e.state(); lastErr != want {
		t.Errorf("lastErr %q", lastErr)
	}
}
