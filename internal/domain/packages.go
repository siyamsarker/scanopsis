// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"sort"
	"strings"
)

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

// RiskWeights are the points one open finding of each severity adds to a risk
// score, in severity order (critical, high, medium, low, unknown). They are
// the weights OWASP Dependency-Track uses for its inherited risk score; an
// unrated finding counts as much as a high one, as it is not assumed harmless.
var RiskWeights = [...]int{10, 5, 3, 1, 5}

// RiskWeight returns the risk points of one finding of the given severity.
func RiskWeight(severity string) int { return RiskWeights[SeverityRank(severity)] }

// Risk returns the risk points of the counts: critical×10 + high×5 +
// medium×3 + low×1 + unknown×5.
func (c SeverityCounts) Risk() int {
	return c.Critical*RiskWeights[0] + c.High*RiskWeights[1] + c.Medium*RiskWeights[2] +
		c.Low*RiskWeights[3] + c.Unknown*RiskWeights[4]
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
// package at one installed version, how many of them report a fixed version,
// and the fixed versions they report (empty strings and duplicates are fine;
// each entry may itself be a comma-separated list). Findings without a fix
// contribute no version.
type PackageGroup struct {
	PkgName          string
	InstalledVersion string
	Severity         string
	Count            int
	Fixable          int
	FixedVersions    []string
}

// PackageImpact is what one installed package costs: its findings per
// severity and the version to upgrade to.
type PackageImpact struct {
	PkgName          string
	InstalledVersion string
	// FixVersion is the highest fixed version among the findings; upgrading
	// to it clears the fixable ones. Empty when none is fixable. See
	// CompareVersions for the ordering's limits and Highest for the choice.
	FixVersion string
	// Findings and BySeverity count all the package's findings.
	Findings   int
	BySeverity SeverityCounts
	// Fixable counts the findings the upgrade to FixVersion fixes: those
	// that report a fixed version, as FixVersion is the highest of them.
	// FixableBySeverity counts them per severity. The upgrade plan of the
	// findings page counts the findings it loads by the same rule.
	Fixable           int
	FixableBySeverity SeverityCounts
	// Risk is the risk points of BySeverity; RiskCleared those of
	// FixableBySeverity, which the upgrade to FixVersion removes.
	Risk        int
	RiskCleared int
}

// TopPackages merges groups by (package, installed version) and returns at
// most n packages in the order of upgradeFirst. Packages without a fix come
// after every package with one.
func TopPackages(groups []PackageGroup, n int) []PackageImpact {
	type pkgKey struct{ name, version string }
	idx := make(map[pkgKey]int)
	var out []PackageImpact
	var fixed [][]string // per package, every fixed version reported
	for _, g := range groups {
		k := pkgKey{g.PkgName, g.InstalledVersion}
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, PackageImpact{PkgName: g.PkgName, InstalledVersion: g.InstalledVersion})
			fixed = append(fixed, nil)
		}
		p := &out[i]
		p.Findings += g.Count
		p.BySeverity.Add(g.Severity, g.Count)
		fixable := min(max(g.Fixable, 0), g.Count)
		p.Fixable += fixable
		p.FixableBySeverity.Add(g.Severity, fixable)
		for _, list := range g.FixedVersions {
			fixed[i] = append(fixed[i], strings.Split(list, ",")...)
		}
	}
	for i := range out {
		out[i].FixVersion = Highest(fixed[i])
		out[i].Risk = out[i].BySeverity.Risk()
		out[i].RiskCleared = out[i].FixableBySeverity.Risk()
	}
	sort.Slice(out, func(i, j int) bool { return upgradeFirst(out[i], out[j]) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// upgradeFirst orders packages as the upgrade plan of the findings page does
// (upgradeGroups in web/src/routes/findings/model.ts), by what the upgrade
// fixes: the most risk points cleared first, then the most findings fixed,
// then the most severe finding fixed, then by name and installed version
// (byte order). Packages without a fix, which the plan does not list, are
// ordered among themselves by their open findings instead: the most risk,
// the most findings and the most severe first.
func upgradeFirst(a, b PackageImpact) bool {
	if a.RiskCleared != b.RiskCleared {
		return a.RiskCleared > b.RiskCleared
	}
	if a.Fixable != b.Fixable {
		return a.Fixable > b.Fixable
	}
	if ra, rb := a.FixableBySeverity.topRank(), b.FixableBySeverity.topRank(); ra != rb {
		return ra < rb
	}
	if a.Fixable == 0 { // neither has a fix
		if a.Risk != b.Risk {
			return a.Risk > b.Risk
		}
		if a.Findings != b.Findings {
			return a.Findings > b.Findings
		}
		if ra, rb := a.BySeverity.topRank(), b.BySeverity.topRank(); ra != rb {
			return ra < rb
		}
	}
	if a.PkgName != b.PkgName {
		return a.PkgName < b.PkgName
	}
	return a.InstalledVersion < b.InstalledVersion
}
