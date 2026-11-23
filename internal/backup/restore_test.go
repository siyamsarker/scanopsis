// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"strings"
	"testing"
	"time"
)

func TestParseTimestamp(t *testing.T) {
	utc := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for in, want := range map[string]time.Time{
		"2026-03-04 05:06:07+00":        utc("2026-03-04T05:06:07Z"),
		"2026-03-04 05:06:07.123456+00": utc("2026-03-04T05:06:07.123456Z"),
		"2026-03-04 11:06:07.5+06":      utc("2026-03-04T05:06:07.5Z"),
		"2026-03-04 10:36:07+05:30":     utc("2026-03-04T05:06:07Z"),
		"1890-01-01 00:00:00+01:09:21":  utc("1889-12-31T22:50:39Z"),
		"2026-03-04 05:06:07-03":        utc("2026-03-04T08:06:07Z"),
		"infinity":                      farFuture,
		"-infinity":                     farPast,
		"0044-03-15 12:00:00+00 BC":     farPast,
		"12026-03-04 05:06:07+00":       farFuture,
	} {
		got, err := parseTimestamp(in)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseTimestamp(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "yesterday", "04/03/2026 05:06:07 +00", "2026-03-04"} {
		if _, err := parseTimestamp(in); err == nil {
			t.Errorf("parseTimestamp(%q) accepted", in)
		}
	}
}

func TestRetentionCount(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	var c retentionCount
	for _, age := range []time.Duration{
		-time.Hour,               // in the future
		0,                        // now
		10 * day,                 // young
		90 * day,                 // exactly at the cutoff of 90 days: kept
		90*day + time.Nanosecond, // just past it: removed
		100 * day,                // removed at 90
		5000 * day,               // older than the longest retention
	} {
		c.add(now, now.Add(-age))
	}
	c.add(now, now.AddDate(-400, 0, 0)) // older than a Duration can say
	c.add(now, farPast)
	c.add(now, farFuture)
	for days, want := range map[int]int64{90: 5, 7: 7, 3650: 3, 1: 7, 100000: 3} {
		if got := c.removed(days); got != want {
			t.Errorf("removed(%d) = %d, want %d", days, got, want)
		}
	}
	if !c.oldest.Equal(farPast) {
		t.Errorf("oldest = %v", c.oldest)
	}
}

func TestCountRows(t *testing.T) {
	for in, want := range map[string]int64{
		"":                                     0,
		"1,a\n":                                1,
		"1,a\n2,b\n":                           2,
		"1,\"two\nlines\"\n2,b\n":              2,
		"1,\"quote \"\" and\nnew line\"\n":     1,
		"1,\"\"\n2,\"\"\"\"\n3,\"a\"\"\nb\"\n": 3,
	} {
		var c rowCounter
		// One byte at a time and all at once give the same count.
		for i := range len(in) {
			_, _ = c.Write([]byte{in[i]})
		}
		var all rowCounter
		_, _ = all.Write([]byte(in))
		if c.rows != want || all.rows != want {
			t.Errorf("rows of %q = %d and %d, want %d", strings.ReplaceAll(in, "\n", `\n`), c.rows, all.rows, want)
		}
	}
}
