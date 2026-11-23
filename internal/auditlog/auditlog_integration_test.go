// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package auditlog

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

type event struct {
	ID     int64          `json:"id"`
	At     string         `json:"at"`
	Actor  string         `json:"actor"`
	Action string         `json:"action"`
	Detail map[string]any `json:"detail"`
}

type page struct {
	Items      []event `json:"items"`
	NextBefore *int64  `json:"next_before"`
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// insert writes one audit row directly, with a fixed timestamp, and returns its id.
func insert(t *testing.T, pool *pgxpool.Pool, at time.Time, actor, action, detail string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO audit_log (at, actor, action, detail) VALUES ($1, $2, $3, $4::jsonb) RETURNING id`,
		at, actor, action, detail).Scan(&id)
	if err != nil {
		t.Fatalf("insert audit row: %v", err)
	}
	return id
}

// get calls the handler and returns the status, the raw body and, for 200
// responses, the decoded page.
func get(t *testing.T, pool *pgxpool.Pool, query string) (int, string, page) {
	t.Helper()
	h := New(pool, discard()).Handlers()["listAuditEvents"]
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/admin/audit?"+query, nil)
	h.ServeHTTP(rec, req)
	var p page
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("decode page: %v (body %s)", err, rec.Body.String())
		}
	}
	return rec.Code, strings.TrimSpace(rec.Body.String()), p
}

func ids(items []event) []int64 {
	out := make([]int64, len(items))
	for i, e := range items {
		out[i] = e.ID
	}
	return out
}

func equalIDs(t *testing.T, label string, got []event, want ...int64) {
	t.Helper()
	if want == nil {
		want = []int64{}
	}
	if g := ids(got); !reflect.DeepEqual(g, want) {
		t.Fatalf("%s: ids = %v, want %v", label, g, want)
	}
}

// seed writes four events in the order a1, b1, a2, c1, so their ids ascend
// in that order: a1 gitlab:alice login.succeeded; b1 local:bob
// login.failed; a2 gitlab:Alice login.failed, which has the earliest
// timestamp but a larger id than b1; c1 local:carol settings.changed.
func seed(t *testing.T, pool *pgxpool.Pool) (a1, b1, a2, c1 int64) {
	t.Helper()
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	a1 = insert(t, pool, base, "gitlab:alice", "login.succeeded", `{}`)
	b1 = insert(t, pool, base.Add(1*time.Second), "local:bob", "login.failed", `{"reason":"bad password"}`)
	a2 = insert(t, pool, base.Add(-1*time.Minute), "gitlab:Alice", "login.failed", `{"reason":"mfa"}`)
	c1 = insert(t, pool, base.Add(2*time.Second), "local:carol", "settings.changed", `{"changed":["gitlab_url"]}`)
	return a1, b1, a2, c1
}

func TestListNewestFirstByID(t *testing.T) {
	pool := storetest.New(t)
	a1, b1, a2, c1 := seed(t, pool)

	status, _, p := get(t, pool, "")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	equalIDs(t, "all", p.Items, c1, a2, b1, a1)
	if p.NextBefore != nil {
		t.Errorf("next_before = %d, want null", *p.NextBefore)
	}
	first := p.Items[0]
	if first.At != "2026-01-02T03:04:07Z" || first.Actor != "local:carol" ||
		first.Action != "settings.changed" {
		t.Errorf("first item = %+v", first)
	}
	if want := map[string]any{"changed": []any{"gitlab_url"}}; !reflect.DeepEqual(first.Detail, want) {
		t.Errorf("first detail = %v, want %v", first.Detail, want)
	}
}

func TestFilterAction(t *testing.T) {
	pool := storetest.New(t)
	_, b1, a2, _ := seed(t, pool)

	_, _, p := get(t, pool, "action=login.failed")
	equalIDs(t, "action", p.Items, a2, b1)

	// Exact match only: a prefix or a different case matches nothing.
	_, _, p = get(t, pool, "action=login")
	equalIDs(t, "prefix", p.Items)
	_, _, p = get(t, pool, "action=LOGIN.FAILED")
	equalIDs(t, "case", p.Items)
}

func TestFilterActorIsCaseInsensitiveSubstring(t *testing.T) {
	pool := storetest.New(t)
	a1, _, a2, _ := seed(t, pool)

	for _, q := range []string{"actor=alice", "actor=ALICE", "actor=Alice", "actor=lic"} {
		_, _, p := get(t, pool, q)
		equalIDs(t, q, p.Items, a2, a1)
	}
	_, _, p := get(t, pool, "actor=gitlab:")
	if len(p.Items) != 2 {
		t.Errorf("actor=gitlab: returned %d items, want 2", len(p.Items))
	}
	_, _, p = get(t, pool, "actor=dave")
	equalIDs(t, "no match", p.Items)
}

func TestFilterBefore(t *testing.T) {
	pool := storetest.New(t)
	a1, b1, a2, c1 := seed(t, pool)

	_, _, p := get(t, pool, "before="+itoa(c1))
	equalIDs(t, "before c1", p.Items, a2, b1, a1)
	_, _, p = get(t, pool, "before="+itoa(a2))
	equalIDs(t, "before a2", p.Items, b1, a1)
	_, _, p = get(t, pool, "before="+itoa(a1))
	equalIDs(t, "before a1", p.Items)
}

func TestFilterCombined(t *testing.T) {
	pool := storetest.New(t)
	_, b1, a2, c1 := seed(t, pool)

	_, _, p := get(t, pool, "action=login.failed&actor=LOCAL&before="+itoa(c1))
	equalIDs(t, "action+actor+before", p.Items, b1)

	_, _, p = get(t, pool, "action=login.failed&actor=alice&before="+itoa(a2))
	equalIDs(t, "no row satisfies all three", p.Items)

	_, _, p = get(t, pool, "action=login.failed&actor=alice")
	equalIDs(t, "action+actor", p.Items, a2)
}

func TestPagingBoundaries(t *testing.T) {
	pool := storetest.New(t)
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	x1 := insert(t, pool, base, "a", "x", `{}`)
	x2 := insert(t, pool, base, "a", "x", `{}`)
	x3 := insert(t, pool, base, "a", "x", `{}`)

	// limit+1 rows exist: the page is trimmed and points at its last item.
	_, _, p := get(t, pool, "limit=2")
	equalIDs(t, "limit=2", p.Items, x3, x2)
	if p.NextBefore == nil || *p.NextBefore != x2 {
		t.Fatalf("next_before = %v, want %d", p.NextBefore, x2)
	}

	// Following next_before returns the remainder with no further cursor.
	_, _, p = get(t, pool, "limit=2&before="+itoa(x2))
	equalIDs(t, "second page", p.Items, x1)
	if p.NextBefore != nil {
		t.Errorf("second page next_before = %d, want null", *p.NextBefore)
	}

	// Exactly limit rows: no cursor.
	_, _, p = get(t, pool, "limit=3")
	equalIDs(t, "limit=3", p.Items, x3, x2, x1)
	if p.NextBefore != nil {
		t.Errorf("exactly limit rows: next_before = %d, want null", *p.NextBefore)
	}

	// limit=1 pages one row at a time.
	_, _, p = get(t, pool, "limit=1")
	equalIDs(t, "limit=1", p.Items, x3)
	if p.NextBefore == nil || *p.NextBefore != x3 {
		t.Fatalf("limit=1 next_before = %v, want %d", p.NextBefore, x3)
	}
}

func TestDefaultLimitIsFifty(t *testing.T) {
	pool := storetest.New(t)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO audit_log (actor, action) SELECT 'bulk', 'bulk.event' FROM generate_series(1, 51)`); err != nil {
		t.Fatal(err)
	}
	var maxID, minID int64
	if err := pool.QueryRow(context.Background(),
		`SELECT max(id), min(id) FROM audit_log`).Scan(&maxID, &minID); err != nil {
		t.Fatal(err)
	}

	_, _, p := get(t, pool, "")
	if len(p.Items) != 50 {
		t.Fatalf("default page has %d items, want 50", len(p.Items))
	}
	if p.Items[0].ID != maxID || p.Items[49].ID != maxID-49 {
		t.Errorf("default page spans %d..%d, want %d..%d",
			p.Items[0].ID, p.Items[49].ID, maxID, maxID-49)
	}
	if p.NextBefore == nil || *p.NextBefore != maxID-49 {
		t.Fatalf("next_before = %v, want %d", p.NextBefore, maxID-49)
	}

	_, _, p = get(t, pool, "before="+itoa(maxID-49))
	equalIDs(t, "remainder", p.Items, minID)
	if p.NextBefore != nil {
		t.Errorf("remainder next_before = %d, want null", *p.NextBefore)
	}

	_, _, p = get(t, pool, "limit=200")
	if len(p.Items) != 51 || p.NextBefore != nil {
		t.Errorf("limit=200: %d items, next_before %v; want 51 and null", len(p.Items), p.NextBefore)
	}
}

func TestDetailRoundTrip(t *testing.T) {
	pool := storetest.New(t)
	insert(t, pool, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "local:bob", "ingest.rejected",
		`{"reason":"bad token","attempts":3,"tags":["a","b"],"meta":{"ok":true,"none":null}}`)

	_, _, p := get(t, pool, "")
	if len(p.Items) != 1 {
		t.Fatalf("items = %d", len(p.Items))
	}
	want := map[string]any{
		"reason":   "bad token",
		"attempts": float64(3),
		"tags":     []any{"a", "b"},
		"meta":     map[string]any{"ok": true, "none": nil},
	}
	if !reflect.DeepEqual(p.Items[0].Detail, want) {
		t.Fatalf("detail = %#v, want %#v", p.Items[0].Detail, want)
	}
}

func TestEmptyResult(t *testing.T) {
	pool := storetest.New(t)
	status, body, _ := get(t, pool, "action=none")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if body != `{"items":[],"next_before":null}` {
		t.Fatalf("body = %s", body)
	}
}

func TestExactTimestampFormat(t *testing.T) {
	pool := storetest.New(t)
	insert(t, pool, time.Date(2026, 6, 7, 8, 9, 10, 0, time.FixedZone("CEST", 2*3600)), "a", "x", `{}`)

	_, _, p := get(t, pool, "")
	if len(p.Items) != 1 || p.Items[0].At != "2026-06-07T06:09:10Z" {
		t.Fatalf("at = %+v, want 2026-06-07T06:09:10Z in UTC", p.Items)
	}
}

func TestDatabaseFailureIsInternalError(t *testing.T) {
	pool := storetest.New(t)
	seed(t, pool)
	pool.Close()

	status, body, _ := get(t, pool, "")
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, body %s", status, body)
	}
	want := `{"error":{"code":"internal_error","message":"Something went wrong. Try again later."}}`
	if body != want {
		t.Fatalf("body = %s, want %s", body, want)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
