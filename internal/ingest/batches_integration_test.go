// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package ingest

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// Findings are written in batches of copyBatch. A report with several batches
// of both kinds, with one result that names its target after its findings,
// stores exactly what the report says.
func TestIntegrationStoresFindingsInBatches(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"SchemaVersion":2,"ArtifactName":"batches","Results":[`)
	// Header after the findings: this result is held while it is read.
	b.WriteString(`{"Vulnerabilities":[`)
	n := 2*copyBatch + 17
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"VulnerabilityID":"CVE-%d","PkgName":"p%d","Severity":"HIGH","FixedVersion":"2"}`, i, i)
	}
	b.WriteString(`],"Secrets":[`)
	for i := range copyBatch + 5 {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"RuleID":"r%d","Severity":"CRITICAL","StartLine":%d,"EndLine":%d}`, i, i, i+1)
	}
	b.WriteString(`],"Target":"late-target","Class":"os-pkgs","Type":"alpine"},`)
	b.WriteString(`{"Target":"second","Class":"lang-pkgs","Type":"npm","Vulnerabilities":[{"VulnerabilityID":"CVE-x","Severity":"LOW"}],`)
	b.WriteString(`"ExperimentalModifiedFindings":[{"Type":"vulnerability","Status":"not_affected","Finding":{"VulnerabilityID":"CVE-m","Severity":"LOW"}}]}`)
	b.WriteString(`]}`)
	body := []byte(b.String())

	e := newIEnv(t, nil)
	res := e.mustUpload(upload{body: body})
	wantV, wantS := expectedRows(t, body, KindImage)
	gotV, gotS := e.storedRows(res.ScanID)
	sort.Strings(gotV)
	sort.Strings(gotS)
	if strings.Join(gotV, "\n") != strings.Join(wantV, "\n") || strings.Join(gotS, "\n") != strings.Join(wantS, "\n") {
		t.Fatalf("stored %d vulnerabilities and %d secrets, want %d and %d (or their content differs)", len(gotV), len(gotS), len(wantV), len(wantS))
	}
	if len(gotV) != n+2 || len(gotS) != copyBatch+5 {
		t.Errorf("rows %d %d", len(gotV), len(gotS))
	}
}
