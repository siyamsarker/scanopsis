// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package catalog

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type historyResponse struct {
	Services []struct {
		ServiceID int64  `json:"service_id"`
		Ref       string `json:"ref"`
		Points    []struct {
			PipelineID int64     `json:"pipeline_id"`
			At         time.Time `json:"at"`
			Counts     counts    `json:"counts"`
		} `json:"points"`
	} `json:"services"`
	Days []struct {
		At     time.Time `json:"at"`
		Counts counts    `json:"counts"`
	} `json:"days"`
	GeneratedAt time.Time `json:"generated_at"`
}

var historyJob atomic.Int64

// pipelineScan inserts one scan of a pipeline with the given counts.
func pipelineScan(t *testing.T, pool *pgxpool.Pool, svc int64, kind, artifact, ref string, pipeline int64,
	at time.Time, c counts) {
	t.Helper()
	mustExec(t, pool, `INSERT INTO scans (service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref,
		ref_protected, commit_sha, schema_version, scanned_at, count_critical, count_high, count_medium, count_low,
		count_unknown, raw_report)
		VALUES ($1,$2,$3,$4,$5,$6,false,'abc',2,$7,$8,$9,$10,$11,$12,'\x1f8b')`,
		svc, kind, artifact, pipeline, 700000+historyJob.Add(1), ref, at,
		c.Critical, c.High, c.Medium, c.Low, c.Unknown)
}

func TestPortfolioHistory(t *testing.T) {
	c, pool, _ := newCatalog(t, "")
	now := time.Now().UTC()
	h := func(d time.Duration) time.Time { return now.Add(-d) }
	const hour, day = time.Hour, 24 * time.Hour

	// Several artifacts: each pipeline's point is the state after its last
	// scan, every artifact carried forward. Pipeline 2 ends with api's scan.
	multi := addService(t, pool, "multi", "g/multi", false)
	pipelineScan(t, pool, multi, "image", "img", "main", 1, h(10*hour), counts{Critical: 1})
	pipelineScan(t, pool, multi, "image", "img", "main", 2, h(8*hour), counts{Critical: 2})
	pipelineScan(t, pool, multi, "image", "api", "main", 2, h(7*hour), counts{High: 4})
	pipelineScan(t, pool, multi, "secret-fs", "repo", "main", 3, h(6*hour+30*time.Minute), counts{})
	pipelineScan(t, pool, multi, "image", "img", "main", 3, h(6*hour), counts{Critical: 3})
	pipelineScan(t, pool, multi, "image", "api", "main", 4, h(5*hour), counts{High: 1})
	// Another branch does not count.
	pipelineScan(t, pool, multi, "image", "img", "feature", 9, h(9*hour), counts{Low: 50})

	// Thirty pipelines: the latest twenty are listed. The window of 24 scans
	// is full, so the days before it are found by a probe.
	long := addService(t, pool, "long", "g/long", false)
	for i := int64(1); i <= 30; i++ {
		pipelineScan(t, pool, long, "image", "img", "main", i, h(time.Duration(31-i)*hour+30*time.Minute), counts{Low: i})
	}

	// A group scanned long ago is carried into every point.
	carry := addService(t, pool, "carry", "g/carry", false)
	pipelineScan(t, pool, carry, "image", "old", "main", 100, h(100*hour), counts{Medium: 5})
	for i := int64(1); i <= 30; i++ {
		pipelineScan(t, pool, carry, "image", "new", "main", 100+i, h(time.Duration(31-i)*hour+30*time.Minute), counts{Critical: 1})
	}

	// Retried jobs: two scans of one artifact per pipeline. The 24 scans read
	// reach back twelve pipelines only, so only those are listed: before
	// them the state of the artifact is unknown.
	retry := addService(t, pool, "retry", "g/retry", false)
	for i := int64(1); i <= 20; i++ {
		at := h(time.Duration(21-i) * hour)
		pipelineScan(t, pool, retry, "image", "r", "main", i, at.Add(-2*time.Minute), counts{High: 100})
		pipelineScan(t, pool, retry, "image", "r", "main", i, at.Add(-time.Minute), counts{High: i})
		pipelineScan(t, pool, retry, "image", "s", "main", i, at, counts{Low: 1})
	}

	// Daily states: one scan a week ago, one four and a bit days ago, one now.
	daily := addService(t, pool, "daily", "g/daily", false)
	pipelineScan(t, pool, daily, "image", "img", "main", 1, h(8*day+hour), counts{High: 1})
	pipelineScan(t, pool, daily, "image", "img", "main", 2, h(5*day-hour), counts{High: 2})
	pipelineScan(t, pool, daily, "image", "img", "main", 3, h(2*hour), counts{High: 3})

	// Not listed: archived, no scans, a chosen branch without scans.
	archived := addService(t, pool, "archived", "g/archived", true)
	pipelineScan(t, pool, archived, "image", "img", "main", 1, h(hour), counts{Critical: 9})
	addService(t, pool, "none", "g/none", false)
	chosen := addService(t, pool, "chosen", "g/chosen", false)
	pipelineScan(t, pool, chosen, "image", "img", "main", 1, h(hour), counts{Critical: 9})
	mustExec(t, pool, `UPDATE services SET default_ref = 'release' WHERE id = $1`, chosen)

	got := decode[historyResponse](t, call(t, c, "getPortfolioHistory", "GET", "/", "", ""), http.StatusOK)
	type point struct {
		pipeline int64
		counts   counts
	}
	points := map[int64][]point{}
	var ids []int64
	for _, s := range got.Services {
		ids = append(ids, s.ServiceID)
		if s.Ref != "main" {
			t.Errorf("service %d ref = %s", s.ServiceID, s.Ref)
		}
		for i, p := range s.Points {
			points[s.ServiceID] = append(points[s.ServiceID], point{p.PipelineID, p.Counts})
			if i > 0 && p.At.Before(s.Points[i-1].At) {
				t.Errorf("service %d: points not oldest first", s.ServiceID)
			}
		}
	}
	eq := func(what string, got, want any) {
		t.Helper()
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s =\n%v\nwant\n%v", what, got, want)
		}
	}
	eq("services", ids, []int64{multi, long, carry, retry, daily})
	eq("multi", points[multi], []point{
		{1, counts{Critical: 1}}, {2, counts{Critical: 2, High: 4}},
		{3, counts{Critical: 3, High: 4}}, {4, counts{Critical: 3, High: 1}},
	})
	var wantLong, wantCarry []point
	for i := int64(11); i <= 30; i++ {
		wantLong = append(wantLong, point{i, counts{Low: i}})
		wantCarry = append(wantCarry, point{100 + i, counts{Critical: 1, Medium: 5}})
	}
	eq("long", points[long], wantLong)
	eq("carry", points[carry], wantCarry)
	var wantRetry []point
	for i := int64(9); i <= 20; i++ {
		wantRetry = append(wantRetry, point{i, counts{High: i, Low: 1}})
	}
	eq("retry", points[retry], wantRetry)
	eq("daily", points[daily], []point{{1, counts{High: 1}}, {2, counts{High: 2}}, {3, counts{High: 3}}})

	// The last point of every service is its portfolio state.
	pf := decode[pfResponse](t, call(t, c, "getPortfolio", "GET", "/", "", ""), http.StatusOK)
	for _, it := range pf.Items {
		if ps := points[it.Service.ID]; len(ps) > 0 && ps[len(ps)-1].counts != it.Counts {
			t.Errorf("%s: last point %+v, portfolio %+v", it.Service.Name, ps[len(ps)-1].counts, it.Counts)
		}
	}

	// Days: 7 to 1 days back. long and carry's new artifact are known a day
	// back only (their scans start 30.5 hours ago); carry's old artifact
	// from 100 hours back; daily changes four and a bit days back.
	var days []counts
	for i, d := range got.Days {
		days = append(days, d.Counts)
		if want := got.GeneratedAt.Add(-time.Duration(7-i) * day); !d.At.Equal(want) {
			t.Errorf("day %d at %v, want %v", i, d.At, want)
		}
	}
	eq("days", days, []counts{
		{High: 1}, {High: 1}, {High: 1},
		{High: 2, Medium: 5}, {High: 2, Medium: 5}, {High: 2, Medium: 5},
		{Critical: 1, High: 2, Medium: 5, Low: 7},
	})
}

func TestPortfolioHistoryEmpty(t *testing.T) {
	c, _, _ := newCatalog(t, "")
	rec := call(t, c, "getPortfolioHistory", "GET", "/", "", "")
	got := decode[historyResponse](t, rec, http.StatusOK)
	if got.Services == nil || len(got.Services) != 0 || len(got.Days) != 7 {
		t.Fatalf("empty history = %s", rec.Body.String())
	}
	for _, d := range got.Days {
		if d.Counts != (counts{}) {
			t.Errorf("empty day = %+v", d)
		}
	}
}

// The history is cached. After a new scan, an archived service or another
// dashboard branch the last history is served for the grace period, then the
// change shows; concurrent readers share one computation.
func TestPortfolioHistoryFollowsChanges(t *testing.T) {
	c, pool, _ := newCatalog(t, "")
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	c.history.now = func() time.Time { return time.Unix(0, clock.Load()) }
	later := func() { clock.Add(int64(historyGrace)) }
	var computed atomic.Int32
	compute := c.history.compute
	c.history.compute = func(ctx context.Context) ([]byte, error) {
		computed.Add(1)
		return compute(ctx)
	}

	now := time.Now().UTC()
	svc := addService(t, pool, "svc", "g/svc", false)
	pipelineScan(t, pool, svc, "image", "img", "main", 1, now.Add(-2*time.Hour), counts{High: 1})
	points := func() []int64 {
		t.Helper()
		h := decode[historyResponse](t, call(t, c, "getPortfolioHistory", "GET", "/", "", ""), http.StatusOK)
		out := []int64{}
		for _, s := range h.Services {
			for _, p := range s.Points {
				out = append(out, s.ServiceID*1000+p.PipelineID)
			}
		}
		return out
	}
	want := func(what string, got, want []int64) {
		t.Helper()
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s: %v, want %v", what, got, want)
		}
	}
	want("first", points(), []int64{svc*1000 + 1})
	want("cached", points(), []int64{svc*1000 + 1})
	pipelineScan(t, pool, svc, "image", "img", "main", 2, now.Add(-time.Hour), counts{High: 2})
	want("within the grace period", points(), []int64{svc*1000 + 1})
	later()
	want("after an upload", points(), []int64{svc*1000 + 1, svc*1000 + 2})
	pipelineScan(t, pool, svc, "image", "img", "dev", 3, now.Add(-time.Minute), counts{High: 3})
	later()
	want("another branch is the latest", points(), []int64{svc*1000 + 3})
	mustExec(t, pool, `UPDATE services SET default_ref = 'main' WHERE id = $1`, svc)
	later()
	want("dashboard branch chosen", points(), []int64{svc*1000 + 1, svc*1000 + 2})
	mustExec(t, pool, `UPDATE services SET archived = true WHERE id = $1`, svc)
	later()
	want("archived", points(), []int64{})
	if n := computed.Load(); n != 5 {
		t.Errorf("%d computations, want 5", n)
	}

	// Twenty readers after a change: one computation; each reader gets the
	// last history or the new one, and the next reader the new one.
	mustExec(t, pool, `UPDATE services SET archived = false WHERE id = $1`, svc)
	later()
	bodies := make([]string, 20)
	var wg sync.WaitGroup
	for i := range bodies {
		wg.Go(func() { bodies[i] = call(t, c, "getPortfolioHistory", "GET", "/", "", "").Body.String() })
	}
	wg.Wait()
	for i, b := range bodies {
		if !strings.Contains(b, `"services":[]`) && !strings.Contains(b, `"pipeline_id":2`) {
			t.Errorf("reader %d got %s", i, b)
		}
	}
	want("the next reader", points(), []int64{svc*1000 + 1, svc*1000 + 2})
	if n := computed.Load(); n != 6 {
		t.Errorf("%d computations, want 6", n)
	}
}
