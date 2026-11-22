// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"reflect"
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

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.2.10", "1.2.9", 1},
		{"1.2.9", "1.2.10", -1},
		{"1.2.3", "1.2.3", 0},
		{"1.0.0-rc1", "1.0.0", -1},
		{"1.0.0", "1.0.0-rc1", 1},
		{"1.0.0-rc2", "1.0.0-rc10", -1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0.0-RC1", "1.0.0-rc1", 0},
		{"1.0", "1.0.0", 0},
		{"1.0", "1.0.1", -1},
		{"1.0.1", "1.0", 1},
		{"1.0", "1.0.a", 1}, // letters after the numbers: pre-release of 1.0
		{"1.0.1", "1.0.a", 1},
		{"2:1.0", "1:9.9", 1},
		{"1:1.0", "9.9", 1},
		{"2:1.0", "2:1.0", 0},
		{"2:1.0", "2:1.1", -1},
		{"0:1.0", "1.0", 0},
		{"a:1", "1", -1}, // not an epoch: a letter run is smaller than a digit run
		{"v1.2.3", "1.2.3", 0},
		{"v1.2.10", "v1.2.9", 1},
		{"1.2.3-4ubuntu1", "1.2.3-4", -1}, // known difference: Debian ranks the suffixed revision higher
		{"1.2.3-10", "1.2.3-9", 1},
		{"1.0~rc1", "1.0", -1},
		{"0001.2", "1.2", 0},
		{"99999999999999999999999.1", "99999999999999999999998.9", 1},
		{"", "", 0},
		{"", "0", 0},
		{"", "1", -1},
		{" 1.2 ", "1.2", 0},
	}
	for _, tc := range tests {
		got := CompareVersions(tc.a, tc.b)
		if sign(got) != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want sign %d", tc.a, tc.b, got, tc.want)
		}
		if back := CompareVersions(tc.b, tc.a); sign(back) != -tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d is not the mirror of %d", tc.b, tc.a, back, got)
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
	tests := []struct{ in, want string }{
		{"", ""},
		{" , ", ""},
		{"1.2.3", "1.2.3"},
		{"1.2.3, 1.3.1", "1.3.1"},
		{"1.3.1, 1.2.3", "1.3.1"},
		{"1.2.9,1.2.10", "1.2.10"},
		{"2.0.0-rc1, 1.9.9", "2.0.0-rc1"},
		{"2.0.0-rc1, 2.0.0", "2.0.0"},
		{"2:1.0, 1:5.0, 3.0", "2:1.0"},
		{"1.0, 1.0.0", "1.0"},
	}
	for _, tc := range tests {
		if got := HighestVersion(tc.in); got != tc.want {
			t.Errorf("HighestVersion(%q) = %q, want %q", tc.in, got, tc.want)
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
			BySeverity: SeverityCounts{Critical: 1, High: 2}, Fixable: 3, Risk: 20, RiskCleared: 20},
		{PkgName: "zlib", InstalledVersion: "1.2.11", FixVersion: "1.2.12", Findings: 3,
			BySeverity: SeverityCounts{Critical: 3}, Fixable: 1, Risk: 30, RiskCleared: 10},
		{PkgName: "alpha", InstalledVersion: "0.1", FixVersion: "0.2", Findings: 1,
			BySeverity: SeverityCounts{High: 1}, Fixable: 1, Risk: 5, RiskCleared: 5},
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
