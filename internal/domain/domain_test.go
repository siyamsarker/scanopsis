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

func TestTopPackages(t *testing.T) {
	groups := []PackageGroup{
		// openssl 3.0.1: 3 findings, fix 3.1.2 (highest of several, lists included).
		{"openssl", "3.0.1", "HIGH", 2, []string{"3.0.5", "3.0.9, 3.1.2", ""}},
		{"openssl", "3.0.1", "CRITICAL", 1, []string{"3.0.10"}},
		// same name, other version: separate entry, 3 findings, only MEDIUM/LOW.
		{"openssl", "1.1.1", "MEDIUM", 2, nil},
		{"openssl", "1.1.1", "LOW", 1, []string{""}},
		// zlib: 3 findings, as severe as openssl 3.0.1 (both CRITICAL): name decides.
		{"zlib", "1.2.11", "CRITICAL", 3, []string{"1.2.12"}},
		// busybox: 1 finding, no fix.
		{"busybox", "1.35", "UNKNOWN", 1, nil},
		// alpha: 1 finding, same count as busybox, more severe.
		{"alpha", "0.1", "HIGH", 1, []string{"0.2"}},
		// tie on everything but version.
		{"libx", "2", "LOW", 1, nil},
		{"libx", "1", "LOW", 1, nil},
	}
	got := TopPackages(groups, 10)
	want := []PackageImpact{
		{"openssl", "3.0.1", "3.1.2", 3, SeverityCounts{Critical: 1, High: 2}},
		{"zlib", "1.2.11", "1.2.12", 3, SeverityCounts{Critical: 3}},
		{"openssl", "1.1.1", "", 3, SeverityCounts{Medium: 2, Low: 1}},
		{"alpha", "0.1", "0.2", 1, SeverityCounts{High: 1}},
		{"libx", "1", "", 1, SeverityCounts{Low: 1}},
		{"libx", "2", "", 1, SeverityCounts{Low: 1}},
		{"busybox", "1.35", "", 1, SeverityCounts{Unknown: 1}},
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

func TestTopPackagesSeverityTieBreak(t *testing.T) {
	groups := []PackageGroup{
		{"a", "1", "LOW", 2, nil},
		{"b", "1", "MEDIUM", 2, nil},
		{"c", "1", "LOW", 2, nil},
		{"d", "1", "UNKNOWN", 2, nil},
		{"e", "1", "HIGH", 2, nil},
		{"f", "1", "CRITICAL", 2, nil},
	}
	var names string
	for _, p := range TopPackages(groups, 10) {
		names += p.PkgName
	}
	if names != "febacd" {
		t.Errorf("order = %q, want febacd", names)
	}
}
