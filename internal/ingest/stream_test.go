// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// streamed reads a report as the upload path does: a first parse that drops
// the findings, then a second that hands them on.
func streamed(t *testing.T, doc string) (*Report, *Report) {
	t.Helper()
	first := &Report{}
	if _, err := parseTo(strings.NewReader(doc), MaxFindings, nopSink{}, false, first); err != nil {
		t.Fatal(err)
	}
	if len(first.Vulns)+len(first.Secrets) != 0 {
		t.Fatal("the first parse kept findings")
	}
	second := &Report{}
	if _, err := parseTo(strings.NewReader(doc), MaxFindings, collectSink{second}, !first.ordered, &Report{}); err != nil {
		t.Fatal(err)
	}
	return first, second
}

// Reading a report in two parts gives exactly what Parse gives, for the
// fixtures and for key orders that need the held-back path.
func TestStreamedParseEqualsParse(t *testing.T) {
	docs := map[string]string{
		"image":       string(readFixture(t, "image-full.json")),
		"suppressed":  string(readFixture(t, "image-suppressed.json")),
		"secrets":     string(readFixture(t, "secret-fs.json")),
		"header last": `{"SchemaVersion":2,"ArtifactName":"a","Results":[{"Vulnerabilities":[{"VulnerabilityID":"CVE-1","Severity":"HIGH","FixedVersion":"2"}],"Secrets":[{"RuleID":"r","Severity":"LOW"}],"Target":"late","Class":"os-pkgs","Type":"debian"}]}`,
		"modified first": `{"SchemaVersion":2,"ArtifactName":"a","Results":[{"Target":"t","ExperimentalModifiedFindings":[{"Type":"vulnerability","Status":"not_affected","Finding":{"VulnerabilityID":"CVE-2","Severity":"LOW"}}],` +
			`"Vulnerabilities":[{"VulnerabilityID":"CVE-3","Severity":"HIGH"}],"Secrets":[{"RuleID":"x"}]}]}`,
		"secrets before vulnerabilities": `{"SchemaVersion":2,"ArtifactName":"a","Results":[{"Target":"t","Secrets":[{"RuleID":"x"}],"Vulnerabilities":[{"VulnerabilityID":"CVE-3"}]}]}`,
		"two results": `{"SchemaVersion":2,"ArtifactName":"a","Results":[{"Target":"one","Class":"os-pkgs","Type":"alpine","Vulnerabilities":[{"VulnerabilityID":"CVE-1"}]},` +
			`{"Target":"two","Vulnerabilities":[{"VulnerabilityID":"CVE-2"}],"Type":"npm"}]}`,
	}
	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			want, err := Parse(strings.NewReader(doc))
			if err != nil {
				t.Fatal(err)
			}
			first, got := streamed(t, doc)
			if !reflect.DeepEqual(got.Vulns, want.Vulns) || !reflect.DeepEqual(got.Secrets, want.Secrets) {
				t.Errorf("findings differ:\n got %+v %+v\nwant %+v %+v", got.Vulns, got.Secrets, want.Vulns, want.Secrets)
			}
			if first.Counts != want.Counts || first.ArtifactName != want.ArtifactName || !first.CreatedAt.Equal(want.CreatedAt) || first.TrivyVersion != want.TrivyVersion {
				t.Errorf("report %+v, want %+v", first, want)
			}
		})
	}
}

func TestStreamedParseOrderedFlag(t *testing.T) {
	for name, tc := range map[string]struct {
		doc     string
		ordered bool
	}{
		"as Trivy writes it": {`{"SchemaVersion":2,"ArtifactName":"a","Results":[{"Target":"t","Class":"c","Type":"y","Vulnerabilities":[{}],"Secrets":[{}],"ExperimentalModifiedFindings":[]}]}`, true},
		"no findings":        {`{"SchemaVersion":2,"ArtifactName":"a"}`, true},
		"target after":       {`{"SchemaVersion":2,"ArtifactName":"a","Results":[{"Vulnerabilities":[{}],"Target":"t"}]}`, false},
		"secrets first":      {`{"SchemaVersion":2,"ArtifactName":"a","Results":[{"Secrets":[{}],"Vulnerabilities":[{}]}]}`, false},
		"one late of two":    {`{"SchemaVersion":2,"ArtifactName":"a","Results":[{"Target":"t","Vulnerabilities":[{}]},{"Vulnerabilities":[{}],"Class":"c"}]}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			if first, _ := streamed(t, tc.doc); first.ordered != tc.ordered {
				t.Errorf("ordered = %v, want %v", first.ordered, tc.ordered)
			}
		})
	}
}

// failSink fails after a number of vulnerabilities.
type failSink struct {
	nopSink
	after int
	n     int
}

var errSink = &tokenError{msg: "sink"}

func (f *failSink) vuln(*Vuln) error {
	if f.n++; f.n > f.after {
		return errSink
	}
	return nil
}

// A sink error stops the parse and comes back unchanged.
func TestParseStopsOnSinkError(t *testing.T) {
	doc := `{"SchemaVersion":2,"ArtifactName":"a","Results":[{"Target":"t","Vulnerabilities":[{},{},{}]}]}`
	for _, buffer := range []bool{false, true} {
		_, err := parseTo(strings.NewReader(doc), MaxFindings, &failSink{after: 1}, buffer, &Report{})
		if !errors.Is(err, errSink) {
			t.Errorf("buffer=%v: err = %v", buffer, err)
		}
	}
}
