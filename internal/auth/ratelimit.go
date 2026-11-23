// Copyright 2026 Siyam Sarker
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
	// limiterMaxSpent bounds the keys kept aside because their budget is spent.
	limiterMaxSpent = 10_000
)

// RateLimiter is a per-key token bucket: each key may make burst requests at
// once and then one more every interval. It is safe for concurrent use.
//
// Memory is bounded. Keys that still have budget are kept in a list of at
// most limiterMaxKeys, from which the least recently used key is dropped to
// make room. A key whose budget is spent moves to a separate set of at most
// limiterMaxSpent keys that new keys never push out, so a flood of other
// keys cannot hand a spent key a fresh budget; it leaves that set when it
// has refilled. Only when that set is full does a spent key stay in the list.
type RateLimiter struct {
	every time.Duration
	burst float64
	idle  time.Duration
	now   func() time.Time

	mu        sync.Mutex
	buckets   map[string]*list.Element // keys with budget left; values are *bucket
	order     *list.List               // front = most recently used
	spent     map[string]*bucket       // keys whose budget is spent
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
		spent:   make(map[string]*bucket),
	}
}

// Allow consumes one token for key. If none is available it returns false
// and how long to wait until one is.
func (l *RateLimiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)

	b := l.get(key, now)
	if b.tokens >= 1 {
		b.tokens--
	} else {
		retryAfter = time.Duration(math.Ceil((1 - b.tokens) * float64(l.every)))
	}
	if b.tokens < 1 {
		l.setAside(b)
	}
	return retryAfter == 0, retryAfter
}

// get returns the refilled bucket of key, creating it in the list if it is
// new. A spent bucket that has refilled goes back to the list. The caller
// holds l.mu.
func (l *RateLimiter) get(key string, now time.Time) *bucket {
	if b := l.spent[key]; b != nil {
		l.refill(b, now)
		if b.tokens >= 1 {
			delete(l.spent, key)
			l.push(b)
		}
		return b
	}
	if el := l.buckets[key]; el != nil {
		l.order.MoveToFront(el)
		b := el.Value.(*bucket)
		l.refill(b, now)
		return b
	}
	b := &bucket{key: key, tokens: l.burst, last: now}
	l.push(b)
	return b
}

// push adds b to the front of the list, dropping the least recently used key
// if the list is full. The list holds no spent bucket unless the set of
// spent keys was full. The caller holds l.mu.
func (l *RateLimiter) push(b *bucket) {
	if len(l.buckets) >= limiterMaxKeys {
		oldest := l.order.Back()
		delete(l.buckets, oldest.Value.(*bucket).key)
		l.order.Remove(oldest)
	}
	l.buckets[b.key] = l.order.PushFront(b)
}

// setAside moves a spent bucket out of the list into the set that new keys
// never push out, if it is in the list and the set has room. The caller
// holds l.mu.
func (l *RateLimiter) setAside(b *bucket) {
	el := l.buckets[b.key]
	if el == nil || len(l.spent) >= limiterMaxSpent {
		return
	}
	delete(l.buckets, b.key)
	l.order.Remove(el)
	l.spent[b.key] = b
}

func (l *RateLimiter) refill(b *bucket, now time.Time) {
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = min(l.burst, b.tokens+float64(elapsed)/float64(l.every))
		b.last = now
	}
}

// Blocked reports whether Allow would refuse key right now, and how long until
// it would not, without consuming a token or creating a bucket.
func (l *RateLimiter) Blocked(key string) (blocked bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.spent[key]
	if b == nil {
		el := l.buckets[key]
		if el == nil {
			return false, 0
		}
		b = el.Value.(*bucket)
	}
	tokens := b.tokens
	if elapsed := l.now().Sub(b.last); elapsed > 0 {
		tokens = min(l.burst, tokens+float64(elapsed)/float64(l.every))
	}
	if tokens >= 1 {
		return false, 0
	}
	return true, time.Duration(math.Ceil((1 - tokens) * float64(l.every)))
}

// sweep drops idle keys, at most once per limiterSweepInterval. A key idle
// for l.idle has a full bucket again, so dropping it loses nothing. The list
// is ordered by last use, so its walk stops at the first key that is not
// idle. The caller holds l.mu.
func (l *RateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < limiterSweepInterval {
		return
	}
	l.lastSweep = now
	for el := l.order.Back(); el != nil; el = l.order.Back() {
		b := el.Value.(*bucket)
		if now.Sub(b.last) <= l.idle {
			break
		}
		delete(l.buckets, b.key)
		l.order.Remove(el)
	}
	for key, b := range l.spent {
		if now.Sub(b.last) > l.idle {
			delete(l.spent, key)
		}
	}
}

// addrKey is the rate-limit key of a client address for sign-in and setup:
// IPv4 addresses are used whole, IPv6 addresses by their /48 prefix. One
// client often controls a /56 or a whole /48, and a narrower key would give
// it thousands of separate budgets.
func addrKey(a netip.Addr) string {
	a = a.Unmap()
	if a.Is4() {
		return a.String()
	}
	return netip.PrefixFrom(a, 48).Masked().String()
}
