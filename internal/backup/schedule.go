// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"time"
)

// NextRun returns the earliest of the times of day (24-hour "HH:MM" in loc)
// that comes strictly after now, or the zero time when times holds no valid
// time. Daylight saving time is handled the way people expect a daily alarm
// to behave: a time that does not exist on a day, because the clocks jump
// over it, runs at the first instant after the jump; a time that happens
// twice, because the clocks go back, runs at its first occurrence only.
func NextRun(now time.Time, times []string, loc *time.Location) time.Time {
	var best time.Time
	local := now.In(loc)
	y, m, d := local.Date()
	// Two days ahead covers every zone: the next occurrence of any time of
	// day is at most a day and a jump of the clocks away.
	for day := range 3 {
		for _, s := range times {
			h, mi, ok := parseTimeOfDay(s)
			if !ok {
				continue
			}
			at := occurrence(y, m, d+day, h, mi, loc)
			if at.After(now) && (best.IsZero() || at.Before(best)) {
				best = at
			}
		}
	}
	return best
}

// parseTimeOfDay reads "HH:MM".
func parseTimeOfDay(s string) (hour, minute int, ok bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, false
	}
	digit := func(b byte) (int, bool) { return int(b - '0'), b >= '0' && b <= '9' }
	h1, ok1 := digit(s[0])
	h2, ok2 := digit(s[1])
	m1, ok3 := digit(s[3])
	m2, ok4 := digit(s[4])
	hour, minute = h1*10+h2, m1*10+m2
	if !ok1 || !ok2 || !ok3 || !ok4 || hour > 23 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}

// occurrence returns the instant at which the clocks in loc first show the
// given wall time on the given day, or, when they skip it, the instant the
// skip ends.
func occurrence(y int, m time.Month, d, hour, minute int, loc *time.Location) time.Time {
	at := time.Date(y, m, d, hour, minute, 0, 0, loc)
	want := time.Date(y, m, d, hour, minute, 0, 0, time.UTC)
	got := wall(at)
	start, end := at.ZoneBounds()
	switch {
	case got.After(want):
		// Skipped: time.Date moved forward across the jump, so the jump
		// is the start of the zone period at is in.
		return start
	case got.Before(want):
		// Skipped, with time.Date moved backward: the jump ends the zone
		// period at is in.
		return end
	}
	// Repeated: the same wall time may also exist in the period before,
	// earlier in absolute time.
	if !start.IsZero() {
		_, before := start.Add(-time.Second).Zone()
		_, offset := at.Zone()
		if earlier := at.Add(time.Duration(offset-before) * time.Second); earlier.Before(start) && wall(earlier.In(loc)).Equal(want) {
			return earlier
		}
	}
	return at
}

// wall returns the wall-clock reading of t as a UTC time, so readings in
// different offsets compare by what the clock shows.
func wall(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
}

// Expired returns the names of the scheduled backups in list that are more
// than keepDays days old at now. The newest scheduled backup is never
// returned, however old, so a schedule that stopped working never deletes
// the last backup it made. Manual, uploaded and before-restore backups are
// kept until an administrator deletes them.
func Expired(list []Info, now time.Time, keepDays int) []string {
	newest, found := Info{}, false
	for _, b := range list {
		if b.Kind == KindScheduled && (!found || b.CreatedAt.After(newest.CreatedAt)) {
			newest, found = b, true
		}
	}
	keep := time.Duration(keepDays) * 24 * time.Hour
	var out []string
	for _, b := range list {
		if b.Kind == KindScheduled && b.Name != newest.Name && now.Sub(b.CreatedAt) > keep {
			out = append(out, b.Name)
		}
	}
	return out
}
