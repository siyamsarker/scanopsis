// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"slices"
	"testing"
	"time"
)

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNextRun(t *testing.T) {
	berlin := mustZone(t, "Europe/Berlin")
	dhaka := mustZone(t, "Asia/Dhaka")
	lordHowe := mustZone(t, "Australia/Lord_Howe")
	tests := []struct {
		name  string
		now   time.Time
		times []string
		loc   *time.Location
		want  time.Time
	}{
		{"later today", utc("2026-11-23T01:00:00Z"), []string{"02:00"}, time.UTC, utc("2026-11-23T02:00:00Z")},
		{"earliest of several", utc("2026-11-23T05:00:00Z"), []string{"23:00", "06:30", "12:00"}, time.UTC, utc("2026-11-23T06:30:00Z")},
		{"next of several", utc("2026-11-23T06:30:00Z"), []string{"23:00", "06:30", "12:00"}, time.UTC, utc("2026-11-23T12:00:00Z")},
		{"exactly now is not after now", utc("2026-11-23T02:00:00Z"), []string{"02:00"}, time.UTC, utc("2026-11-24T02:00:00Z")},
		{"wraps to the next day", utc("2026-11-23T23:59:59Z"), []string{"00:00", "12:00"}, time.UTC, utc("2026-11-24T00:00:00Z")},
		{"wraps over the end of a year", utc("2026-12-31T23:30:00Z"), []string{"02:00"}, time.UTC, utc("2027-01-01T02:00:00Z")},
		{"zone ahead of UTC", utc("2026-11-23T19:00:00Z"), []string{"02:00"}, dhaka, utc("2026-11-23T20:00:00Z")},
		{"zone ahead of UTC, date already tomorrow there", utc("2026-11-23T21:00:00Z"), []string{"02:00"}, dhaka, utc("2026-11-24T20:00:00Z")},
		{"zone in winter", utc("2026-11-23T00:00:00Z"), []string{"02:00"}, berlin, utc("2026-11-23T01:00:00Z")},
		// 2026-03-29: 02:00 CET becomes 03:00 CEST, so 02:30 does not exist
		// and the backup runs at the first instant after the gap.
		{"spring forward 02:30", utc("2026-03-28T23:00:00Z"), []string{"02:30"}, berlin, utc("2026-03-29T01:00:00Z")},
		{"spring forward other times", utc("2026-03-28T23:00:00Z"), []string{"02:30", "01:30", "04:00"}, berlin, utc("2026-03-29T00:30:00Z")},
		{"day after spring forward", utc("2026-03-29T01:00:00Z"), []string{"02:30"}, berlin, utc("2026-03-30T00:30:00Z")},
		// 2026-10-25: 03:00 CEST becomes 02:00 CET, so 02:30 happens twice;
		// the backup runs at the first one only.
		{"fall back 02:30 first", utc("2026-10-24T22:00:00Z"), []string{"02:30"}, berlin, utc("2026-10-25T00:30:00Z")},
		{"fall back 02:30 not again", utc("2026-10-25T00:30:00Z"), []string{"02:30"}, berlin, utc("2026-10-26T01:30:00Z")},
		{"fall back between the two", utc("2026-10-25T00:45:00Z"), []string{"02:30"}, berlin, utc("2026-10-26T01:30:00Z")},
		// Lord Howe Island moves its clocks by half an hour: on 2026-10-04
		// 02:00 (+10:30) becomes 02:30 (+11), and on 2026-04-05 02:00 (+11)
		// becomes 01:30 (+10:30).
		{"half-hour jump forward", utc("2026-10-03T13:00:00Z"), []string{"02:15"}, lordHowe, utc("2026-10-03T15:30:00Z")},
		{"half-hour jump back first", utc("2026-04-04T13:00:00Z"), []string{"01:45"}, lordHowe, utc("2026-04-04T14:45:00Z")},
		{"half-hour jump back not again", utc("2026-04-04T14:45:00Z"), []string{"01:45"}, lordHowe, utc("2026-04-05T15:15:00Z")},
		{"no times", utc("2026-11-23T01:00:00Z"), nil, time.UTC, time.Time{}},
		{"bad times ignored", utc("2026-11-23T01:00:00Z"), []string{"nope", "25:00", "03:00"}, time.UTC, utc("2026-11-23T03:00:00Z")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextRun(tt.now, tt.times, tt.loc)
			if !got.Equal(tt.want) {
				t.Errorf("NextRun = %v, want %v", got.UTC(), tt.want)
			}
			if !got.IsZero() && !got.After(tt.now) {
				t.Errorf("NextRun %v is not after now %v", got, tt.now)
			}
		})
	}
}

// TestNextRunEveryDay walks a year of Berlin days with two times and
// checks that every day gets exactly two runs, in order.
func TestNextRunEveryDay(t *testing.T) {
	berlin := mustZone(t, "Europe/Berlin")
	now := utc("2026-01-01T00:00:00Z")
	end := utc("2027-01-01T00:00:00Z")
	perDay := map[string]int{}
	for {
		next := NextRun(now, []string{"02:30", "14:00"}, berlin)
		if !next.After(now) {
			t.Fatalf("NextRun(%v) = %v", now, next)
		}
		if !next.Before(end) {
			break
		}
		perDay[next.In(berlin).Format(time.DateOnly)]++
		now = next
	}
	if len(perDay) != 365 {
		t.Errorf("%d days, want 365", len(perDay))
	}
	for day, n := range perDay {
		if n != 2 {
			t.Errorf("%s has %d runs", day, n)
		}
	}
}

func TestExpired(t *testing.T) {
	now := utc("2026-11-23T12:00:00Z")
	day := 24 * time.Hour
	info := func(name string, kind Kind, age time.Duration) Info {
		return Info{Name: name, Kind: kind, CreatedAt: now.Add(-age)}
	}
	list := []Info{
		info("s-new", KindScheduled, time.Hour),
		info("s-7d", KindScheduled, 7*day),
		info("s-old", KindScheduled, 7*day+time.Second),
		info("s-older", KindScheduled, 30*day),
		info("m-old", KindManual, 400*day),
		info("u-old", KindUploaded, 400*day),
		info("b-old", KindBeforeRestore, 400*day),
	}
	if got := Expired(list, now, 7); !slices.Equal(got, []string{"s-old", "s-older"}) {
		t.Errorf("Expired = %v", got)
	}

	// The newest scheduled backup stays however old it is, wherever it is
	// in the list.
	old := []Info{
		info("s-older", KindScheduled, 30*day),
		info("s-old", KindScheduled, 20*day),
		info("m-new", KindManual, time.Hour),
	}
	if got := Expired(old, now, 7); !slices.Equal(got, []string{"s-older"}) {
		t.Errorf("Expired = %v, want only the older one", got)
	}
	if got := Expired(old[:1], now, 1); len(got) != 0 {
		t.Errorf("Expired of the only scheduled backup = %v", got)
	}
	if got := Expired(nil, now, 7); len(got) != 0 {
		t.Errorf("Expired(nil) = %v", got)
	}
}
