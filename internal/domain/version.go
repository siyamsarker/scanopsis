// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"slices"
	"strings"
)

// CompareVersions orders two version strings naturally: it returns a negative
// number when a < b, zero when they are equal and a positive number when a > b.
//
// The comparison is ecosystem-neutral, so some forms in some ecosystems are
// not ordered as their package manager orders them, among them:
//   - Ubuntu revisions: 1.2.3-4ubuntu1 is below 1.2.3-4;
//   - "+dfsg" and the like in the middle: 1.2+dfsg-1 is below 1.2-1;
//   - PEP 440 post-releases: 1.0.post1 is below 1.0;
//   - Alpine patch levels: 9.3_p2-r0 is below 9.3-r0;
//   - Go pseudo-versions of a later tag: v1.2.4-0.20230101000000-abcdef is
//     above v1.2.4;
//   - semver numeric pre-releases: 1.0.0-1 is above 1.0.0;
//   - semver pre-release identifiers, where a number ranks below letters:
//     1.0.0-alpha.1 is above 1.0.0-alpha.beta;
//   - RPM carets and Maven qualifiers.
//
// The rules are:
//   - an optional leading "N:" (digits) is an epoch and dominates the rest;
//   - a leading "v" before a digit is ignored;
//   - the rest is split into runs of digits and runs of letters; every other
//     character only separates runs, except that a run after "~" sorts
//     before anything else and a run after "+" after the end, as dpkg orders
//     them (1.0-1~deb12u1 < 1.0-1 < 1.0-1+deb12u1);
//   - digit runs compare by numeric value (without overflow), letter runs
//     case-insensitively, and a digit run is greater than a letter run;
//   - when one side runs out, the other is greater unless its remainder
//     starts with letters (a pre-release such as "rc1") or consists only of
//     zeros, so 1.0.0-rc1 < 1.0.0 = 1.0 < 1.0.1.
//
// The web's compareVersions (web/src/routes/findings/model.ts) follows the
// same rules; testdata/upgrade-rules.json holds the cases both must pass.
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
		case ta[i].tilde != tb[i].tilde:
			if ta[i].tilde {
				return -1
			}
			return 1
		default:
			if c := compareToken(ta[i].text, tb[i].text); c != 0 {
				return c
			}
		}
	}
	return 0
}

// HighestVersion returns the greatest version in list, which may hold several
// versions separated by commas (Trivy joins the fixed versions of different
// release branches as "1.2.3, 1.3.1"). It returns "" when list holds no
// version. See Highest for the order of the versions.
func HighestVersion(list string) string {
	return Highest(strings.Split(list, ","))
}

// Highest returns the greatest of versions ("" when there is none, blanks
// ignored). The comparison is not transitive in every case ("1.0+b" is above
// "1.0", which equals "1.0.0", which is above "1.0+b"), so the result could
// depend on the order the versions come in. It never does: the distinct
// versions are sorted byte by byte first and then kept while Higher says so.
func Highest(versions []string) string {
	vs := make([]string, 0, len(versions))
	for _, v := range versions {
		if v = strings.TrimSpace(v); v != "" {
			vs = append(vs, v)
		}
	}
	slices.Sort(vs)
	vs = slices.Compact(vs)
	best := ""
	for _, v := range vs {
		if best == "" || Higher(v, best) {
			best = v
		}
	}
	return best
}

// Higher reports whether v should replace best as the highest version: v is
// greater, or it compares equal but is written differently and sorts first
// byte by byte ("1.0" before "1.0.0", "1.2" before "v1.2").
func Higher(v, best string) bool {
	c := CompareVersions(v, best)
	return c > 0 || c == 0 && v < best
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

// token is a run of digits or of letters. tilde and plus record a "~" or a
// "+" among the characters that separate it from the run before.
type token struct {
	text        string
	tilde, plus bool
}

// tokens splits v into digit runs and letter runs.
func tokens(v string) []token {
	if len(v) > 1 && (v[0] == 'v' || v[0] == 'V') && isDigit(v[1]) {
		v = v[1:]
	}
	var out []token
	var tilde, plus bool
	for i := 0; i < len(v); {
		var pred func(byte) bool
		switch c := v[i]; {
		case isDigit(c):
			pred = isDigit
		case isLetter(c):
			pred = isLetter
		default:
			tilde = tilde || c == '~'
			plus = plus || c == '+'
			i++
			continue
		}
		j := i + 1
		for j < len(v) && pred(v[j]) {
			j++
		}
		out = append(out, token{text: v[i:j], tilde: tilde, plus: plus})
		tilde, plus = false, false
		i = j
	}
	return out
}

// remainder says how a version that still has token t left compares with one
// that has ended: 1 greater, -1 smaller (pre-release), 0 no difference.
func remainder(t token) int {
	switch {
	case t.tilde:
		return -1
	case t.plus:
		return 1
	case isLetter(t.text[0]):
		return -1
	case strings.Trim(t.text, "0") == "":
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
