// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

/*
 * How far a backup or restore has come. Counting tables alone moves the bar
 * in uneven steps: a few tables hold nearly all the data, so a backup would
 * sit at 90% for most of its run. The share of the work is counted in bytes
 * instead: for a backup, the table data written against an estimate made
 * from the size PostgreSQL stores for each table; for a restore, the bytes of
 * the file read so far.
 */

const (
	// defaultCSVRatio is how many bytes of table data (CSV) one byte that
	// PostgreSQL stores for a table is taken to give, until the tables
	// copied first tell better.
	defaultCSVRatio = 0.7
	// minTableWeight is the least a table counts for, so empty tables
	// still move the bar a little.
	minTableWeight = 8 << 10
	// calibrateAfter is how many stored bytes the finished tables must
	// have before their ratio of data to storage replaces the default.
	calibrateAfter = 1 << 20
	// restoreCheckShare is the share of a restore, in percent, that the
	// check of the whole file takes before the data is loaded; loading
	// the data into PostgreSQL takes far longer than reading the file.
	restoreCheckShare = 10
)

// percent is how far the job has come, from 0 to 99: by work when it is
// known, else by tables. A job is at 100 only once it has finished.
func (p Progress) percent() int {
	switch {
	case p.workTotal > 0:
		return int(min(max(p.work, 0)*100/p.workTotal, 99))
	case p.TablesTotal > 0:
		return min(p.TablesDone*100/p.TablesTotal, 99)
	}
	return 0
}

// backupEstimate weighs the tables of a backup by the size PostgreSQL
// stores for each of them.
type backupEstimate struct {
	weights []int64
	total   int64
}

// newBackupEstimate returns the estimate for tables of the stored sizes,
// in the order the backup copies them.
func newBackupEstimate(sizes []int64) *backupEstimate {
	e := &backupEstimate{weights: make([]int64, len(sizes))}
	for i, s := range sizes {
		e.weights[i] = max(s, minTableWeight)
		e.total += e.weights[i]
	}
	return e
}

// apply sets the work of p: the weights of the finished tables, and the
// share of the current table's weight that its data written so far makes
// of the data it is expected to give. The expectation uses the ratio of
// data to storage of the finished tables once they are large enough,
// which stays the same while one table is copied, so the work never goes
// back. A table that gives more than expected stays just short of its end.
func (e *backupEstimate) apply(p Progress) Progress {
	k := min(max(p.TablesDone, 0), len(e.weights))
	var doneWeight int64
	for _, w := range e.weights[:k] {
		doneWeight += w
	}
	work := doneWeight
	if k < len(e.weights) {
		ratio := defaultCSVRatio
		if written := p.Bytes - p.current; doneWeight >= calibrateAfter && written > 0 {
			ratio = float64(written) / float64(doneWeight)
		}
		share := min(float64(p.current)/(float64(e.weights[k])*ratio), 0.99)
		work += int64(share * float64(e.weights[k]))
	}
	p.work, p.workTotal = work, e.total
	return p
}

// restoreWork sets the work of p for a restore of a file of size bytes of
// which read bytes were read: the check of the whole file makes the first
// restoreCheckShare percent, loading the data (loading true) the rest.
func restoreWork(p Progress, read, size int64, loading bool) Progress {
	if size <= 0 {
		return p
	}
	read = min(max(read, 0), size)
	p.work, p.workTotal = read*restoreCheckShare, size*100
	if loading {
		p.work = size*restoreCheckShare + read*(100-restoreCheckShare)
	}
	return p
}

// jobScale is the work of a whole restore job, so its bar runs once from 0
// to 100 through all its parts.
const jobScale = 1_000_000

// safetyShare is where the backup of the current data before a restore
// ends, in percent: it runs from restoreCheckShare to here, and the load of
// the backup takes the rest.
const safetyShare = 40

// fraction is how far p has come, from 0 to 1: by work when it is known,
// else by tables.
func (p Progress) fraction() float64 {
	switch {
	case p.workTotal > 0:
		return min(max(float64(p.work)/float64(p.workTotal), 0), 1)
	case p.TablesTotal > 0:
		return min(max(float64(p.TablesDone)/float64(p.TablesTotal), 0), 1)
	}
	return 0
}

// span puts the fraction f into the part of the job scale from lo to hi
// percent.
func span(p Progress, f float64, lo, hi int64) Progress {
	p.work = lo*jobScale/100 + int64(f*float64((hi-lo)*jobScale/100))
	p.workTotal = jobScale
	return p
}

// restoreJobWork puts the progress p of Restore (see restoreWork) onto the
// scale of the whole restore job: the check of the file keeps the first
// restoreCheckShare percent; the load takes the rest, or what is left
// after the backup of the current data when one was taken (safety).
func restoreJobWork(p Progress, safety bool) Progress {
	f := p.fraction()
	check := float64(restoreCheckShare) / 100
	if f < check {
		return span(p, f/check, 0, restoreCheckShare)
	}
	from := int64(restoreCheckShare)
	if safety {
		from = safetyShare
	}
	return span(p, (f-check)/(1-check), from, 100)
}

// safetyWork puts the progress p of the backup of the current data before
// a restore onto the scale of the whole restore job.
func safetyWork(p Progress) Progress {
	return span(p, p.fraction(), restoreCheckShare, safetyShare)
}
