// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package findings

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

// insertVulns adds n findings to a scan with set-based SQL: vuln_id is
// prefix || g, one package per finding, medium severity.
func insertVulns(t *testing.T, pool *pgxpool.Pool, scan int64, prefix string, from, n int) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO vulnerabilities (scan_id, target, class, pkg_name, installed_version, vuln_id, severity)
		SELECT $1, 'debian 12', 'os-pkgs', 'pkg' || g, '1.0', $2::text || g, 'MEDIUM'
		FROM generate_series($3::int, $3::int + $4::int - 1) g`, scan, prefix, from, n)
	if err != nil {
		t.Fatal(err)
	}
	refresh(t, pool, scan)
}

type searchBody struct {
	Scope  string `json:"scope"`
	VulnID string `json:"vuln_id"`
	Items  []struct {
		Severity string `json:"severity"`
	} `json:"items"`
	Truncated     bool  `json:"truncated"`
	TotalFindings int64 `json:"total_findings"`
	TotalServices int64 `json:"total_services"`
}

func TestSearchIsBounded(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	mux := newMux(pool, "")
	svc := newService(t, pool, "wide", "grp/wide", false)
	now := time.Now().UTC().Truncate(time.Second)
	old := newScan(t, pool, svc, "image", "app", "main", now.Add(-2*time.Hour))
	cur := newScan(t, pool, svc, "image", "app", "main", now.Add(-time.Hour))
	// The ID is in the current scan 1001 times (one per package) and once
	// more in an older scan, which the current state does not include.
	_, err := pool.Exec(context.Background(), `
		INSERT INTO vulnerabilities (scan_id, target, class, pkg_name, installed_version, vuln_id, severity)
		SELECT $1, 'debian 12', 'os-pkgs', 'pkg' || g, '1.0', 'CVE-2030-1', 'HIGH' FROM generate_series(1, 1001) g`, cur)
	if err != nil {
		t.Fatal(err)
	}
	insertVulns(t, pool, old, "CVE-2030-", 1, 1)
	insertVulns(t, pool, cur, "CVE-2031-", 1, 3)
	if _, err := pool.Exec(context.Background(), "ANALYZE"); err != nil {
		t.Fatal(err)
	}

	// A second service, last by name, has one critical finding of the ID: a
	// capped list must still show it, and the totals count both services.
	other := newService(t, pool, "zzz-last", "grp/zzz", false)
	zscan := newScan(t, pool, other, "image", "app", "main", now)
	addVulns(t, pool, zscan, vuln{id: "CVE-2030-1", pkg: "z", version: "1", target: "t", class: "os-pkgs", severity: "CRITICAL"})

	var wide searchBody
	getJSON(t, mux, "/api/v1/search/vulnerabilities?id=cve-2030-1", &wide)
	if len(wide.Items) != maxSearchResults || !wide.Truncated || wide.TotalFindings != 1002 || wide.TotalServices != 2 {
		t.Errorf("1002 matches: %d items, truncated %v, totals %d findings in %d services; want %d items, truncated, 1002 in 2",
			len(wide.Items), wide.Truncated, wide.TotalFindings, wide.TotalServices, maxSearchResults)
	}
	if len(wide.Items) > 0 && wide.Items[0].Severity != "CRITICAL" {
		t.Errorf("first listed finding is %s, want the critical one", wide.Items[0].Severity)
	}

	var narrow searchBody
	getJSON(t, mux, "/api/v1/search/vulnerabilities?id=CVE-2031-2", &narrow)
	if len(narrow.Items) != 1 || narrow.Truncated || narrow.TotalFindings != 1 || narrow.TotalServices != 1 {
		t.Errorf("one match: %d items, truncated %v, totals %d/%d", len(narrow.Items), narrow.Truncated, narrow.TotalFindings, narrow.TotalServices)
	}
}

func TestSearchListsOnlyCurrentScans(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	mux := newMux(pool, "")
	now := time.Now().UTC().Truncate(time.Second)
	live := newService(t, pool, "live", "grp/live", false)
	gone := newService(t, pool, "gone", "grp/gone", true)
	// live: the ID is in the older scan only; another ref has it current.
	oldMain := newScan(t, pool, live, "image", "app", "main", now.Add(-3*time.Hour))
	newMain := newScan(t, pool, live, "image", "app", "main", now.Add(-time.Hour))
	other := newScan(t, pool, live, "image", "app", "dev", now.Add(-2*time.Hour))
	archived := newScan(t, pool, gone, "image", "app", "main", now)
	addVulns(t, pool, oldMain, vuln{id: "CVE-2030-9", pkg: "a", version: "1", target: "t", class: "os-pkgs", severity: "HIGH"})
	addVulns(t, pool, newMain, vuln{id: "CVE-2030-8", pkg: "a", version: "1", target: "t", class: "os-pkgs", severity: "HIGH"})
	addVulns(t, pool, other, vuln{id: "CVE-2030-9", pkg: "a", version: "1", target: "t", class: "os-pkgs", severity: "HIGH"},
		vuln{id: "CVE-2030-9", pkg: "b", version: "1", target: "t", class: "os-pkgs", severity: "LOW", suppressed: true})
	addVulns(t, pool, archived, vuln{id: "CVE-2030-9", pkg: "a", version: "1", target: "t", class: "os-pkgs", severity: "HIGH"})

	var got struct {
		Items []struct {
			Ref    string `json:"ref"`
			ScanID int64  `json:"scan_id"`
		} `json:"items"`
		Truncated bool `json:"truncated"`
	}
	getJSON(t, mux, "/api/v1/search/vulnerabilities?id=CVE-2030-9", &got)
	if len(got.Items) != 1 || got.Items[0].Ref != "dev" || got.Items[0].ScanID != other || got.Truncated {
		t.Errorf("search = %+v, want only the current dev scan %d", got, other)
	}
}

func TestScanDiffIsBounded(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	mux := newMux(pool, "")
	svc := newService(t, pool, "churn", "grp/churn", false)
	now := time.Now().UTC().Truncate(time.Second)
	prev := newScan(t, pool, svc, "image", "app", "main", now.Add(-time.Hour))
	cur := newScan(t, pool, svc, "image", "app", "main", now)
	// 100 findings are in both scans, 3 only in the previous one and 600 only
	// in the current one.
	insertVulns(t, pool, prev, "CVE-2030-", 1, 103)
	insertVulns(t, pool, cur, "CVE-2030-", 4, 697)

	var d struct {
		New []struct {
			FirstSeenAt time.Time `json:"first_seen_at"`
		} `json:"new"`
		Fixed          []interface{} `json:"fixed"`
		UnchangedItems []struct {
			FirstSeenAt time.Time `json:"first_seen_at"`
		} `json:"unchanged_items"`
		Unchanged  int        `json:"unchanged"`
		NewCounts  countsJSON `json:"new_counts"`
		NewTotal   int        `json:"new_total"`
		FixedTotal int        `json:"fixed_total"`
		Truncated  bool       `json:"truncated"`
	}
	getJSON(t, mux, fmt.Sprintf("/api/v1/scans/%d/diff", cur), &d)
	if len(d.New) != maxDiffItems || d.NewTotal != 597 || len(d.Fixed) != 3 || d.FixedTotal != 3 || !d.Truncated {
		t.Errorf("diff: %d new listed of %d, %d fixed listed of %d, truncated %v", len(d.New), d.NewTotal, len(d.Fixed), d.FixedTotal, d.Truncated)
	}
	if d.Unchanged != 100 || len(d.UnchangedItems) != maxUnchangedItems {
		t.Errorf("unchanged = %d, %d listed; want 100, %d listed", d.Unchanged, len(d.UnchangedItems), maxUnchangedItems)
	}
	// The counts per severity cover the new findings that are not listed.
	if c := d.NewCounts; c.Critical+c.High+c.Medium+c.Low+c.Unknown != 597 {
		t.Errorf("new_counts = %+v, want 597 in all", c)
	}
	for _, v := range append(d.New, d.UnchangedItems...) {
		if v.FirstSeenAt.IsZero() {
			t.Fatal("a listed finding has no first-seen time")
		}
	}

	// A small diff is complete.
	var small struct {
		NewTotal  int  `json:"new_total"`
		Truncated bool `json:"truncated"`
	}
	getJSON(t, mux, fmt.Sprintf("/api/v1/scans/%d/diff", prev), &small)
	if small.NewTotal != 103 || small.Truncated {
		t.Errorf("first scan: new_total %d, truncated %v; want 103 and complete", small.NewTotal, small.Truncated)
	}
}

// statementLog records the statements a pool runs and on which connection.
type statementLog struct {
	mu   sync.Mutex
	list []statement
}

type statement struct {
	conn *pgx.Conn
	sql  string
}

func (l *statementLog) TraceQueryStart(_ context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.list = append(l.list, statement{conn, data.SQL})
	return context.Background()
}

func (l *statementLog) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// TestOverviewReadsOneSnapshot checks that every statement of an overview
// runs inside one read-only REPEATABLE READ transaction.
func TestOverviewReadsOneSnapshot(t *testing.T) {
	t.Parallel()
	base := storetest.New(t)
	svc := newService(t, base, "snap", "grp/snap", false)
	scan := newScan(t, base, svc, "image", "app", "main", time.Now().UTC().Add(-time.Hour))
	addVulns(t, base, scan, vuln{id: "CVE-2030-1", pkg: "a", version: "1", target: "t", class: "os-pkgs", severity: "HIGH"})

	tr := &statementLog{}
	cfg := base.Config().Copy()
	cfg.ConnConfig.Tracer = tr
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	mux := newMux(pool, "")
	get(t, mux, fmt.Sprintf("/api/v1/services/%d/overview", svc)) // opens the connection
	tr.mu.Lock()
	tr.list = nil
	tr.mu.Unlock()

	if code, body := get(t, mux, fmt.Sprintf("/api/v1/services/%d/overview", svc)); code != 200 {
		t.Fatalf("overview = %d %s", code, body)
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.list) < 6 {
		t.Fatalf("overview ran %d statements, want the begin and at least five reads: %v", len(tr.list), tr.list)
	}
	begin := strings.ToLower(tr.list[0].sql)
	if !strings.Contains(begin, "begin") || !strings.Contains(begin, "repeatable read") || !strings.Contains(begin, "read only") {
		t.Errorf("first statement = %q, want BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY", tr.list[0].sql)
	}
	reads := 0
	for _, s := range tr.list[1:] {
		if s.conn != tr.list[0].conn {
			t.Errorf("statement %q ran outside the transaction's connection", s.sql)
		}
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(s.sql)), "-- name:") {
			reads++
		}
	}
	if reads < 5 {
		t.Errorf("only %d reads were traced inside the transaction", reads)
	}
}
