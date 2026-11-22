// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package findings

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/store/storetest"
)

// randText returns n printable ASCII characters that do not compress, so a
// value of that length really is too long for a B-tree entry.
func randText(r *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('!' + r.Intn(90))
	}
	return string(b)
}

// TestFindingsAtClipLimits stores findings whose vuln_id, pkg_name and target
// are 4096 characters long (the longest ingestion keeps), in scans whose ref
// and artifact are at their limits (1024 and 512 bytes), and checks that the
// indexes accept them and the first-seen lookup still finds the earliest scan
// through the hash key.
func TestFindingsAtClipLimits(t *testing.T) {
	t.Parallel()
	pool := storetest.New(t)
	mux := newMux(pool, "")
	ctx := context.Background()
	rnd := rand.New(rand.NewSource(1))
	svc := newService(t, pool, "long", "grp/long", false)
	ref, artifact := randText(rnd, 1024), randText(rnd, 512)
	vulnID, pkg, target := randText(rnd, 4096), randText(rnd, 4096), randText(rnd, 4096)
	other := randText(rnd, 4096) // same ID and package, another target: a different finding

	first := time.Now().UTC().Truncate(time.Second).Add(-48 * time.Hour)
	for i := range 3 {
		scan := newScan(t, pool, svc, "image", artifact, ref, first.Add(time.Duration(i)*24*time.Hour))
		for _, tg := range []string{target, other} {
			if i == 0 && tg == other {
				continue // the second target appears from the second scan on
			}
			_, err := pool.Exec(ctx, `
				INSERT INTO vulnerabilities (scan_id, target, class, pkg_name, vuln_id, severity)
				VALUES ($1, $2, 'os-pkgs', $3, $4, 'HIGH')`, scan, tg, pkg, vulnID)
			if err != nil {
				t.Fatalf("scan %d: a finding with 4096-character fields was rejected: %v", i, err)
			}
		}
		refresh(t, pool, scan)
	}

	var page struct {
		Items []struct {
			Target      string    `json:"target"`
			FirstSeenAt time.Time `json:"first_seen_at"`
		} `json:"items"`
	}
	getJSON(t, mux, fmt.Sprintf("/api/v1/services/%d/vulnerabilities", svc), &page)
	if len(page.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(page.Items))
	}
	for _, it := range page.Items {
		want := first // the first target is in all three scans
		if it.Target == other {
			want = first.Add(24 * time.Hour)
		}
		if !it.FirstSeenAt.Equal(want) {
			t.Errorf("first seen of target %.8s... = %v, want %v", it.Target, it.FirstSeenAt, want)
		}
	}
}
