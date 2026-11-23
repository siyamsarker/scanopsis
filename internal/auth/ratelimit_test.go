// Copyright 2026 Siyam Sarker
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
	// A burst of 3, so two uses leave budget and k0 stays in the list.
	l := NewRateLimiter(time.Second, 3, clk.Now)
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
		"2001:db8:1:2:3:4:5:6": "2001:db8:1::/48",
		"2001:db8:1:ffff::1":   "2001:db8:1::/48",
		"2001:db8:2::1":        "2001:db8:2::/48",
	} {
		if got := addrKey(netip.MustParseAddr(in)); got != want {
			t.Errorf("addrKey(%s) = %s; want %s", in, got, want)
		}
	}
}

func TestRateLimiterBlockedDoesNotConsume(t *testing.T) {
	clk := newFakeClock()
	l := NewRateLimiter(10*time.Second, 2, clk.Now)
	if blocked, _ := l.Blocked("k"); blocked {
		t.Fatal("unknown key is blocked")
	}
	if len(l.buckets) != 0 {
		t.Fatal("Blocked created a bucket")
	}
	for range 5 {
		if blocked, _ := l.Blocked("k"); blocked {
			t.Fatal("Blocked consumed tokens")
		}
	}
	l.Allow("k")
	l.Allow("k")
	blocked, wait := l.Blocked("k")
	if !blocked || wait != 10*time.Second {
		t.Fatalf("Blocked = %v, %v; want true, 10s", blocked, wait)
	}
	clk.Advance(4 * time.Second)
	if blocked, wait := l.Blocked("k"); !blocked || wait != 6*time.Second {
		t.Fatalf("Blocked = %v, %v; want true, 6s", blocked, wait)
	}
	// Checking again must not have spent or refilled anything.
	clk.Advance(6 * time.Second)
	if blocked, _ := l.Blocked("k"); blocked {
		t.Fatal("still blocked after a full interval")
	}
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("Allow refused after Blocked reported a free token")
	}
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("Blocked granted an extra token")
	}
}

// spendAll uses up the whole budget of key.
func spendAll(t *testing.T, l *RateLimiter, key string) {
	t.Helper()
	for {
		if ok, _ := l.Allow(key); !ok {
			return
		}
	}
}

func TestRateLimiterFloodDoesNotEvictASpentKey(t *testing.T) {
	clk := newFakeClock()
	l := NewRateLimiter(12*time.Second, 10, clk.Now)
	spendAll(t, l, "victim")
	// More distinct keys than the list holds, each with budget left.
	for i := range 2 * limiterMaxKeys {
		l.Allow(fmt.Sprintf("junk-%d", i))
	}
	if blocked, _ := l.Blocked("victim"); !blocked {
		t.Fatal("a flood of other keys gave the victim a fresh budget")
	}
	if ok, _ := l.Allow("victim"); ok {
		t.Fatal("the victim was allowed after the flood")
	}
	if n := len(l.buckets); n != limiterMaxKeys {
		t.Fatalf("list holds %d keys; want %d", n, limiterMaxKeys)
	}
}

func TestRateLimiterSpentKeyReturnsWhenRefilled(t *testing.T) {
	clk := newFakeClock()
	l := NewRateLimiter(10*time.Second, 2, clk.Now)
	spendAll(t, l, "k")
	if _, ok := l.spent["k"]; !ok {
		t.Fatal("a spent key is not set aside")
	}
	clk.Advance(10 * time.Second)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("refilled token refused")
	}
	// That token spent the budget again.
	if _, ok := l.spent["k"]; !ok {
		t.Fatal("the key left the set while spent")
	}
	clk.Advance(30 * time.Second)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("refilled token refused")
	}
	if _, inList := l.buckets["k"]; !inList {
		t.Fatal("a key with budget left is not back in the list")
	}
	if _, ok := l.spent["k"]; ok {
		t.Fatal("a key with budget left is still set aside")
	}
}

func TestRateLimiterSpentSetIsBoundedAndSwept(t *testing.T) {
	clk := newFakeClock()
	l := NewRateLimiter(time.Second, 1, clk.Now)
	for i := range limiterMaxSpent + 50 {
		spendAll(t, l, fmt.Sprintf("s-%d", i))
	}
	if n := len(l.spent); n != limiterMaxSpent {
		t.Fatalf("set aside %d keys; want at most %d", n, limiterMaxSpent)
	}
	// The overflow stays in the list, still limited.
	if n := len(l.buckets); n != 50 {
		t.Fatalf("list holds %d keys; want the 50 that did not fit", n)
	}
	if ok, _ := l.Allow(fmt.Sprintf("s-%d", limiterMaxSpent+49)); ok {
		t.Fatal("an overflow key was allowed")
	}
	clk.Advance(limiterMinIdle + time.Minute)
	l.Allow("fresh")
	_, freshAside := l.spent["fresh"]
	if len(l.spent)+len(l.buckets) != 1 || !freshAside {
		t.Fatalf("after the idle time: %d set aside, %d listed; want only the fresh key", len(l.spent), len(l.buckets))
	}
}
