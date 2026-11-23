// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package domain

import "strings"

// CompareVersions orders two version strings naturally: it returns a negative
// number when a < b, zero when they are equal and a positive number when a > b.
//
// The comparison is ecosystem-neutral, so it can differ from the native
// ordering of a package manager (Debian "~" and revision rules, RPM carets,
// Maven qualifiers, ...). The rules are:
//   - an optional leading "N:" (digits) is an epoch and dominates the rest;
//   - a leading "v" before a digit is ignored;
//   - the rest is split into runs of digits and runs of letters; every other
//     character only separates runs;
//   - digit runs compare by numeric value (without overflow), letter runs
//     case-insensitively, and a digit run is greater than a letter run;
//   - when one side runs out, the other is greater unless its remainder
//     starts with letters (a pre-release such as "rc1") or consists only of
//     zeros, so 1.0.0-rc1 < 1.0.0 = 1.0 < 1.0.1.
func CompareVersions(a, b string) int {
	ea, ra := splitEpoch(a)
	eb, rb := splitEpoch(b)
	if c := compareNumeric(ea, eb); c != 0 {
		return c
	}
	ta, tb := tokens(ra), tokens(rb)
	for i := 0; i < len(ta) || i < len(tb); i++ {
		switch {
		case i >= len(tb):
			if c := remainder(ta[i]); c != 0 {
				return c
			}
		case i >= len(ta):
			if c := remainder(tb[i]); c != 0 {
				return -c
			}
		default:
			if c := compareToken(ta[i], tb[i]); c != 0 {
				return c
			}
		}
	}
	return 0
}

// HighestVersion returns the greatest version in list, which may hold several
// versions separated by commas (Trivy joins the fixed versions of different
// release branches as "1.2.3, 1.3.1"). On equal versions the first wins. It
// returns "" when list holds no version.
func HighestVersion(list string) string {
	best := ""
	for _, v := range strings.Split(list, ",") {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if best == "" || CompareVersions(v, best) > 0 {
			best = v
		}
	}
	return best
}

func splitEpoch(v string) (epoch, rest string) {
	v = strings.TrimSpace(v)
	if i := strings.IndexByte(v, ':'); i > 0 && isDigits(v[:i]) {
		return v[:i], v[i+1:]
	}
	return "0", v
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return s != ""
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// tokens splits v into digit runs and letter runs.
func tokens(v string) []string {
	if len(v) > 1 && (v[0] == 'v' || v[0] == 'V') && isDigit(v[1]) {
		v = v[1:]
	}
	var out []string
	for i := 0; i < len(v); {
		var pred func(byte) bool
		switch c := v[i]; {
		case isDigit(c):
			pred = isDigit
		case isLetter(c):
			pred = isLetter
		default:
			i++
			continue
		}
		j := i + 1
		for j < len(v) && pred(v[j]) {
			j++
		}
		out = append(out, v[i:j])
		i = j
	}
	return out
}

// remainder says how a version that still has token t left compares with one
// that has ended: 1 greater, -1 smaller (pre-release), 0 no difference.
func remainder(t string) int {
	if isLetter(t[0]) {
		return -1
	}
	if strings.Trim(t, "0") == "" {
		return 0
	}
	return 1
}

func compareToken(a, b string) int {
	da, db := isDigit(a[0]), isDigit(b[0])
	switch {
	case da && db:
		return compareNumeric(a, b)
	case da:
		return 1
	case db:
		return -1
	}
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}

// compareNumeric compares two digit strings by value.
func compareNumeric(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}
