// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func parseFixture(t testing.TB, name string) *Report {
	t.Helper()
	rep, err := Parse(bytes.NewReader(readFixture(t, name)))
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return rep
}

// expected holds the numbers a Trivy report implies, counted with a plain
// walk over generic JSON, independently of Parse.
type expected struct {
	sev                                  map[string]int
	fixable, secrets, suppressed         int
	vulnRows, secretRows, suppressedRows int
}

func countIndependently(t testing.TB, raw []byte) expected {
	t.Helper()
	var doc struct {
		Results []map[string]any
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	e := expected{sev: map[string]int{}}
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	list := func(m map[string]any, k string) []any { l, _ := m[k].([]any); return l }
	for _, res := range doc.Results {
		for _, x := range list(res, "Vulnerabilities") {
			v := x.(map[string]any)
			e.vulnRows++
			e.sev[str(v, "Severity")]++
			if str(v, "FixedVersion") != "" {
				e.fixable++
			}
		}
		e.secrets += len(list(res, "Secrets"))
		e.secretRows += len(list(res, "Secrets"))
		for _, x := range list(res, "ExperimentalModifiedFindings") {
			m := x.(map[string]any)
			switch str(m, "Type") {
			case "vulnerability", "secret":
				e.suppressed++
				e.suppressedRows++
			}
		}
	}
	return e
}

var (
	tableTotal      = regexp.MustCompile(`^Total: (\d+) \(UNKNOWN: (\d+), LOW: (\d+), MEDIUM: (\d+), HIGH: (\d+), CRITICAL: (\d+)\)`)
	tableSuppressed = regexp.MustCompile(`^Suppressed Vulnerabilities \(Total: (\d+)\)`)
)

// tableTotals sums the Total lines that "trivy convert --format table" prints
// (see "Test data" in docs/development.md): severity counts and suppressed
// findings.
func tableTotals(t testing.TB, name string) (sev map[string]int, suppressed int) {
	t.Helper()
	sev = map[string]int{}
	for _, line := range strings.Split(string(readFixture(t, name)), "\n") {
		if m := tableTotal.FindStringSubmatch(line); m != nil {
			for i, k := range []string{"UNKNOWN", "LOW", "MEDIUM", "HIGH", "CRITICAL"} {
				n, _ := strconv.Atoi(m[2+i])
				sev[k] += n
			}
		} else if m := tableSuppressed.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			suppressed += n
		}
	}
	return sev, suppressed
}

func TestGoldenFixtures(t *testing.T) {
	tests := []struct {
		file, totals string
		want         Counts
		vulns        int
		secrets      int
		artifact     string
		created      string
	}{
		{
			file: "image-full.json", totals: "image-full.totals.txt",
			want:  Counts{Critical: 2, High: 44, Medium: 58, Low: 31, Unknown: 3, Fixable: 66},
			vulns: 138, artifact: "python:3.9-slim-buster", created: "2026-11-19T10:41:23.914578+06:00",
		},
		{
			file: "image-suppressed.json", totals: "image-suppressed.totals.txt",
			want:  Counts{Critical: 1, High: 43, Medium: 57, Low: 31, Unknown: 3, Fixable: 64, Suppressed: 3},
			vulns: 138, artifact: "python:3.9-slim-buster",
		},
		{
			file: "secret-fs.json", totals: "secret-fs.totals.txt",
			want:  Counts{Secrets: 2},
			vulns: 0, secrets: 2, artifact: ".",
		},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			raw := readFixture(t, tc.file)
			rep := parseFixture(t, tc.file)
			if rep.Counts != tc.want {
				t.Errorf("counts = %+v, want %+v", rep.Counts, tc.want)
			}
			if len(rep.Vulns) != tc.vulns || len(rep.Secrets) != tc.secrets+0 && tc.secrets != 0 {
				t.Errorf("rows: %d vulnerabilities, %d secrets; want %d, %d", len(rep.Vulns), len(rep.Secrets), tc.vulns, tc.secrets)
			}
			if rep.SchemaVersion != 2 || rep.TrivyVersion != "0.76.2" || rep.ArtifactName != tc.artifact {
				t.Errorf("header = %d %q %q", rep.SchemaVersion, rep.TrivyVersion, rep.ArtifactName)
			}
			if tc.created != "" {
				want, _ := time.Parse(time.RFC3339Nano, tc.created)
				if !rep.CreatedAt.Equal(want) {
					t.Errorf("CreatedAt = %v, want %v", rep.CreatedAt, want)
				}
			}

			// Independent counter over the raw JSON.
			e := countIndependently(t, raw)
			got := map[string]int{
				SevCritical: int(rep.Counts.Critical), SevHigh: int(rep.Counts.High), SevMedium: int(rep.Counts.Medium),
				SevLow: int(rep.Counts.Low), SevUnknown: int(rep.Counts.Unknown),
			}
			// Suppressed findings are listed apart from Vulnerabilities in the report, so
			// the walk's severity counts are exactly the unsuppressed ones.
			for k, n := range got {
				if e.sev[k] != n {
					t.Errorf("%s: parser %d, independent count %d", k, n, e.sev[k])
				}
			}
			if int(rep.Counts.Fixable) != e.fixable || int(rep.Counts.Secrets) != e.secrets || int(rep.Counts.Suppressed) != e.suppressed {
				t.Errorf("fixable/secrets/suppressed: parser %d/%d/%d, independent %d/%d/%d",
					rep.Counts.Fixable, rep.Counts.Secrets, rep.Counts.Suppressed, e.fixable, e.secrets, e.suppressed)
			}
			if len(rep.Vulns) != e.vulnRows+countModified(t, raw, "vulnerability") || len(rep.Secrets) != e.secretRows+countModified(t, raw, "secret") {
				t.Errorf("row counts differ from the report")
			}

			// Cross-check against "trivy convert --format table" of the same file.
			tsev, tsupp := tableTotals(t, tc.totals)
			for k, n := range got {
				if tc.file != "secret-fs.json" && tsev[k] != n {
					t.Errorf("%s: parser %d, trivy table %d", k, n, tsev[k])
				}
			}
			if tc.file != "secret-fs.json" && tsupp != int(rep.Counts.Suppressed) {
				t.Errorf("suppressed: parser %d, trivy table %d", rep.Counts.Suppressed, tsupp)
			}
			if tc.file == "secret-fs.json" && tsev[SevCritical] != int(rep.Counts.Secrets) {
				t.Errorf("secrets: parser %d, trivy table %d", rep.Counts.Secrets, tsev[SevCritical])
			}
		})
	}
}

func countModified(t testing.TB, raw []byte, typ string) int {
	t.Helper()
	var doc struct {
		Results []struct {
			ExperimentalModifiedFindings []struct{ Type string }
		}
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range doc.Results {
		for _, m := range r.ExperimentalModifiedFindings {
			if m.Type == typ {
				n++
			}
		}
	}
	return n
}

func TestGoldenRows(t *testing.T) {
	rep := parseFixture(t, "image-full.json")
	find := func(id, pkg string) Vuln {
		t.Helper()
		for _, v := range rep.Vulns {
			if v.VulnID == id && v.PkgName == pkg {
				return v
			}
		}
		t.Fatalf("no row %s %s", id, pkg)
		return Vuln{}
	}
	os := find("CVE-2021-37600", "bsdutils")
	wantOS := Vuln{
		Target: "python:3.9-slim-buster (debian 10.13)", Class: "os-pkgs", PkgType: "debian", PkgName: "bsdutils",
		InstalledVersion: "1:2.33.1-0.1", FixedVersion: "2.33.1-0.1+deb10u1", VulnID: "CVE-2021-37600",
		Severity: SevMedium, Status: "fixed",
		Title:      "util-linux: integer overflow can lead to buffer overflow in get_sem_elements() in sys-utils/ipcutils.c",
		PrimaryURL: "https://avd.aquasec.com/nvd/cve-2021-37600",
	}
	if os != wantOS {
		t.Errorf("os row = %+v\nwant   %+v", os, wantOS)
	}
	py := find("CVE-2023-5752", "pip")
	wantPy := Vuln{
		Target: "Python", Class: "lang-pkgs", PkgType: "python-pkg", PkgName: "pip",
		PkgPath:          "usr/local/lib/python3.9/site-packages/pip-23.0.1.dist-info/METADATA",
		InstalledVersion: "23.0.1", FixedVersion: "23.3", VulnID: "CVE-2023-5752", Severity: SevMedium, Status: "fixed",
		Title:      "pip: Mercurial configuration injectable in repo revision when installing via pip",
		PrimaryURL: "https://avd.aquasec.com/nvd/cve-2023-5752",
	}
	if py != wantPy {
		t.Errorf("python row = %+v\nwant     %+v", py, wantPy)
	}
	// An unfixed finding keeps an empty fixed version and its status.
	un := find("CVE-2019-8457", "libdb5.3")
	if un.FixedVersion != "" || un.Status != "will_not_fix" || un.Severity != SevCritical || un.Suppressed {
		t.Errorf("unfixed row = %+v", un)
	}
}

func TestGoldenSuppressedRows(t *testing.T) {
	rep := parseFixture(t, "image-suppressed.json")
	var got []string
	for _, v := range rep.Vulns {
		if v.Suppressed {
			got = append(got, v.VulnID+"|"+v.PkgName+"|"+v.Severity+"|"+v.Target+"|"+v.Suppression)
		}
	}
	want := []string{
		"CVE-2019-8457|libdb5.3|CRITICAL|python:3.9-slim-buster (debian 10.13)|ignored (.trivyignore)",
		"CVE-2023-5752|pip|MEDIUM|Python|ignored (.trivyignore)",
		"CVE-2022-40897|setuptools|HIGH|Python|ignored (.trivyignore)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("suppressed rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// A suppressed finding is not in the unsuppressed counts: the ignored
	// CRITICAL is gone from the critical count (1, not 2).
	if rep.Counts.Critical != 1 {
		t.Errorf("critical = %d", rep.Counts.Critical)
	}
}

func TestGoldenSecretRows(t *testing.T) {
	rep := parseFixture(t, "secret-fs.json")
	want := []Secret{
		{Target: "config.env", RuleID: "aws-access-key-id", Category: "AWS", Severity: SevCritical, Title: "AWS Access Key ID", StartLine: 1, EndLine: 1},
		{Target: "config.env", RuleID: "aws-secret-access-key", Category: "AWS", Severity: SevCritical, Title: "AWS Secret Access Key", StartLine: 2, EndLine: 2},
	}
	if len(rep.Secrets) != 2 || rep.Secrets[0] != want[0] || rep.Secrets[1] != want[1] {
		t.Errorf("secrets = %+v", rep.Secrets)
	}
}

// The matched text and the code excerpt must not survive parsing, whatever
// the report holds.
func TestParseNeverKeepsSecretText(t *testing.T) {
	const marker = "SUPER-SECRET-MARKER"
	doc := `{"SchemaVersion":2,"ArtifactName":"x","Results":[{"Target":"f","Class":"secret",
	  "Secrets":[{"RuleID":"r","Category":"c","Severity":"HIGH","Title":"t","StartLine":3,"EndLine":4,
	  "Match":"` + marker + `","Code":{"Lines":[{"Content":"` + marker + `"}]}}],
	  "ExperimentalModifiedFindings":[{"Type":"secret","Status":"ignored","Source":".trivyignore",
	  "Finding":{"RuleID":"r2","Severity":"LOW","Match":"` + marker + `"}}]}]}`
	rep, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(secretStrings(rep), " "); strings.Contains(got, marker) {
		t.Fatalf("secret text retained: %s", got)
	}
	if len(rep.Secrets) != 2 || rep.Counts.Secrets != 1 || rep.Counts.Suppressed != 1 || !rep.Secrets[1].Suppressed {
		t.Errorf("secrets = %+v counts = %+v", rep.Secrets, rep.Counts)
	}
}

func secretStrings(rep *Report) []string {
	var out []string
	for _, s := range rep.Secrets {
		out = append(out, s.Target, s.RuleID, s.Category, s.Severity, s.Title)
	}
	return out
}

func TestParseErrors(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	tests := []struct {
		name string
		in   string
		want error
	}{
		{"empty", ``, ErrInvalidJSON},
		{"whitespace", "  \n", ErrInvalidJSON},
		{"truncated", `{"SchemaVersion":2,"Results":[`, ErrInvalidJSON},
		{"garbage", `not json`, ErrInvalidJSON},
		{"trailing data", `{"SchemaVersion":2,"ArtifactName":"a"} {}`, ErrInvalidJSON},
		{"trailing garbage", `{"SchemaVersion":2,"ArtifactName":"a"} x`, ErrInvalidJSON},
		{"syntax error after wrong version", `{"SchemaVersion":3,"Results":[}`, ErrInvalidJSON},
		{"array", `[]`, ErrNotTrivy},
		{"null", `null`, ErrNotTrivy},
		{"number", `42`, ErrNotTrivy},
		{"object without schema", `{}`, ErrNotTrivy},
		{"other tool", `{"results":[],"tool":"x"}`, ErrNotTrivy},
		{"schema version 1", `{"SchemaVersion":1,"ArtifactName":"a","Results":[]}`, ErrNotTrivy},
		{"schema version 3", `{"SchemaVersion":3,"ArtifactName":"a","Results":[]}`, ErrNotTrivy},
		{"schema version 0", `{"SchemaVersion":0,"ArtifactName":"a"}`, ErrNotTrivy},
		{"schema version string", `{"SchemaVersion":"2","ArtifactName":"a"}`, ErrNotTrivy},
		{"schema version fraction", `{"SchemaVersion":2.5,"ArtifactName":"a"}`, ErrNotTrivy},
		{"neither results nor artifact", `{"SchemaVersion":2}`, ErrNotTrivy},
		{"results wrong type", `{"SchemaVersion":2,"Results":"x"}`, ErrNotTrivy},
		{"vulnerability wrong type", `{"SchemaVersion":2,"Results":[{"Vulnerabilities":[{"VulnerabilityID":5}]}]}`, ErrNotTrivy},
		{"line number overflow", `{"SchemaVersion":2,"Results":[{"Secrets":[{"StartLine":99999999999}]}]}`, ErrNotTrivy},
		{"huge string is fine but unterminated", `{"SchemaVersion":2,"ArtifactName":"` + big, ErrInvalidJSON},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep, err := Parse(strings.NewReader(tc.in))
			if !errors.Is(err, tc.want) || rep != nil {
				t.Fatalf("Parse = %v, %v; want error %v", rep, err, tc.want)
			}
		})
	}
}

func TestParseReaderErrorPassesThrough(t *testing.T) {
	boom := errors.New("boom")
	_, err := Parse(errReader{boom})
	if !errors.Is(err, boom) || errors.Is(err, ErrInvalidJSON) || errors.Is(err, ErrNotTrivy) {
		t.Fatalf("err = %v", err)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestParseTolerance(t *testing.T) {
	t.Run("clean scan has no Results", func(t *testing.T) {
		rep, err := Parse(strings.NewReader(`{"SchemaVersion":2,"ArtifactName":"app:1","Trivy":{"Version":"0.50.0"}}`))
		if err != nil || rep.ArtifactName != "app:1" || len(rep.Vulns) != 0 || rep.Counts != (Counts{}) {
			t.Fatalf("%+v %v", rep, err)
		}
	})
	t.Run("null and empty lists", func(t *testing.T) {
		rep, err := Parse(strings.NewReader(`{"SchemaVersion":2,"Results":[],"ArtifactName":null}`))
		if err != nil || len(rep.Vulns) != 0 {
			t.Fatalf("%+v %v", rep, err)
		}
		rep, err = Parse(strings.NewReader(`{"SchemaVersion":2,"Results":[{"Target":"t","Vulnerabilities":null,"Secrets":null}]}`))
		if err != nil || len(rep.Vulns) != 0 || len(rep.Secrets) != 0 {
			t.Fatalf("%+v %v", rep, err)
		}
	})
	t.Run("only the four always-present fields", func(t *testing.T) {
		rep, err := Parse(strings.NewReader(`{"SchemaVersion":2,"Results":[{"Vulnerabilities":[
		  {"VulnerabilityID":"CVE-1","PkgName":"p","InstalledVersion":"1","Severity":"HIGH"}]}]}`))
		if err != nil {
			t.Fatal(err)
		}
		want := Vuln{PkgName: "p", InstalledVersion: "1", VulnID: "CVE-1", Severity: SevHigh}
		if len(rep.Vulns) != 1 || rep.Vulns[0] != want || rep.Counts != (Counts{High: 1}) || rep.TrivyVersion != "" {
			t.Fatalf("%+v counts %+v", rep.Vulns, rep.Counts)
		}
		if !rep.CreatedAt.IsZero() {
			t.Errorf("CreatedAt = %v", rep.CreatedAt)
		}
	})
	t.Run("unknown fields and nested values are ignored", func(t *testing.T) {
		rep, err := Parse(strings.NewReader(`{"SchemaVersion":2,"Future":{"a":[1,2,{"b":null}]},"ArtifactName":"a",
		  "Results":[{"Target":"t","New":[1],"Vulnerabilities":[{"VulnerabilityID":"CVE-1","PkgName":"p",
		  "InstalledVersion":"1","Severity":"LOW","CVSS":{"nvd":{"V3Score":9.8}},"X":true}]}]}`))
		if err != nil || rep.Counts.Low != 1 {
			t.Fatalf("%+v %v", rep, err)
		}
	})
	t.Run("severity", func(t *testing.T) {
		in := `{"SchemaVersion":2,"Results":[{"Vulnerabilities":[
		 {"Severity":"CRITICAL"},{"Severity":"HIGH"},{"Severity":"MEDIUM"},{"Severity":"LOW"},{"Severity":"UNKNOWN"},
		 {"Severity":"high"},{"Severity":"SEVERE"},{"Severity":""},{}]}]}`
		rep, err := Parse(strings.NewReader(in))
		if err != nil {
			t.Fatal(err)
		}
		want := Counts{Critical: 1, High: 1, Medium: 1, Low: 1, Unknown: 5}
		if rep.Counts != want {
			t.Fatalf("counts = %+v, want %+v", rep.Counts, want)
		}
		for _, v := range rep.Vulns {
			if v.Severity != SevCritical && v.Severity != SevHigh && v.Severity != SevMedium && v.Severity != SevLow && v.Severity != SevUnknown {
				t.Errorf("severity %q", v.Severity)
			}
		}
	})
	t.Run("fixable counts any non-empty fixed version", func(t *testing.T) {
		rep, err := Parse(strings.NewReader(`{"SchemaVersion":2,"Results":[{"Vulnerabilities":[
		 {"Severity":"LOW","FixedVersion":"1.2, 1.3"},{"Severity":"LOW","FixedVersion":""},{"Severity":"LOW"},
		 {"Severity":"LOW","FixedVersion":null}]}]}`))
		if err != nil || rep.Counts.Fixable != 1 || rep.Counts.Low != 4 {
			t.Fatalf("%+v %v", rep.Counts, err)
		}
		if rep.Vulns[0].FixedVersion != "1.2, 1.3" {
			t.Errorf("fixed = %q", rep.Vulns[0].FixedVersion)
		}
	})
	t.Run("a blank fixed version is no fix, and spaces around one are dropped", func(t *testing.T) {
		rep, err := Parse(strings.NewReader(`{"SchemaVersion":2,"Results":[{"Vulnerabilities":[
		 {"Severity":"LOW","FixedVersion":"  "},{"Severity":"LOW","FixedVersion":" 1.2 "}]}]}`))
		if err != nil || rep.Counts.Fixable != 1 {
			t.Fatalf("%+v %v", rep.Counts, err)
		}
		if got := []string{rep.Vulns[0].FixedVersion, rep.Vulns[1].FixedVersion}; got[0] != "" || got[1] != "1.2" {
			t.Errorf("fixed versions %q", got)
		}
	})
	t.Run("duplicates are kept", func(t *testing.T) {
		rep, err := Parse(strings.NewReader(`{"SchemaVersion":2,"Results":[
		 {"Target":"a","Vulnerabilities":[{"VulnerabilityID":"CVE-1","Severity":"HIGH"}]},
		 {"Target":"b","Vulnerabilities":[{"VulnerabilityID":"CVE-1","Severity":"HIGH"},{"VulnerabilityID":"CVE-1","Severity":"HIGH"}]}]}`))
		if err != nil || len(rep.Vulns) != 3 || rep.Counts.High != 3 {
			t.Fatalf("%+v %v", rep, err)
		}
	})
	t.Run("modified findings of other types are ignored", func(t *testing.T) {
		rep, err := Parse(strings.NewReader(`{"SchemaVersion":2,"Results":[{"ExperimentalModifiedFindings":[
		 {"Type":"misconfiguration","Status":"ignored","Finding":{"Severity":"HIGH"}},
		 {"Type":"license","Status":"ignored","Finding":{}}]}]}`))
		if err != nil || len(rep.Vulns) != 0 || rep.Counts != (Counts{}) {
			t.Fatalf("%+v %v", rep, err)
		}
	})
	t.Run("suppression text", func(t *testing.T) {
		tests := []struct{ status, statement, source, want string }{
			{"ignored", "", ".trivyignore", "ignored (.trivyignore)"},
			{"not_affected", "component_not_present", "vex.json", "not_affected: component_not_present (vex.json)"},
			{"ignored", "accepted risk", "", "ignored: accepted risk"},
			{"", "", "", "suppressed"},
			{"  ignored ", "  why ", " src ", "ignored: why (src)"},
		}
		for _, tc := range tests {
			got := suppression(&wireModified{Status: tc.status, Statement: tc.statement, Source: tc.source})
			if got != tc.want {
				t.Errorf("suppression(%q,%q,%q) = %q, want %q", tc.status, tc.statement, tc.source, got, tc.want)
			}
		}
		long := suppression(&wireModified{Status: "ignored", Statement: strings.Repeat("é", 1000), Source: "s"})
		if len(long) > maxSuppressionLen || !strings.HasPrefix(long, "ignored: é") {
			t.Errorf("long suppression len %d", len(long))
		}
	})
	t.Run("created at", func(t *testing.T) {
		tests := map[string]bool{
			`"2026-01-02T03:04:05Z"`:             true,
			`"2026-01-02T03:04:05.123456+06:00"`: true,
			`"yesterday"`:                        false,
			`""`:                                 false,
			`null`:                               false,
		}
		for in, ok := range tests {
			rep, err := Parse(strings.NewReader(`{"SchemaVersion":2,"ArtifactName":"a","CreatedAt":` + in + `}`))
			if err != nil || rep.CreatedAt.IsZero() == ok {
				t.Errorf("CreatedAt %s: %v, zero=%v", in, err, rep.CreatedAt.IsZero())
			}
		}
	})
}

func TestParseHugeAndOddStrings(t *testing.T) {
	huge := strings.Repeat("é", 100_000) // 200 kB
	doc := `{"SchemaVersion":2,"ArtifactName":"` + huge + `","Results":[{"Target":"` + huge + `","Vulnerabilities":[
	  {"VulnerabilityID":"` + huge + `","PkgName":"` + huge + `","Title":"` + huge + `","Severity":"LOW"},
	  {"VulnerabilityID":"a\u0000b","Title":"nul\u0000","PkgName":"bad\ud800utf8"}]}]}`
	rep, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	v := rep.Vulns[0]
	if len(v.VulnID) > maxVulnIDLen || len(v.PkgName) > maxFieldLen || len(v.Title) > maxFieldLen || len(v.Target) > maxFieldLen || len(rep.ArtifactName) > maxFieldLen {
		t.Errorf("fields not bounded: %d %d %d", len(v.VulnID), len(v.PkgName), len(rep.ArtifactName))
	}
	for _, s := range []string{v.VulnID, v.PkgName, v.Title, rep.ArtifactName} {
		if !isValidUTF8(s) || !strings.HasPrefix(s, "é") {
			t.Errorf("cut inside a character: %q", s[:10])
		}
	}
	w := rep.Vulns[1]
	if strings.ContainsRune(w.VulnID+w.Title, 0) || w.VulnID != "a�b" || w.Title != "nul�" {
		t.Errorf("NUL not replaced: %q %q", w.VulnID, w.Title)
	}
	if w.PkgName != "bad\uFFFDutf8" {
		t.Errorf("lone surrogate not replaced: %q", w.PkgName)
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

func TestClip(t *testing.T) {
	if got := clip("abc", 10); got != "abc" {
		t.Errorf("%q", got)
	}
	if got := clip("aéb", 2); got != "a" { // é is two bytes: cut before it
		t.Errorf("%q", got)
	}
	if got := clip("aéb", 3); got != "aé" {
		t.Errorf("%q", got)
	}
	if got := clip("", 0); got != "" {
		t.Errorf("%q", got)
	}
}

func FuzzParse(f *testing.F) {
	for _, name := range []string{"image-suppressed.json", "secret-fs.json"} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatal(err)
		}
		if len(b) > 8192 {
			b = b[:8192]
		}
		f.Add(b)
	}
	f.Add([]byte(`{"SchemaVersion":2,"ArtifactName":"a","Results":[{"Target":"t","Vulnerabilities":[{"VulnerabilityID":"CVE-1","Severity":"HIGH","FixedVersion":"1"}],"Secrets":[{"RuleID":"r"}],"ExperimentalModifiedFindings":[{"Type":"vulnerability","Status":"ignored","Finding":{"Severity":"LOW"}}]}]}`))
	f.Add([]byte(`{"SchemaVersion":2}`))
	f.Add([]byte(`[]`))
	f.Add([]byte(``))
	f.Fuzz(func(t *testing.T, data []byte) {
		rep, err := Parse(bytes.NewReader(data))
		if err != nil {
			if rep != nil {
				t.Fatal("report and error together")
			}
			return
		}
		// The counts always agree with the rows.
		var c Counts
		for _, v := range rep.Vulns {
			if v.Suppressed {
				c.Suppressed++
				continue
			}
			switch v.Severity {
			case SevCritical:
				c.Critical++
			case SevHigh:
				c.High++
			case SevMedium:
				c.Medium++
			case SevLow:
				c.Low++
			case SevUnknown:
				c.Unknown++
			default:
				t.Fatalf("bad severity %q", v.Severity)
			}
			if v.FixedVersion != "" {
				c.Fixable++
			}
			if strings.IndexByte(v.VulnID+v.Title+v.PkgName, 0) >= 0 {
				t.Fatal("NUL kept")
			}
		}
		for _, s := range rep.Secrets {
			if s.Suppressed {
				c.Suppressed++
			} else {
				c.Secrets++
			}
		}
		if c != rep.Counts {
			t.Fatalf("counts %+v do not match rows %+v", rep.Counts, c)
		}
	})
}

func TestParseFindingCap(t *testing.T) {
	vulns := func(n int) string { return strings.TrimSuffix(strings.Repeat(`{"Severity":"LOW"},`, n), ",") }
	doc := func(results string) string {
		return `{"SchemaVersion":2,"ArtifactName":"a","Results":[` + results + `]}`
	}
	tests := []struct {
		name string
		in   string
		max  int
		ok   bool
	}{
		{"at the cap", doc(`{"Vulnerabilities":[` + vulns(5) + `]}`), 5, true},
		{"one over", doc(`{"Vulnerabilities":[` + vulns(6) + `]}`), 5, false},
		{"over across results", doc(`{"Vulnerabilities":[` + vulns(3) + `]},{"Vulnerabilities":[` + vulns(3) + `]}`), 5, false},
		{"secrets count", doc(`{"Vulnerabilities":[` + vulns(3) + `],"Secrets":[{},{},{}]}`), 5, false},
		{"suppressed count", doc(`{"Vulnerabilities":[` + vulns(3) + `],"ExperimentalModifiedFindings":[{"Type":"vulnerability"},{"Type":"secret"},{"Type":"vulnerability"}]}`), 5, false},
		{"suppressed at the cap", doc(`{"Vulnerabilities":[` + vulns(3) + `],"ExperimentalModifiedFindings":[{"Type":"vulnerability"},{"Type":"secret"}]}`), 5, true},
		{"other modified types do not count", doc(`{"Vulnerabilities":[` + vulns(5) + `],"ExperimentalModifiedFindings":[{"Type":"license"},{"Type":"misconfiguration"}]}`), 5, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := &Report{}
			_, err := parseTo(strings.NewReader(tc.in), tc.max, collectSink{rep}, true, rep)
			if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, ErrTooManyFindings)) {
				t.Fatalf("err %v, want ok=%v", err, tc.ok)
			}
		})
	}
	// The real cap.
	big := doc(`{"Vulnerabilities":[` + vulns(MaxFindings+1) + `]}`)
	if _, err := Parse(strings.NewReader(big)); !errors.Is(err, ErrTooManyFindings) {
		t.Fatalf("err = %v", err)
	}
	ok := doc(`{"Vulnerabilities":[` + vulns(MaxFindings) + `]}`)
	if rep, err := Parse(strings.NewReader(ok)); err != nil || len(rep.Vulns) != MaxFindings {
		t.Fatalf("at the cap: %v", err)
	}
}

func TestParseOrderIndependent(t *testing.T) {
	// Target, Class and Type may follow the findings.
	rep, err := Parse(strings.NewReader(`{"Results":[{"Vulnerabilities":[{"VulnerabilityID":"CVE-1","Severity":"HIGH"}],
	  "Type":"debian","Class":"os-pkgs","Target":"t"}],"ArtifactName":"a","SchemaVersion":2}`))
	if err != nil || rep.Vulns[0].Target != "t" || rep.Vulns[0].Class != "os-pkgs" || rep.Vulns[0].PkgType != "debian" {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestParseWrongShapesAreNotTrivy(t *testing.T) {
	for _, in := range []string{
		`{"SchemaVersion":2,"Results":{"a":[1,{"b":2}]}}`,
		`{"SchemaVersion":2,"Results":[5]}`,
		`{"SchemaVersion":2,"Results":[[1,2]]}`,
		`{"SchemaVersion":2,"Results":[{"Vulnerabilities":{"x":1}}]}`,
		`{"SchemaVersion":2,"Results":[{"Secrets":"x"}]}`,
		`{"SchemaVersion":2,"ArtifactName":"a","Trivy":[1]}`,
	} {
		if _, err := Parse(strings.NewReader(in)); !errors.Is(err, ErrNotTrivy) {
			t.Errorf("%s: err = %v", in, err)
		}
	}
	// A syntax error later in the body wins over an earlier wrong shape.
	if _, err := Parse(strings.NewReader(`{"SchemaVersion":2,"Results":"x","Z":[}`)); !errors.Is(err, ErrInvalidJSON) {
		t.Errorf("err = %v", err)
	}
}

func TestClipControlCharacters(t *testing.T) {
	tests := []struct {
		in   string
		max  int
		want string
	}{
		{"a\x00b", 10, "a�b"},
		{"a\x01\x1fb", 10, "a��b"},
		{"tab\tnl\ncr\r", 20, "tab\tnl\ncr\r"},
		{"\x01\x01", 4, "�"},
		{"\x00\x00\x00", 6, "��"},
		{"a\x7fb", 10, "a\x7fb"},
	}
	for _, tc := range tests {
		got := clip(tc.in, tc.max)
		if got != tc.want || len(got) > tc.max {
			t.Errorf("clip(%q, %d) = %q", tc.in, tc.max, got)
		}
	}
	// Every stored field, in a report full of control characters, stays within its limit.
	nul := strings.Repeat(`\u0000`, 3000)
	rep, err := Parse(strings.NewReader(`{"SchemaVersion":2,"ArtifactName":"` + nul + `","Results":[{"Target":"` + nul + `","Vulnerabilities":[{"VulnerabilityID":"` + nul + `","Title":"` + nul + `"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	v := rep.Vulns[0]
	if len(rep.ArtifactName) > maxArtifactLen || len(v.VulnID) > maxVulnIDLen || len(v.Title) > maxFieldLen || len(v.Target) > maxFieldLen {
		t.Errorf("limits exceeded: %d %d %d %d", len(rep.ArtifactName), len(v.VulnID), len(v.Title), len(v.Target))
	}
}

func TestParseArtifactNameBound(t *testing.T) {
	rep, err := Parse(strings.NewReader(`{"SchemaVersion":2,"ArtifactName":"` + strings.Repeat("a", 10_000) + `"}`))
	if err != nil || len(rep.ArtifactName) != maxArtifactLen {
		t.Fatalf("%d %v", len(rep.ArtifactName), err)
	}
}

// Unknown fields, however large, are skipped without being held in memory and
// without affecting the result.
func TestParseSkipsLargeUnknownFields(t *testing.T) {
	// One unknown value of about 24 MB: nested arrays and objects.
	var unknown strings.Builder
	unknown.WriteString(`[`)
	for i := 0; i < 160_000; i++ {
		if i > 0 {
			unknown.WriteString(`,`)
		}
		unknown.WriteString(`{"a":[1,2,[3,[4,{"b":"` + strings.Repeat("x", 100) + `"}]]],"c":null,"d":true}`)
	}
	unknown.WriteString(`]`)
	if unknown.Len() < 20<<20 {
		t.Fatalf("setup: %d bytes", unknown.Len())
	}
	doc := `{"Future":` + unknown.String() + `,"SchemaVersion":2,"ArtifactName":"a",
	  "Results":[{"Target":"t","Vulnerabilities":[{"VulnerabilityID":"CVE-1","Severity":"HIGH"}]}]}`

	// Sample the heap while parsing. A value read into memory (a
	// json.RawMessage, say) would need at least its own size, and more while
	// its buffer grows.
	debug.SetGCPercent(10)
	defer debug.SetGCPercent(100)
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var peak atomic.Uint64
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			default:
			}
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > peak.Load() {
				peak.Store(m.HeapAlloc)
			}
			time.Sleep(time.Millisecond)
		}
	}()
	rep, err := Parse(strings.NewReader(doc))
	close(stop)
	<-done
	if err != nil || len(rep.Vulns) != 1 || rep.Counts.High != 1 || rep.Vulns[0].Target != "t" {
		t.Fatalf("%+v %v", rep, err)
	}
	if grew := int64(peak.Load()) - int64(base.HeapAlloc); grew > 8<<20 {
		t.Errorf("the heap grew by %d MB while skipping %d MB", grew>>20, unknown.Len()>>20)
	}
}

func TestParseSkipsUnknownFieldsEverywhere(t *testing.T) {
	u := `[1,{"a":[2,{"b":null}],"c":"s"},[[]],"x",true,null,{}]`
	doc := `{"A":` + u + `,"SchemaVersion":2,"B":{"x":` + u + `},"ArtifactName":"a","Trivy":{"Version":"1","Z":` + u + `},
	  "Results":[{"Q":` + u + `,"Target":"t","Vulnerabilities":[{"VulnerabilityID":"CVE-1","Severity":"HIGH","CVSS":` + u + `,"References":[` + u + `]}],
	  "Secrets":[{"RuleID":"r","Severity":"LOW","Code":` + u + `}],"Z":7,"Y":"s","X":null,"W":false}],"Last":` + u + `}`
	rep, err := Parse(strings.NewReader(doc))
	if err != nil || rep.Counts.High != 1 || rep.Counts.Secrets != 1 || rep.TrivyVersion != "1" || rep.Vulns[0].Target != "t" {
		t.Fatalf("%+v %v", rep, err)
	}
	// A syntax error inside an unknown value is still found.
	for _, bad := range []string{
		`{"SchemaVersion":2,"ArtifactName":"a","Future":[1,{"a":}]}`,
		`{"SchemaVersion":2,"ArtifactName":"a","Future":[1,2`,
		`{"SchemaVersion":2,"ArtifactName":"a","Results":[{"Future":{"a":[}}]}`,
	} {
		if _, err := Parse(strings.NewReader(bad)); !errors.Is(err, ErrInvalidJSON) {
			t.Errorf("%s: err = %v", bad, err)
		}
	}
}
