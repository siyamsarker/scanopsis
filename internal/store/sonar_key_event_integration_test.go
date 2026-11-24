// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package store_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/siyamsarker/scanopsis/internal/store"
	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

// namedQuery returns the SQL of a named query in a query file, with its
// named parameters as sqlc numbers them.
func namedQuery(t *testing.T, file, name string, params ...string) string {
	t.Helper()
	raw, err := os.ReadFile("queries/" + file)
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(raw), "-- name: "+name+" ")
	if !ok {
		t.Fatalf("no query %s in %s", name, file)
	}
	_, rest, _ = strings.Cut(rest, "\n")
	sql, _, _ := strings.Cut(rest, ";")
	for i, p := range params {
		sql = strings.ReplaceAll(sql, "@"+p, "$"+string(rune('1'+i)))
	}
	return sql
}

// LastSonarKeyEvent runs on every upload of a project whose declared key is
// ignored, under the project's locks, so it must not read the audit log by
// time: each action is read from audit_log_action_at_idx, newest first.
// It still returns the newest event of either action for the service.
func TestLastSonarKeyEventUsesActionIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := storetest.New(t)
	for _, q := range []string{
		`INSERT INTO audit_log (actor, action, detail, at)
		 SELECT 'local:root', 'login.succeeded', jsonb_build_object('n', g), now() - g * interval '1 second'
		 FROM generate_series(1, 200000) g`,
		`INSERT INTO audit_log (actor, action, detail, at)
		 SELECT 'local:root', 'service.updated', jsonb_build_object('id', g % 500), now() - g * interval '1 minute'
		 FROM generate_series(1, 2000) g`,
		`INSERT INTO audit_log (actor, action, detail, at)
		 SELECT 'ci:p', 'ingest.sonar_key_ignored', jsonb_build_object('id', g % 500, 'reason', 'service_has_other_key', 'sonar_project_key', 'k' || g), now() - g * interval '1 minute' - interval '30 seconds'
		 FROM generate_series(1, 2000) g`,
		`ANALYZE audit_log`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	sql := namedQuery(t, "sonar.sql", "LastSonarKeyEvent", "service_id")
	rows, err := pool.Query(ctx, "EXPLAIN "+sql, "7")
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Logf("plan:\n%s", plan.String())
	if n := strings.Count(plan.String(), "audit_log_action_at_idx"); n != 2 || strings.Contains(plan.String(), "audit_log_at_idx") ||
		strings.Contains(plan.String(), "Seq Scan") {
		t.Errorf("want both actions read from audit_log_action_at_idx and nothing else:\n%s", plan.String())
	}

	// The newest event of either action: service 7 changed at minute 7 and
	// had an ignored key at minute 7.5 (older), and so on.
	q := store.New(pool)
	got, err := q.LastSonarKeyEvent(ctx, "7")
	if err != nil {
		t.Fatal(err)
	}
	if got.Action != "service.updated" || got.Reason != "" || got.SonarProjectKey != "" {
		t.Errorf("service 7: %+v", got)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO audit_log (actor, action, detail)
		VALUES ('ci:p', 'ingest.sonar_key_ignored', '{"id": 7, "reason": "key_linked_to_other_service", "sonar_project_key": "x:y"}')`); err != nil {
		t.Fatal(err)
	}
	if got, err = q.LastSonarKeyEvent(ctx, "7"); err != nil || got.Action != "ingest.sonar_key_ignored" ||
		got.Reason != "key_linked_to_other_service" || got.SonarProjectKey != "x:y" {
		t.Errorf("service 7 after an ignored key: %+v %v", got, err)
	}
	if _, err := q.LastSonarKeyEvent(ctx, "100000"); err == nil {
		t.Error("an event for a service without events")
	}
}
