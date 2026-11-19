// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"
)

func TestRateLimiterBurstThenDeny(t *testing.T) {
	clk := newFakeClock()
	l := NewRateLimiter(12*time.Second, 3, clk.Now)
	for i := range 3 {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("attempt %d denied within burst", i+1)
		}
	}
	ok, wait := l.Allow("a")
	if ok || wait != 12*time.Second {
		t.Fatalf("4th attempt = %v, %v; want denied, 12s", ok, wait)
	}
	// Other keys are unaffected.
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("independent key denied")
	}
}

func TestRateLimiterRefillAndRetryAfter(t *testing.T) {
	clk := newFakeClock()
	l := NewRateLimiter(12*time.Second, 1, clk.Now)
	l.Allow("a")
	clk.Advance(5 * time.Second)
	ok, wait := l.Allow("a")
	if ok || wait != 7*time.Second {
		t.Fatalf("after 5s = %v, %v; want denied, 7s", ok, wait)
	}
	clk.Advance(7 * time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("denied after waiting the advertised time")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("a refilled bucket must not exceed one token")
	}
}

func TestRateLimiterBurstCapsRefill(t *testing.T) {
	clk := newFakeClock()
	l := NewRateLimiter(time.Second, 2, clk.Now)
	l.Allow("a")
	clk.Advance(time.Hour)
	allowed := 0
	for range 5 {
		if ok, _ := l.Allow("a"); ok {
			allowed++
		}
	}
	if allowed != 2 {
		t.Fatalf("allowed %d after a long idle period; want the burst of 2", allowed)
	}
}

func TestRateLimiterEvictsIdleKeys(t *testing.T) {
	clk := newFakeClock()
	l := NewRateLimiter(time.Second, 2, clk.Now)
	for i := range 100 {
		l.Allow(fmt.Sprintf("k%d", i))
	}
	if n := len(l.buckets); n != 100 {
		t.Fatalf("tracking %d keys; want 100", n)
	}
	clk.Advance(5 * time.Minute)
	l.Allow("fresh") // sweeps, but nothing is idle for 10 minutes yet
	if n := len(l.buckets); n != 101 {
		t.Fatalf("tracking %d keys after 5 minutes; want 101", n)
	}
	clk.Advance(6 * time.Minute)
	l.Allow("fresh")
	if n := len(l.buckets); n != 1 {
		t.Fatalf("tracking %d keys after 11 minutes; want only the active one", n)
	}
}

func TestRateLimiterSweepsAtMostOncePerMinute(t *testing.T) {
	clk := newFakeClock()
	l := NewRateLimiter(time.Second, 2, clk.Now)
	l.Allow("old")
	clk.Advance(9 * time.Minute)
	l.Allow("x") // sweep runs: "old" is not idle long enough
	clk.Advance(90 * time.Second)
	l.Allow("y") // next sweep, "old" is now 10.5 minutes idle
	if _, ok := l.buckets["old"]; ok {
		t.Fatal("idle key survived a sweep")
	}
	l.Allow("late")
	clk.Advance(30 * time.Second)
	l.buckets["stale"] = l.order.PushBack(&bucket{key: "stale", last: clk.Now().Add(-time.Hour)})
	l.Allow("z") // within a minute of the last sweep: no sweep
	if _, ok := l.buckets["stale"]; !ok {
		t.Fatal("sweep ran more than once a minute")
	}
}

func TestRateLimiterNeverEvictsAnEmptyBucket(t *testing.T) {
	clk := newFakeClock()
	// A bucket needs 20 minutes to refill, longer than the minimum idle time.
	l := NewRateLimiter(10*time.Minute, 2, clk.Now)
	l.Allow("a")
	l.Allow("a")
	clk.Advance(15 * time.Minute)
	l.Allow("other")
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("expected one refilled token after 15 minutes")
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("bucket was evicted early and handed out free tokens")
	}
}

func TestRateLimiterNilClockAndConcurrency(t *testing.T) {
	l := NewRateLimiter(time.Hour, 50, nil)
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.Allow("shared"); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 50 {
		t.Fatalf("allowed %d concurrent attempts; want exactly the burst of 50", allowed)
	}
}

func TestRateLimiterIsBoundedUnderAFlood(t *testing.T) {
	clk := newFakeClock()
	l := NewRateLimiter(time.Second, 2, clk.Now)
	for i := range 20_000 {
		l.Allow(fmt.Sprintf("flood%d", i))
		clk.Advance(time.Millisecond)
	}
	if n, m := len(l.buckets), l.order.Len(); n != limiterMaxKeys || m != limiterMaxKeys {
		t.Fatalf("tracking %d keys (list %d); want %d", n, m, limiterMaxKeys)
	}
	if _, ok := l.buckets["flood0"]; ok {
		t.Fatal("the oldest key was kept")
	}
	if _, ok := l.buckets["flood19999"]; !ok {
		t.Fatal("the newest key was dropped")
	}
}

func TestRateLimiterEvictsTheLeastRecentlyUsedKey(t *testing.T) {
	clk := newFakeClock()
	l := NewRateLimiter(time.Second, 2, clk.Now)
	for i := range limiterMaxKeys {
		l.Allow(fmt.Sprintf("k%d", i))
	}
	l.Allow("k0") // refreshed: k1 is now the least recently used
	l.Allow("new")
	if _, ok := l.buckets["k0"]; !ok {
		t.Fatal("a recently used key was evicted")
	}
	if _, ok := l.buckets["k1"]; ok {
		t.Fatal("the least recently used key was kept")
	}
}

func TestAddrKey(t *testing.T) {
	for in, want := range map[string]string{
		"192.0.2.7":            "192.0.2.7",
		"::ffff:192.0.2.7":     "192.0.2.7",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::1": "2001:db8:1:2::/64",
		"2001:db8:1:3::1":      "2001:db8:1:3::/64",
	} {
		if got := addrKey(netip.MustParseAddr(in)); got != want {
			t.Errorf("addrKey(%s) = %s; want %s", in, got, want)
		}
	}
}
