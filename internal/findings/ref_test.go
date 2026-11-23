// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestPickRef(t *testing.T) {
	chosen := pgtype.Text{String: "release", Valid: true}
	none := pgtype.Text{}
	refs := []string{"feature/x", "main"}
	for _, tc := range []struct {
		name       string
		requested  string
		chosen     pgtype.Text
		refs       []string
		wantRef    string
		wantSource string
	}{
		{"requested beats chosen and latest", "main", chosen, refs, "main", "requested"},
		{"requested beats latest", "main", none, refs, "main", "requested"},
		{"requested without scans", "nope", none, nil, "nope", "requested"},
		{"chosen beats latest", "", chosen, refs, "release", "default"},
		{"chosen without any scans", "", chosen, nil, "release", "default"},
		{"chosen is not in the scanned refs", "", chosen, []string{"main"}, "release", "default"},
		{"latest", "", none, refs, "feature/x", "latest"},
		{"nothing at all", "", none, nil, "", "latest"},
	} {
		ref, source := pickRef(tc.requested, tc.chosen, tc.refs)
		if ref != tc.wantRef || source != tc.wantSource {
			t.Errorf("%s: pickRef = %q, %q; want %q, %q", tc.name, ref, source, tc.wantRef, tc.wantSource)
		}
	}
}
