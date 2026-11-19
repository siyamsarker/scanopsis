// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"container/list"
	"math"
	"net/netip"
	"sync"
	"time"
)

const (
	limiterMinIdle       = 10 * time.Minute
	limiterSweepInterval = time.Minute
	// limiterMaxKeys bounds the memory of a limiter under a flood of distinct keys.
	limiterMaxKeys = 10_000
)

// RateLimiter is a per-key token bucket: each key may make burst requests at
// once and then one more every interval. It tracks at most limiterMaxKeys
// keys: when full, the least recently used key is dropped. It is safe for
// concurrent use.
type RateLimiter struct {
	every time.Duration
	burst float64
	idle  time.Duration
	now   func() time.Time

	mu        sync.Mutex
	buckets   map[string]*list.Element // values are *bucket
	order     *list.List               // front = most recently used
	lastSweep time.Time
}

type bucket struct {
	key    string
	tokens float64
	last   time.Time
}

// NewRateLimiter returns a limiter allowing burst requests per key and then
// one every interval. A nil now uses time.Now. The sign-in endpoint uses
// NewRateLimiter(12*time.Second, 10, nil) for each of its two limiters: ten
// attempts, then five a minute.
func NewRateLimiter(every time.Duration, burst int, now func() time.Time) *RateLimiter {
	if now == nil {
		now = time.Now
	}
	// A bucket idle for this long is full again, so dropping it loses nothing.
	idle := max(limiterMinIdle, every*time.Duration(burst))
	return &RateLimiter{
		every:   every,
		burst:   float64(burst),
		idle:    idle,
		now:     now,
		buckets: make(map[string]*list.Element),
		order:   list.New(),
	}
}

// Allow consumes one token for key. If none is available it returns false
// and how long to wait until one is.
func (l *RateLimiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)

	var b *bucket
	if el := l.buckets[key]; el != nil {
		l.order.MoveToFront(el)
		b = el.Value.(*bucket)
	} else {
		if len(l.buckets) >= limiterMaxKeys {
			oldest := l.order.Back()
			delete(l.buckets, oldest.Value.(*bucket).key)
			l.order.Remove(oldest)
		}
		b = &bucket{key: key, tokens: l.burst, last: now}
		l.buckets[key] = l.order.PushFront(b)
	}
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = min(l.burst, b.tokens+float64(elapsed)/float64(l.every))
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration(math.Ceil((1 - b.tokens) * float64(l.every)))
}

// sweep drops idle keys, at most once per limiterSweepInterval. Keys are
// ordered by last use, so it stops at the first one that is not idle. The
// caller holds l.mu.
func (l *RateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < limiterSweepInterval {
		return
	}
	l.lastSweep = now
	for el := l.order.Back(); el != nil; el = l.order.Back() {
		b := el.Value.(*bucket)
		if now.Sub(b.last) <= l.idle {
			return
		}
		delete(l.buckets, b.key)
		l.order.Remove(el)
	}
}

// addrKey is the rate-limit key for a client address: IPv4 addresses are
// used whole, IPv6 addresses by their /64 prefix, because one client usually
// controls a whole /64.
func addrKey(a netip.Addr) string {
	a = a.Unmap()
	if a.Is4() {
		return a.String()
	}
	return netip.PrefixFrom(a, 64).Masked().String()
}
