// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package findings

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

func setProtected(t testing.TB, pool *pgxpool.Pool, scan int64, protected bool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE scans SET ref_protected = $2 WHERE id = $1`, scan, protected); err != nil {
		t.Fatal(err)
	}
}

type scopeBody struct {
	Scope string `json:"scope"`
	Items []struct {
		Service struct {
			Name string `json:"name"`
		} `json:"service"`
		Ref string `json:"ref"`
	} `json:"items"`
	Truncated     bool  `json:"truncated"`
	TotalFindings int64 `json:"total_findings"`
	TotalServices int64 `json:"total_services"`
}

func (b scopeBody) hits() []string {
	var out []string
	for _, i := range b.Items {
		out = append(out, i.Service.Name+"@"+i.Ref)
	}
	slices.Sort(out)
	return out
}

// TestSearchScope checks which refs a search covers. Every scan below holds
// the same vulnerability; the scope decides which of them are listed.
func TestSearchScope(t *testing.T) {
	pool := storetest.New(t)
	ctx := context.Background()
	mux := newMux(pool, "")
	base := time.Now().UTC().Truncate(time.Second)
	const id = "CVE-2030-0001"
	finding := vuln{id: id, pkg: "p", version: "1", target: "t", class: "os-pkgs", severity: "HIGH"}

	// scan adds a scan holding the finding; protected is the flag of the ref.
	scan := func(svc int64, ref string, ago time.Duration, protected bool) int64 {
		s := newScan(t, pool, svc, "image", "app", ref, base.Add(-ago))
		setProtected(t, pool, s, protected)
		addVulns(t, pool, s, finding)
		return s
	}

	// latest: no chosen branch, so the dashboards show the ref of the latest
	// scan, "feature". "release" is protected; "old" and "main" are not.
	latest := newService(t, pool, "latest", "g/latest", false)
	scan(latest, "main", 5*time.Hour, false)
	scan(latest, "old", 4*time.Hour, false)
	scan(latest, "release", 3*time.Hour, true)
	scan(latest, "feature", time.Hour, false)

	// chosen: the administrator chose "main", although "newer" was scanned later.
	chosen := newService(t, pool, "chosen", "g/chosen", false)
	if _, err := pool.Exec(ctx, `UPDATE services SET default_ref = 'main' WHERE id = $1`, chosen); err != nil {
		t.Fatal(err)
	}
	scan(chosen, "main", 5*time.Hour, false)
	scan(chosen, "newer", time.Hour, false)

	// chosen-empty: the chosen branch has no scans, so only protected refs count.
	empty := newService(t, pool, "chosen-empty", "g/chosen-empty", false)
	if _, err := pool.Exec(ctx, `UPDATE services SET default_ref = 'nothing-yet' WHERE id = $1`, empty); err != nil {
		t.Fatal(err)
	}
	scan(empty, "other", time.Hour, false)
	scan(empty, "tag-v1", 2*time.Hour, true)

	// unprotected-now: the ref was protected once, but its latest scan is not,
	// and a newer scan of another ref is the dashboard ref.
	flip := newService(t, pool, "flip", "g/flip", false)
	scan(flip, "was-protected", 5*time.Hour, true)
	scan(flip, "was-protected", 4*time.Hour, false)
	scan(flip, "shown", time.Hour, false)

	// Superseded protected scan: only the latest scan of a ref counts.
	super := newService(t, pool, "super", "g/super", false)
	scan(super, "main", 5*time.Hour, true)
	scan(super, "main", time.Hour, true)

	// An archived service is never listed.
	arch := newService(t, pool, "archived", "g/archived", true)
	scan(arch, "main", time.Hour, true)

	// A service that does not hold the vulnerability.
	other := newService(t, pool, "clean", "g/clean", false)
	s := newScan(t, pool, other, "image", "app", "main", base)
	addVulns(t, pool, s, vuln{id: "CVE-2030-9999", pkg: "p", version: "1", target: "t", class: "os-pkgs", severity: "LOW"})

	cases := []struct {
		query    string
		scope    string
		want     []string
		services int64
	}{
		// The default is the dashboard scope.
		{"", "dashboard", []string{"chosen@main", "chosen-empty@tag-v1", "flip@shown", "latest@feature", "latest@release", "super@main"}, 5},
		{"&scope=dashboard", "dashboard", []string{"chosen@main", "chosen-empty@tag-v1", "flip@shown", "latest@feature", "latest@release", "super@main"}, 5},
		{"&scope=all", "all", []string{
			"chosen@main", "chosen@newer", "chosen-empty@other", "chosen-empty@tag-v1", "flip@shown", "flip@was-protected",
			"latest@feature", "latest@main", "latest@old", "latest@release", "super@main"}, 5},
	}
	for _, c := range cases {
		slices.Sort(c.want)
		var got scopeBody
		getJSON(t, mux, "/api/v1/search/vulnerabilities?id="+id+c.query, &got)
		if !slices.Equal(got.hits(), c.want) || got.Scope != c.scope || got.TotalFindings != int64(len(c.want)) ||
			got.TotalServices != c.services || got.Truncated {
			t.Errorf("scope %q: scope %q, hits %v, totals %d in %d, truncated %v\nwant hits %v, scope %q, %d in %d",
				c.query, got.Scope, got.hits(), got.TotalFindings, got.TotalServices, got.Truncated,
				c.want, c.scope, len(c.want), c.services)
		}
	}
	wantError(t, mux, "/api/v1/search/vulnerabilities?id="+id+"&scope=everything", 400, "invalid_parameter")
	wantError(t, mux, "/api/v1/search/vulnerabilities?id="+id+"&scope=ALL", 400, "invalid_parameter")

	// Choosing another dashboard branch changes what the default scope finds.
	if _, err := pool.Exec(ctx, `UPDATE services SET default_ref = 'old' WHERE id = $1`, latest); err != nil {
		t.Fatal(err)
	}
	var got scopeBody
	getJSON(t, mux, "/api/v1/search/vulnerabilities?id="+id, &got)
	if !slices.Contains(got.hits(), "latest@old") || slices.Contains(got.hits(), "latest@feature") || !slices.Contains(got.hits(), "latest@release") {
		t.Errorf("after choosing the branch 'old', hits = %v", got.hits())
	}
}
