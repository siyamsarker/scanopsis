// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package domain

import "sort"

// Severities in descending order of importance.
var severities = [...]string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "UNKNOWN"}

// SeverityRank returns 0 for CRITICAL up to 4 for UNKNOWN; any other value
// ranks as UNKNOWN.
func SeverityRank(s string) int {
	for i, name := range severities {
		if s == name {
			return i
		}
	}
	return len(severities) - 1
}

// SeverityCounts counts findings per severity.
type SeverityCounts struct {
	Critical, High, Medium, Low, Unknown int
}

// Add counts n findings of the given severity.
func (c *SeverityCounts) Add(severity string, n int) {
	switch SeverityRank(severity) {
	case 0:
		c.Critical += n
	case 1:
		c.High += n
	case 2:
		c.Medium += n
	case 3:
		c.Low += n
	default:
		c.Unknown += n
	}
}

func (c SeverityCounts) topRank() int {
	switch {
	case c.Critical > 0:
		return 0
	case c.High > 0:
		return 1
	case c.Medium > 0:
		return 2
	case c.Low > 0:
		return 3
	}
	return 4
}

// PackageGroup is the number of unsuppressed findings of one severity in one
// package at one installed version, with the fixed versions those findings
// report (empty strings and duplicates are fine; each entry may itself be a
// comma-separated list). Findings without a fix contribute no version.
type PackageGroup struct {
	PkgName          string
	InstalledVersion string
	Severity         string
	Count            int
	FixedVersions    []string
}

// PackageImpact is what one installed package costs: its findings per
// severity and the version to upgrade to.
type PackageImpact struct {
	PkgName          string
	InstalledVersion string
	// FixVersion is the highest fixed version among the findings; upgrading
	// to it clears the fixable ones. Empty when none is fixable. See
	// CompareVersions for the ordering's limits.
	FixVersion string
	Findings   int
	BySeverity SeverityCounts
}

// TopPackages merges groups by (package, installed version) and returns at
// most n packages, most findings first, then the most severe, then by name
// and version so the order is stable.
func TopPackages(groups []PackageGroup, n int) []PackageImpact {
	type pkgKey struct{ name, version string }
	idx := make(map[pkgKey]int)
	var out []PackageImpact
	for _, g := range groups {
		k := pkgKey{g.PkgName, g.InstalledVersion}
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, PackageImpact{PkgName: g.PkgName, InstalledVersion: g.InstalledVersion})
		}
		p := &out[i]
		p.Findings += g.Count
		p.BySeverity.Add(g.Severity, g.Count)
		for _, list := range g.FixedVersions {
			if v := HighestVersion(list); v != "" && (p.FixVersion == "" || CompareVersions(v, p.FixVersion) > 0) {
				p.FixVersion = v
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Findings != b.Findings {
			return a.Findings > b.Findings
		}
		if ra, rb := a.BySeverity.topRank(), b.BySeverity.topRank(); ra != rb {
			return ra < rb
		}
		if a.PkgName != b.PkgName {
			return a.PkgName < b.PkgName
		}
		return a.InstalledVersion < b.InstalledVersion
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
