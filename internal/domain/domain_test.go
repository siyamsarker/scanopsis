// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"
)

type finding struct {
	id, pkg, target string
	n               int // distinguishes duplicates
}

func key(f finding) Key { return Key{f.id, f.pkg, f.target} }

func TestDiff(t *testing.T) {
	a := finding{"CVE-1", "openssl", "alpine", 1}
	b := finding{"CVE-2", "openssl", "alpine", 2}
	c := finding{"CVE-3", "zlib", "alpine", 3}
	aOtherTarget := finding{"CVE-1", "openssl", "app/Gemfile.lock", 4}
	aDup := finding{"CVE-1", "openssl", "alpine", 5}

	tests := []struct {
		name          string
		prev, cur     []finding
		added, remove []finding
		unchanged     int
	}{
		{"both empty", nil, nil, nil, nil, 0},
		{"no previous: all new", nil, []finding{a, b}, []finding{a, b}, nil, 0},
		{"empty current: all fixed", []finding{a, b}, nil, nil, []finding{a, b}, 0},
		{"identical", []finding{a, b}, []finding{b, a}, nil, nil, 2},
		{"one new one fixed", []finding{a, b}, []finding{b, c}, []finding{c}, []finding{a}, 1},
		{"target is part of the key", []finding{a}, []finding{aOtherTarget}, []finding{aOtherTarget}, []finding{a}, 0},
		{"duplicates in current count once", []finding{b}, []finding{a, aDup, b}, []finding{a}, nil, 1},
		{"duplicates in previous count once", []finding{a, aDup, b}, []finding{c}, []finding{c}, []finding{a, b}, 0},
		{"duplicate on both sides is one unchanged", []finding{a, aDup}, []finding{aDup, a}, nil, nil, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			added, removed, unchanged := Diff(tc.prev, tc.cur, key)
			if !reflect.DeepEqual(added, tc.added) || !reflect.DeepEqual(removed, tc.remove) || unchanged != tc.unchanged {
				t.Errorf("Diff = %v, %v, %d; want %v, %v, %d", added, removed, unchanged, tc.added, tc.remove, tc.unchanged)
			}
		})
	}
}

// upgradeRules is testdata/upgrade-rules.json, which the web tests of the
// upgrade plan read too, so both sides are held to the same cases.
type upgradeRules struct {
	Compare  [][3]any    `json:"compare"`
	Highest  [][2]string `json:"highest"`
	Findings []struct {
		Pkg, Installed, Severity, Fixed string
		Suppressed                      bool
	} `json:"findings"`
	Plan []struct {
		Pkg, Installed string
		UpgradeTo      string `json:"upgrade_to"`
		Cleared, Risk  int
		Counts         struct{ Critical, High, Medium, Low, Unknown int }
	} `json:"plan"`
}

func loadUpgradeRules(t *testing.T) upgradeRules {
	t.Helper()
	b, err := os.ReadFile("testdata/upgrade-rules.json")
	if err != nil {
		t.Fatal(err)
	}
	var r upgradeRules
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range loadUpgradeRules(t).Compare {
		a, b, want := tc[0].(string), tc[1].(string), int(tc[2].(float64))
		got := CompareVersions(a, b)
		if sign(got) != want {
			t.Errorf("CompareVersions(%q, %q) = %d, want sign %d", a, b, got, want)
		}
		if back := CompareVersions(b, a); sign(back) != -want {
			t.Errorf("CompareVersions(%q, %q) = %d is not the mirror of %d", b, a, back, got)
		}
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

func TestHighestVersion(t *testing.T) {
	for _, tc := range loadUpgradeRules(t).Highest {
		if got := HighestVersion(tc[0]); got != tc[1] {
			t.Errorf("HighestVersion(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
}

// The upgrade plan of the shared cases, built as the overview builds it: the
// unsuppressed findings grouped per package, installed version and severity
// (FindingsPackageGroups). The packages with a fix come first, in the order
// and with the target, counts and risk of the web upgrade plan.
func TestTopPackagesMatchTheUpgradePlan(t *testing.T) {
	r := loadUpgradeRules(t)
	type key struct{ pkg, installed, severity string }
	idx := map[key]int{}
	var groups []PackageGroup
	for _, f := range r.Findings {
		if f.Suppressed {
			continue
		}
		k := key{f.Pkg, f.Installed, f.Severity}
		i, ok := idx[k]
		if !ok {
			i = len(groups)
			idx[k] = i
			groups = append(groups, PackageGroup{PkgName: f.Pkg, InstalledVersion: f.Installed, Severity: f.Severity})
		}
		g := &groups[i]
		g.Count++
		if f.Fixed != "" {
			g.Fixable++
			if !slices.Contains(g.FixedVersions, f.Fixed) {
				g.FixedVersions = append(g.FixedVersions, f.Fixed)
			}
		}
	}
	top := TopPackages(groups, 100)
	if len(top) < len(r.Plan) {
		t.Fatalf("%d packages, want at least %d", len(top), len(r.Plan))
	}
	for i, want := range r.Plan {
		p := top[i]
		c := p.FixableBySeverity
		got := fmt.Sprintf("%s@%s -> %s, %d cleared, %d points, %d/%d/%d/%d/%d", p.PkgName, p.InstalledVersion, p.FixVersion,
			p.Fixable, p.RiskCleared, c.Critical, c.High, c.Medium, c.Low, c.Unknown)
		w := want.Counts
		exp := fmt.Sprintf("%s@%s -> %s, %d cleared, %d points, %d/%d/%d/%d/%d", want.Pkg, want.Installed, want.UpgradeTo,
			want.Cleared, want.Risk, w.Critical, w.High, w.Medium, w.Low, w.Unknown)
		if got != exp {
			t.Errorf("package %d: %s\nwant %s", i+1, got, exp)
		}
	}
	for _, p := range top[len(r.Plan):] {
		if p.Fixable != 0 {
			t.Errorf("%s@%s has a fix but is not in the plan", p.PkgName, p.InstalledVersion)
		}
	}
}

func TestSeverityRank(t *testing.T) {
	for i, s := range []string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "UNKNOWN", "bogus"} {
		want := min(i, 4)
		if got := SeverityRank(s); got != want {
			t.Errorf("SeverityRank(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestSeverityCountsAdd(t *testing.T) {
	var c SeverityCounts
	for i, s := range []string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "UNKNOWN", "weird"} {
		c.Add(s, i+1)
	}
	want := SeverityCounts{Critical: 1, High: 2, Medium: 3, Low: 4, Unknown: 5 + 6}
	if c != want {
		t.Errorf("counts = %+v, want %+v", c, want)
	}
}

// group builds a PackageGroup; fixable of count findings report a fix.
func group(name, version, severity string, count, fixable int, fixed ...string) PackageGroup {
	return PackageGroup{PkgName: name, InstalledVersion: version, Severity: severity,
		Count: count, Fixable: fixable, FixedVersions: fixed}
}

func TestTopPackages(t *testing.T) {
	groups := []PackageGroup{
		// openssl 3.0.1: 3 findings, all fixable, fix 3.1.2 (highest of several, lists included).
		group("openssl", "3.0.1", "HIGH", 2, 2, "3.0.5", "3.0.9, 3.1.2", ""),
		group("openssl", "3.0.1", "CRITICAL", 1, 1, "3.0.10"),
		// same name, other version: separate entry, 3 findings, only MEDIUM/LOW, no fix.
		group("openssl", "1.1.1", "MEDIUM", 2, 0),
		group("openssl", "1.1.1", "LOW", 1, 0, ""),
		// zlib: more risk than openssl 3.0.1 (30 points), but the upgrade
		// clears only one of its three findings (10 points).
		group("zlib", "1.2.11", "CRITICAL", 3, 1, "1.2.12"),
		// busybox: 1 finding, no fix.
		group("busybox", "1.35", "UNKNOWN", 1, 0),
		// alpha: 1 fixable finding, 5 points.
		group("alpha", "0.1", "HIGH", 1, 1, "0.2"),
		// tie on everything but version.
		group("libx", "2", "LOW", 1, 0),
		group("libx", "1", "LOW", 1, 0),
	}
	got := TopPackages(groups, 10)
	want := []PackageImpact{
		{PkgName: "openssl", InstalledVersion: "3.0.1", FixVersion: "3.1.2", Findings: 3,
			BySeverity: SeverityCounts{Critical: 1, High: 2}, Fixable: 3,
			FixableBySeverity: SeverityCounts{Critical: 1, High: 2}, Risk: 20, RiskCleared: 20},
		{PkgName: "zlib", InstalledVersion: "1.2.11", FixVersion: "1.2.12", Findings: 3,
			BySeverity: SeverityCounts{Critical: 3}, Fixable: 1,
			FixableBySeverity: SeverityCounts{Critical: 1}, Risk: 30, RiskCleared: 10},
		{PkgName: "alpha", InstalledVersion: "0.1", FixVersion: "0.2", Findings: 1,
			BySeverity: SeverityCounts{High: 1}, Fixable: 1,
			FixableBySeverity: SeverityCounts{High: 1}, Risk: 5, RiskCleared: 5},
		{PkgName: "openssl", InstalledVersion: "1.1.1", Findings: 3,
			BySeverity: SeverityCounts{Medium: 2, Low: 1}, Risk: 7},
		{PkgName: "busybox", InstalledVersion: "1.35", Findings: 1,
			BySeverity: SeverityCounts{Unknown: 1}, Risk: 5},
		{PkgName: "libx", InstalledVersion: "1", Findings: 1, BySeverity: SeverityCounts{Low: 1}, Risk: 1},
		{PkgName: "libx", InstalledVersion: "2", Findings: 1, BySeverity: SeverityCounts{Low: 1}, Risk: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TopPackages =\n%+v\nwant\n%+v", got, want)
	}

	if top := TopPackages(groups, 2); !reflect.DeepEqual(top, want[:2]) {
		t.Errorf("limit 2 = %+v, want %+v", top, want[:2])
	}
	if none := TopPackages(nil, 10); len(none) != 0 {
		t.Errorf("no groups = %+v, want none", none)
	}
}

// The upgrade to the highest fixed version fixes exactly the findings that
// report a fixed version, whichever version each reports; the others stay.
// The counts of what it fixes are those the upgrade plan shows.
func TestTopPackagesCountsWhatTheUpgradeFixes(t *testing.T) {
	groups := []PackageGroup{
		group("libc-bin", "2.36-9+deb12u3", "HIGH", 3, 2, "2.36-9+deb12u7", "2.36-9+deb12u10"),
		group("libc-bin", "2.36-9+deb12u3", "MEDIUM", 3, 1, "2.36-9+deb12u4"),
		group("libc-bin", "2.36-9+deb12u3", "LOW", 2, 2, "2.36-9+deb12u4", "2.36-9+deb12u9"),
	}
	got := TopPackages(groups, 10)
	want := []PackageImpact{{
		PkgName: "libc-bin", InstalledVersion: "2.36-9+deb12u3", FixVersion: "2.36-9+deb12u10",
		Findings: 8, BySeverity: SeverityCounts{High: 3, Medium: 3, Low: 2},
		Fixable: 5, FixableBySeverity: SeverityCounts{High: 2, Medium: 1, Low: 2},
		Risk: 3*5 + 3*3 + 2, RiskCleared: 2*5 + 1*3 + 2,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TopPackages =\n%+v\nwant\n%+v", got, want)
	}
}

func TestTopPackagesFixableIsBounded(t *testing.T) {
	// A count that disagrees with the group size never clears more than it holds.
	got := TopPackages([]PackageGroup{group("a", "1", "HIGH", 2, 5, "2"), group("b", "1", "LOW", 1, -1)}, 10)
	if got[0].Fixable != 2 || got[0].RiskCleared != 10 || got[1].Fixable != 0 || got[1].RiskCleared != 0 {
		t.Errorf("TopPackages = %+v", got)
	}
}

func TestRisk(t *testing.T) {
	c := SeverityCounts{Critical: 2, High: 44, Medium: 58, Low: 31, Unknown: 3}
	if got := c.Risk(); got != 2*10+44*5+58*3+31+3*5 {
		t.Errorf("Risk = %d", got)
	}
	for sev, want := range map[string]int{"CRITICAL": 10, "HIGH": 5, "MEDIUM": 3, "LOW": 1, "UNKNOWN": 5, "bogus": 5} {
		if got := RiskWeight(sev); got != want {
			t.Errorf("RiskWeight(%s) = %d, want %d", sev, got, want)
		}
	}
}

func TestTopPackagesSeverityTieBreak(t *testing.T) {
	groups := []PackageGroup{
		// Equal risk points (10 each): more findings first, then the most
		// severe (high and unknown weigh the same), then the name.
		group("f", "1", "CRITICAL", 1, 0),
		group("d", "1", "UNKNOWN", 2, 0),
		group("e", "1", "HIGH", 2, 0),
		group("c", "1", "LOW", 10, 0),
		group("b", "1", "HIGH", 2, 0),
		group("a", "1", "LOW", 10, 0),
	}
	var names string
	for _, p := range TopPackages(groups, 10) {
		names += p.PkgName
	}
	if names != "acbedf" {
		t.Errorf("order = %q, want acbedf", names)
	}
}
