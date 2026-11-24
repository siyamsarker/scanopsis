// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package sonar

import (
	"testing"
	"time"
)

// An analysis gets a delivery's pipeline only when its date is exactly the
// delivered analysedAt, and only when the deliveries of that analysis agree.
func TestDeliveriesPipelineOf(t *testing.T) {
	at := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		ds   deliveries
		date time.Time
		want int64 // 0: none
	}{
		{"exact", deliveries{{pipeline: 11, analysedAt: at}}, at, 11},
		{"same instant, other zone", deliveries{{pipeline: 11, analysedAt: at}}, at.In(time.FixedZone("+06", 6*3600)), 11},
		{"newer analysis", deliveries{{pipeline: 11, analysedAt: at}}, at.Add(time.Second), 0},
		{"older analysis", deliveries{{pipeline: 11, analysedAt: at}}, at.Add(-time.Second), 0},
		{"unknown analysedAt", deliveries{{pipeline: 11}}, at, 0},
		{"no pipeline", deliveries{{analysedAt: at}}, at, 0},
		{"no deliveries", nil, at, 0},
		{"one of several", deliveries{{pipeline: 10, analysedAt: at.Add(-time.Hour)}, {pipeline: 11, analysedAt: at}}, at, 11},
		{"redelivered", deliveries{{pipeline: 11, analysedAt: at}, {pipeline: 11, analysedAt: at}}, at, 11},
		{"conflicting", deliveries{{pipeline: 11, analysedAt: at}, {pipeline: 12, analysedAt: at}}, at, 0},
	} {
		got := tc.ds.pipelineOf(tc.date)
		switch {
		case tc.want == 0 && got != nil:
			t.Errorf("%s: pipeline %d, want none", tc.name, *got)
		case tc.want != 0 && (got == nil || *got != tc.want):
			t.Errorf("%s: pipeline %v, want %d", tc.name, got, tc.want)
		}
	}
}
