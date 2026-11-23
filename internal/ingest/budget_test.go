// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"context"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clockedBudget is a budget whose clock a test moves.
func clockedBudget(size int64) (*memBudget, *testClock) {
	c := newTestClock()
	b := newMemBudget(size)
	b.now = c.now
	return b, c
}

func waitQueued(t *testing.T, b *memBudget, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		b.mu.Lock()
		got := len(b.queue)
		b.mu.Unlock()
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d waiters, want %d", got, n)
		}
		time.Sleep(time.Millisecond)
	}
}

func acquireAsync(b *memBudget, n int64) <-chan func() {
	out := make(chan func(), 1)
	go func() {
		rel, err := b.acquire(context.Background(), n)
		if err != nil {
			close(out)
			return
		}
		out <- rel
	}()
	return out
}

func admitted(c <-chan func()) bool {
	select {
	case <-c:
		return true
	case <-time.After(50 * time.Millisecond):
		return false
	}
}

// A small upload that fits is admitted while a large one waits.
func TestBudgetSmallPassesWaitingLarge(t *testing.T) {
	b, _ := clockedBudget(100)
	running, _ := b.acquire(context.Background(), 70)
	large := acquireAsync(b, 60) // needs 60, 30 free
	waitQueued(t, b, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rel, err := b.acquire(ctx, 10)
	if err != nil {
		t.Fatalf("a small upload that fits waited behind a large one: %v", err)
	}
	if got := b.inUse(); got != 80 {
		t.Errorf("in use %d, want 80", got)
	}
	if admitted(large) {
		t.Fatal("the large upload was admitted beyond the budget")
	}
	rel()
	running()
	if !admitted(large) {
		t.Fatal("the large upload was not admitted once memory was free")
	}
}

// u is the size unit of the fairness tests: 10 units are larger than
// smallUpload, so those uploads are subject to the fairness bound.
const u = 2 << 20

// After the oldest waiter has waited the fairness bound, uploads that fit
// but are not small queue behind it. Sizes are in units of u, so that a unit
// of 10 is above smallUpload.
func TestBudgetSmallQueuesAfterFairnessBound(t *testing.T) {
	b, clock := clockedBudget(100 * u)
	running, _ := b.acquire(context.Background(), 70*u)
	large := acquireAsync(b, 60*u)
	waitQueued(t, b, 1)
	clock.advance(defaultFairWait)
	small := acquireAsync(b, 10*u)
	waitQueued(t, b, 2)
	if admitted(small) {
		t.Fatal("a small upload passed a waiter that exceeded the fairness bound")
	}
	if b.inUse() != 70*u {
		t.Errorf("in use %d, want %d", b.inUse(), 70*u)
	}
	// The running upload ends: the large one goes first, then the small one.
	running()
	relLarge := <-large
	if !admitted(small) {
		t.Fatal("the small upload was not admitted after the large one")
	}
	relLarge()
}

// A stream of small uploads cannot starve a large one.
func TestBudgetLargeIsNotStarved(t *testing.T) {
	b, clock := clockedBudget(100 * u)
	var small []func()
	for range 9 {
		rel, _ := b.acquire(context.Background(), 10*u)
		small = append(small, rel)
	}
	large := acquireAsync(b, 50*u) // 90 in use, 10 free
	waitQueued(t, b, 1)
	// Within the bound, small ones keep passing it.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rel, err := b.acquire(ctx, 10*u)
	if err != nil {
		t.Fatalf("a small upload that fits waited behind a large one: %v", err)
	}
	small = append(small, rel)
	clock.advance(defaultFairWait + time.Second)
	// Now new small ones queue, and each release frees memory for the large.
	more := acquireAsync(b, 10*u)
	waitQueued(t, b, 2)
	for _, rel := range small[:5] {
		rel()
	}
	if !admitted(large) {
		t.Fatal("the large upload was starved")
	}
	for _, rel := range small[5:] {
		rel()
	}
	if !admitted(more) {
		t.Fatal("the queued small upload was not admitted after the large one")
	}
}

// A small upload that fits passes waiting uploads even after the fairness
// bound: it holds its memory only briefly. smallUpload is 9 units.
func TestBudgetSmallAlwaysPassesWhenItFits(t *testing.T) {
	b, clock := clockedBudget(100 * u)
	runA, _ := b.acquire(context.Background(), 50*u)
	runB, _ := b.acquire(context.Background(), 42*u) // 8 units free: less than smallUpload
	large := acquireAsync(b, 60*u)
	waitQueued(t, b, 1)
	clock.advance(defaultFairWait + time.Second)
	small := acquireAsync(b, smallUpload)
	waitQueued(t, b, 2)
	// 50 units free: the large one still does not fit, the small one does
	// and is granted although the large one waited past the bound.
	runB()
	if !admitted(small) {
		t.Fatal("a queued small upload was not granted while a large one stays blocked")
	}
	if admitted(large) {
		t.Fatal("the large upload was admitted beyond the budget")
	}
	// A new small upload that fits is admitted at once.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rel, err := b.acquire(ctx, smallUpload)
	if err != nil {
		t.Fatalf("a small upload waited behind a large one after the fairness bound: %v", err)
	}
	rel()
	runA()
	if !admitted(large) {
		t.Fatal("the large upload was not admitted once memory was free")
	}
}

// The budget is never exceeded under concurrent load.
func TestBudgetNeverExceeded(t *testing.T) {
	const size = 100
	b := newMemBudget(size)
	b.fairWait = 2 * time.Millisecond
	var inUse atomic.Int64
	var admittedN atomic.Int64
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for range 200 {
				n := int64(1 + rng.Intn(60))
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(rng.Intn(5)+1)*time.Millisecond)
				rel, err := b.acquire(ctx, n)
				cancel()
				if err != nil {
					continue
				}
				admittedN.Add(1)
				if cur := inUse.Add(n); cur > size {
					t.Errorf("%d bytes held, budget %d", cur, size)
				}
				time.Sleep(time.Duration(rng.Intn(300)) * time.Microsecond)
				inUse.Add(-n)
				rel()
			}
		}()
	}
	wg.Wait()
	if b.inUse() != 0 {
		t.Errorf("%d bytes still held", b.inUse())
	}
	b.mu.Lock()
	left := len(b.queue)
	b.mu.Unlock()
	if left != 0 {
		t.Errorf("%d waiters left", left)
	}
	if admittedN.Load() == 0 {
		t.Error("nothing was admitted")
	}
}
