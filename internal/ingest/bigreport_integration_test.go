// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package ingest

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"math/rand"
	"testing"
)

// A report whose compressed form is above discardConnAbove makes the saver
// close its database connection afterwards. The pool must open another one,
// so uploads go on working, and the stored report is intact.
func TestIntegrationLargeReportDiscardsConnection(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var b bytes.Buffer
	b.WriteString(`{"SchemaVersion":2,"ArtifactName":"big","Results":[{"Target":"t","Vulnerabilities":[`)
	for i := range 1500 {
		if i > 0 {
			b.WriteByte(',')
		}
		raw := make([]byte, 3000)
		rng.Read(raw) // incompressible
		fmt.Fprintf(&b, `{"VulnerabilityID":"CVE-%d","Severity":"LOW","Title":%q}`, i, base64.StdEncoding.EncodeToString(raw))
	}
	b.WriteString(`]}]}`)
	body := b.Bytes()
	packed := gz(t, body)
	if len(packed) < discardConnAbove {
		t.Fatalf("test report compresses to %d bytes, below %d", len(packed), discardConnAbove)
	}

	e := newIEnv(t, nil)
	first := e.mustUpload(upload{body: packed, gzip: true})
	// The next upload (and a retry of the same job, which replaces the scan)
	// gets a working connection.
	second := e.mustUpload(upload{body: packed, gzip: true})
	if !second.Replaced || second.ScanID == first.ScanID {
		t.Errorf("second upload %+v after %+v", second, first)
	}
	if n := e.count("vulnerabilities"); n != 1500 {
		t.Errorf("%d vulnerabilities stored", n)
	}
	var stored []byte
	if err := e.pool.QueryRow(context.Background(), `SELECT raw_report FROM scans`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, packed) {
		t.Error("stored report differs")
	}
}
