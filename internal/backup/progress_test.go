// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import "testing"

func TestStatusPercentFollowsWork(t *testing.T) {
	s := NewService(ServiceOptions{Log: discard})
	t.Cleanup(s.Close)
	if _, err := s.begin(JobStatus{State: JobBackingUp, Type: JobTypeBackup}); err != nil {
		t.Fatal(err)
	}
	// Nine small tables are done, the large last one is half copied.
	s.setProgress(Progress{Table: "vulnerabilities", TablesDone: 9, TablesTotal: 10, work: 50, workTotal: 100})
	if got := s.Status().Percent; got != 50 {
		t.Errorf("percent = %d, want 50 (by work, not by tables)", got)
	}
	s.setProgress(Progress{TablesDone: 9, TablesTotal: 10, work: 100, workTotal: 100})
	if got := s.Status().Percent; got != 99 {
		t.Errorf("percent = %d, want 99 until the job has finished", got)
	}
	s.setProgress(Progress{TablesDone: 3, TablesTotal: 10})
	if got := s.Status().Percent; got != 30 {
		t.Errorf("percent without work = %d, want 30 (by tables)", got)
	}
	s.finish(func(st *JobStatus) { st.State = JobDone })
}

func TestBackupEstimate(t *testing.T) {
	// Two small tables, then one of 100 MiB stored, then one of 400 MiB.
	const mib = 1 << 20
	e := newBackupEstimate([]int64{0, 4 << 10, 100 * mib, 400 * mib})
	if e.total != 2*minTableWeight+500*mib {
		t.Fatalf("total = %d", e.total)
	}
	percent := func(p Progress) int { return e.apply(p).percent() }

	// The CSV of a table is taken to be defaultCSVRatio of its stored size
	// until a large enough table has been copied.
	half := int64(float64(50*mib) * defaultCSVRatio)
	if got := percent(Progress{TablesDone: 2, TablesTotal: 4, Bytes: 1000 + half, current: half}); got < 9 || got > 11 {
		t.Errorf("halfway through the first large table: %d%%, want about 10", got)
	}
	// The first large table gave 0.5 bytes of CSV per stored byte: the
	// second is expected to give as much.
	done := int64(1000 + 50*mib)
	var last int
	for _, part := range []int64{0, 50 * mib, 100 * mib, 150 * mib, 199 * mib} {
		got := percent(Progress{TablesDone: 3, TablesTotal: 4, Bytes: done + part, current: part})
		if got < last {
			t.Errorf("percent went back from %d to %d", last, got)
		}
		last = got
	}
	if want := (100 + 400*199/200) * 100 / 500; last < want-1 || last > want+1 {
		t.Errorf("near the end of the last table: %d%%, want about %d", last, want)
	}
	if got := percent(Progress{TablesDone: 3, TablesTotal: 4, Bytes: done + 100*mib, current: 100 * mib}); got != 60 {
		t.Errorf("halfway through the last table: %d%%, want 60", got)
	}
	// A table larger than expected stays just short of its end.
	if got := percent(Progress{TablesDone: 3, TablesTotal: 4, Bytes: done + 900*mib, current: 900 * mib}); got != 99 {
		t.Errorf("past the expected end: %d%%, want 99", got)
	}
	if got := percent(Progress{TablesDone: 4, TablesTotal: 4, Bytes: done + 900*mib}); got != 99 {
		t.Errorf("all tables done: %d%%, want 99", got)
	}
}

func TestRestoreWork(t *testing.T) {
	const size = 1000
	cases := []struct {
		loading bool
		read    int64
		want    int
	}{
		{false, 0, 0},
		{false, 500, 5},
		{false, 1000, 10},
		{true, 0, 10},
		{true, 500, 55},
		{true, 1000, 99},
	}
	for _, c := range cases {
		if got := restoreWork(Progress{}, c.read, size, c.loading).percent(); got != c.want {
			t.Errorf("loading %v, %d of %d bytes read: %d%%, want %d", c.loading, c.read, size, got, c.want)
		}
	}
}

func TestRestoreJobWork(t *testing.T) {
	at := func(p Progress) int { return p.percent() }
	check := func(read int64) Progress { return restoreWork(Progress{}, read, 1000, false) }
	load := func(read int64) Progress { return restoreWork(Progress{}, read, 1000, true) }
	dump := func(work int64) Progress { return Progress{work: work, workTotal: 100} }
	// With the backup of the current data: check 0-10, backup 10-40, load 40-99.
	steps := []int{
		at(restoreJobWork(check(500), false)), at(restoreJobWork(check(1000), false)),
		at(safetyWork(dump(0))), at(safetyWork(dump(50))), at(safetyWork(dump(100))),
		at(restoreJobWork(load(0), true)), at(restoreJobWork(load(500), true)), at(restoreJobWork(load(1000), true)),
	}
	want := []int{5, 10, 10, 25, 40, 40, 70, 99}
	for i := range want {
		if steps[i] != want[i] {
			t.Fatalf("percents = %v, want %v", steps, want)
		}
	}
	// Without it, the load takes 10-99.
	if got := at(restoreJobWork(load(500), false)); got != 55 {
		t.Errorf("halfway through the load without the backup: %d%%, want 55", got)
	}
}
