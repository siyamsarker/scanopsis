// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"slices"
	"strings"
	"testing"
)

func TestParseUp(t *testing.T) {
	t.Parallel()
	src := `-- Copyright header

-- What the migration does.

-- +goose NO TRANSACTION

-- +goose Up
-- A comment before the first statement is dropped.
CREATE TABLE a (
    id bigint, -- a trailing comment; with a semicolon
    note text
); -- the statement ends here

-- +goose StatementBegin
DO $$
BEGIN
    PERFORM 1;
END $$;
-- +goose StatementEnd
CREATE INDEX CONCURRENTLY IF NOT EXISTS a_note_idx
    ON a (note);
DROP INDEX CONCURRENTLY IF EXISTS a_old_idx;

-- +goose Down
DROP TABLE a;
-- +goose StatementBegin
DO $$ BEGIN PERFORM 2; END $$;
-- +goose StatementEnd
`
	got, err := parseUp(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"CREATE TABLE a (\n    id bigint, -- a trailing comment; with a semicolon\n    note text\n); -- the statement ends here",
		"DO $$\nBEGIN\n    PERFORM 1;\nEND $$;",
		"CREATE INDEX CONCURRENTLY IF NOT EXISTS a_note_idx\n    ON a (note);",
		"DROP INDEX CONCURRENTLY IF EXISTS a_old_idx;",
	}
	if !slices.Equal(got, want) {
		t.Errorf("parseUp =\n%q\nwant\n%q", got, want)
	}
}

func TestParseUpErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, src, want string }{
		{"no up", "CREATE TABLE a (id int);\n", "-- +goose Up"},
		{"down before up", "-- +goose Down\nDROP TABLE a;\n", "-- +goose Up"},
		{"two ups", "-- +goose Up\n-- +goose Up\n", "duplicate"},
		{"unknown annotation", "-- +goose Up\n-- +goose ENVSUB ON\n", "unsupported annotation"},
		{"indented annotation", "-- +goose Up\n  -- +goose StatementBegin\n", "leading whitespace"},
		{"statement without end", "-- +goose Up\n-- +goose StatementBegin\nDO $$ BEGIN END $$;\n", "StatementEnd"},
		{"end without begin", "-- +goose Up\n-- +goose StatementEnd\n", "StatementBegin"},
		{"missing semicolon", "-- +goose Up\nCREATE TABLE a (id int)\n", "semicolon"},
		{"missing semicolon before down", "-- +goose Up\nCREATE TABLE a (id int)\n-- +goose Down\nDROP TABLE a;\n", "semicolon"},
	} {
		if _, err := parseUp(tc.src); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want one containing %q", tc.name, err, tc.want)
		}
	}
}

func TestWithoutConcurrently(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"CREATE INDEX CONCURRENTLY IF NOT EXISTS x ON t (a);", "CREATE INDEX IF NOT EXISTS x ON t (a);"},
		{"create unique index\n    concurrently x on t (a);", "create unique index x on t (a);"},
		{"DROP INDEX CONCURRENTLY IF EXISTS x;", "DROP INDEX IF EXISTS x;"},
		{"CREATE INDEX x ON t (a);", "CREATE INDEX x ON t (a);"},
		{"CREATE INDEX concurrently_idx ON t (a);", "CREATE INDEX concurrently_idx ON t (a);"},
		{"REINDEX INDEX CONCURRENTLY x;", "REINDEX INDEX CONCURRENTLY x;"},
		{"DO $$ BEGIN EXECUTE 'CREATE INDEX CONCURRENTLY x ON t (a)'; END $$;", "DO $$ BEGIN EXECUTE 'CREATE INDEX CONCURRENTLY x ON t (a)'; END $$;"},
		{"ALTER TABLE t ADD COLUMN concurrently int;", "ALTER TABLE t ADD COLUMN concurrently int;"},
	} {
		if got := withoutConcurrently(tc.in); got != tc.want {
			t.Errorf("withoutConcurrently(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestEmbeddedMigrations parses every embedded migration: versions ascend
// from 1 without gaps, no statement carries an annotation or a statement of
// the Down part, and the files that build indexes concurrently keep every
// statement of their Up part.
func TestEmbeddedMigrations(t *testing.T) {
	t.Parallel()
	ms, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 {
		t.Fatal("no embedded migrations")
	}
	for i, m := range ms {
		if m.version != int64(i+1) {
			t.Errorf("migration %s has version %d, want %d", m.name, m.version, i+1)
		}
		if len(m.up) == 0 {
			t.Errorf("migration %s has no statements", m.name)
		}
		for _, s := range m.up {
			if strings.Contains(s, "+goose") || !strings.HasSuffix(s, ";") {
				t.Errorf("migration %s: statement %q", m.name, s)
			}
			if strings.HasPrefix(s, "DROP TABLE") {
				t.Errorf("migration %s: statement of the Down part %q", m.name, s)
			}
		}
	}
	if got := LatestVersion(); got != ms[len(ms)-1].version {
		t.Errorf("LatestVersion() = %d, want %d", got, ms[len(ms)-1].version)
	}
	counts := map[int64]int{7: 11, 10: 2}
	for v, want := range counts {
		if got := len(ms[v-1].up); got != want {
			t.Errorf("migration %s: %d statements, want %d", ms[v-1].name, got, want)
		}
		for _, s := range ms[v-1].up {
			if strings.HasPrefix(s, "CREATE INDEX") && !strings.Contains(s, "CONCURRENTLY") {
				t.Errorf("migration %s: %q is not built concurrently", ms[v-1].name, s)
			}
			if w := withoutConcurrently(s); strings.HasPrefix(s, "CREATE INDEX") || strings.HasPrefix(s, "DROP INDEX") {
				if strings.Contains(w, "CONCURRENTLY") {
					t.Errorf("migration %s: CONCURRENTLY left in %q", ms[v-1].name, w)
				}
			} else if w != s {
				t.Errorf("migration %s: %q changed to %q", ms[v-1].name, s, w)
			}
		}
	}
}
