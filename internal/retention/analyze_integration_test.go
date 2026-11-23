// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package retention

import (
	"context"
	"testing"
	"time"
)

// lastAnalyze returns when ANALYZE last ran on table, or the zero time. The
// statistics reach other sessions with a short delay, so it waits for the
// state the caller expects.
func (f *fixture) lastAnalyze(table string, wantSet bool) time.Time {
	f.t.Helper()
	var at *time.Time
	for range 40 {
		if err := f.pool.QueryRow(context.Background(),
			`SELECT last_analyze FROM pg_stat_user_tables WHERE relname = $1`, table).Scan(&at); err != nil {
			f.t.Fatal(err)
		}
		if (at != nil) == wantSet {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if at == nil {
		return time.Time{}
	}
	return *at
}

// A run that removed or cleared at least the threshold of rows ends with
// ANALYZE of the scans and, because their findings went too, of the
// findings; a smaller run leaves that to autovacuum.
func TestRunAnalyzesAfterALargeCleanUp(t *testing.T) {
	t.Run("at the threshold", func(t *testing.T) {
		f := newFixture(t)
		seedScans(f)
		r := f.job1(newSettings(30))
		r.analyzeAt = 3
		res := r.RunOnce(context.Background())
		if res.FailedSteps != 0 || res.Scans+res.RawReports < 3 || !res.Analyzed {
			t.Fatalf("result = %+v, want an analyzed run", res)
		}
		if f.lastAnalyze("scans", true).IsZero() || f.lastAnalyze("vulnerabilities", true).IsZero() {
			t.Error("scans and vulnerabilities were not analyzed")
		}
	})
	t.Run("below the threshold", func(t *testing.T) {
		f := newFixture(t)
		seedScans(f)
		r := f.job1(newSettings(30))
		r.analyzeAt = 1000
		res := r.RunOnce(context.Background())
		if res.Scans == 0 || res.Analyzed {
			t.Fatalf("result = %+v, want scans deleted and no ANALYZE", res)
		}
		if !f.lastAnalyze("scans", false).IsZero() {
			t.Error("scans were analyzed by a small run")
		}
	})
	t.Run("only raw reports cleared", func(t *testing.T) {
		f := newFixture(t)
		svc := f.service("svc")
		// Old scans that a newer scan of the same ref replaced: with a long
		// retention they stay, but their original reports are cleared.
		for i := range 5 {
			f.scan(svc, "main", "img", "image", time.Duration(40+i)*day)
		}
		f.scan(svc, "main", "img", "image", day)
		r := f.job1(newSettings(365))
		r.analyzeAt = 5
		res := r.RunOnce(context.Background())
		if res.Scans != 0 || res.RawReports != 5 || !res.Analyzed {
			t.Fatalf("result = %+v, want 5 reports cleared and ANALYZE", res)
		}
		if f.lastAnalyze("scans", true).IsZero() {
			t.Error("scans were not analyzed")
		}
		if !f.lastAnalyze("vulnerabilities", false).IsZero() {
			t.Error("vulnerabilities were analyzed although no scan was deleted")
		}
	})
}
