// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"context"
	"math"
	"runtime/debug"
	"sync"
	"time"
)

// memBudget admits uploads by an estimate of the memory each needs. It is a
// weighted semaphore: acquire takes a number of bytes and waits until that
// many are free.
//
// An upload that fits in the free memory is admitted at once, even when
// larger uploads are waiting: a small report must not sit behind a large one
// that waits for a lot of memory to be free. To keep the large ones from
// being starved by a stream of small ones, this stops when the oldest waiter
// has waited fairWait: from then on nothing passes it, new arrivals queue
// behind it, and memory is freed for it as the running uploads end.
type memBudget struct {
	mu       sync.Mutex
	size     int64
	used     int64
	queue    []*budgetWaiter // in order of arrival
	fairWait time.Duration
	now      func() time.Time
}

type budgetWaiter struct {
	n     int64
	since time.Time
	ready chan struct{} // closed when the bytes are granted
}

// defaultFairWait is how long the oldest waiting upload can be passed by
// smaller ones that fit.
const defaultFairWait = 5 * time.Second

func newMemBudget(size int64) *memBudget {
	return &memBudget{size: size, fairWait: defaultFairWait, now: time.Now}
}

// bypassAllowed reports whether an upload may be admitted ahead of the
// waiters: there are none, or the oldest has not yet waited fairWait. The
// caller holds mu.
func (b *memBudget) bypassAllowed() bool {
	return len(b.queue) == 0 || b.now().Sub(b.queue[0].since) < b.fairWait
}

// acquire waits for n bytes, or fails with the context's error. n is cut to
// the size of the budget, so a request larger than the whole budget runs
// alone instead of never. The returned function gives the bytes back; it may
// be called once.
func (b *memBudget) acquire(ctx context.Context, n int64) (release func(), err error) {
	n = min(max(n, 0), b.size)
	b.mu.Lock()
	if b.used+n <= b.size && b.bypassAllowed() {
		b.used += n
		b.mu.Unlock()
		return b.releaser(n), nil
	}
	w := &budgetWaiter{n: n, since: b.now(), ready: make(chan struct{})}
	b.queue = append(b.queue, w)
	b.mu.Unlock()

	select {
	case <-w.ready:
		return b.releaser(n), nil
	case <-ctx.Done():
		b.mu.Lock()
		select {
		case <-w.ready:
			// Granted while the context ended: hand the bytes back, which
			// also wakes the next waiter.
			b.mu.Unlock()
			b.releaser(n)()
		default:
			b.remove(w)
			// The waiter at the head may have been the one blocking others.
			b.grant()
			b.mu.Unlock()
		}
		return nil, ctx.Err()
	}
}

func (b *memBudget) releaser(n int64) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.used -= n
			b.grant()
			b.mu.Unlock()
		})
	}
}

// grant serves the waiters that fit, oldest first. A waiter that does not fit
// is passed by the later ones that do, until the oldest has waited fairWait.
// The caller holds mu.
func (b *memBudget) grant() {
	for i := 0; i < len(b.queue); {
		w := b.queue[i]
		if b.used+w.n <= b.size {
			b.used += w.n
			b.queue = append(b.queue[:i], b.queue[i+1:]...)
			close(w.ready)
			continue
		}
		if !b.bypassAllowed() {
			return
		}
		i++
	}
}

func (b *memBudget) remove(w *budgetWaiter) {
	for i, x := range b.queue {
		if x == w {
			b.queue = append(b.queue[:i], b.queue[i+1:]...)
			return
		}
	}
}

// inUse is the number of bytes granted now.
func (b *memBudget) inUse() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// The estimate of an upload's memory. An upload holds its compressed body
// (about the length of the request for gzip, at most that for plain JSON),
// and the database driver builds a second copy of it to send the report with
// the scan, so the estimate is twice the body. The rest of the parse, the
// batches of findings and the buffers are small and almost independent of the
// size; uploadOverhead covers them with room to spare (measured: about 6 MiB
// per concurrent upload).
const (
	uploadOverhead = 10 << 20
	// defaultMemoryBudget is used when Options.MemoryBudget is 0 and the
	// process has no memory limit (GOMEMLIMIT).
	defaultMemoryBudget = 160 << 20
	// minMemoryBudget is the smallest budget derived from the memory limit.
	minMemoryBudget = 64 << 20
)

// uploadWeight is the estimated memory of an upload with the given
// Content-Length (-1 or 0 when unknown, which assumes the largest body).
func uploadWeight(contentLength, limit int64) int64 {
	n := limit
	if contentLength > 0 && contentLength < limit {
		n = contentLength
	}
	return 2*n + uploadOverhead
}

// budgetFromLimit derives the budget from the memory limit of the process:
// two fifths of it. The rest is for the server itself, the garbage
// collector's headroom (an estimate is exact only for what is live) and the
// buffers the database driver grows while it sends a large report. Eight
// uploads of the largest incompressible reports peaked at 430 MiB of resident
// memory with a budget of half a 400 MiB limit, and at the figure below with
// two fifths. Without a limit it is defaultMemoryBudget.
func budgetFromLimit() int64 {
	limit := debug.SetMemoryLimit(-1)
	if limit == math.MaxInt64 || limit <= 0 {
		return defaultMemoryBudget
	}
	return max(limit/5*2, minMemoryBudget)
}
