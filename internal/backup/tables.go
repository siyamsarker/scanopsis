// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"context"
	"fmt"
	"sort"

	"github.com/siyamsarker/scanopsis/internal/store"
)

// Excluded lists the tables a backup does not contain: sessions end with a
// restore anyway, and the migration history is rebuilt from the schema
// version in the manifest.
var Excluded = []string{"goose_db_version", "sessions"}

// Tables returns the tables a backup holds: every ordinary table in the
// public schema except Excluded, ordered so that a table a foreign key refers
// to comes before the tables that refer to it. Columns are in the order of the
// table; dropped and generated columns are left out. Within these rules
// tables are sorted by name, so the result is the same every time.
func Tables(ctx context.Context, db store.DBTX) ([]TableSpec, error) {
	rows, err := db.Query(ctx, `
SELECT c.relname, a.attname
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_attribute a ON a.attrelid = c.oid
WHERE n.nspname = 'public' AND c.relkind = 'r' AND NOT c.relispartition
  AND a.attnum > 0 AND NOT a.attisdropped AND a.attgenerated = ''
  AND c.relname <> ALL ($1)
ORDER BY c.relname, a.attnum`, Excluded)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	byName := map[string]*TableSpec{}
	var names []string
	for rows.Next() {
		var table, col string
		if err := rows.Scan(&table, &col); err != nil {
			rows.Close()
			return nil, fmt.Errorf("list tables: %w", err)
		}
		t := byName[table]
		if t == nil {
			t = &TableSpec{Name: table}
			byName[table] = t
			names = append(names, table)
		}
		t.Columns = append(t.Columns, col)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}

	// refs[a] holds the tables a refers to through a foreign key.
	refs := map[string][]string{}
	fk, err := db.Query(ctx, `
SELECT r.relname, p.relname
FROM pg_constraint k
JOIN pg_class r ON r.oid = k.conrelid
JOIN pg_class p ON p.oid = k.confrelid
JOIN pg_namespace n ON n.oid = r.relnamespace
WHERE k.contype = 'f' AND n.nspname = 'public' AND r.oid <> p.oid`)
	if err != nil {
		return nil, fmt.Errorf("list foreign keys: %w", err)
	}
	for fk.Next() {
		var from, to string
		if err := fk.Scan(&from, &to); err != nil {
			fk.Close()
			return nil, fmt.Errorf("list foreign keys: %w", err)
		}
		if byName[from] != nil && byName[to] != nil {
			refs[from] = append(refs[from], to)
		}
	}
	if err := fk.Err(); err != nil {
		return nil, fmt.Errorf("list foreign keys: %w", err)
	}
	return orderTables(names, byName, refs)
}

// orderTables sorts names so that referenced tables come first, and reports a
// cycle instead of looping.
func orderTables(names []string, byName map[string]*TableSpec, refs map[string][]string) ([]TableSpec, error) {
	sort.Strings(names)
	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	out := make([]TableSpec, 0, len(names))
	var visit func(name string) error
	visit = func(name string) error {
		switch state[name] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("tables refer to each other in a cycle at %s", name)
		}
		state[name] = visiting
		deps := append([]string(nil), refs[name]...)
		sort.Strings(deps)
		for _, d := range deps {
			if err := visit(d); err != nil {
				return err
			}
		}
		state[name] = done
		out = append(out, *byName[name])
		return nil
	}
	for _, n := range names {
		if err := visit(n); err != nil {
			return nil, err
		}
	}
	return out, nil
}
