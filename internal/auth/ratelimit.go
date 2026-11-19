// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"math"
	"sync"
	"time"
)

const (
	limiterMinIdle       = 10 * time.Minute
	limiterSweepInterval = time.Minute
)

// RateLimiter is a per-key token bucket: each key may make burst requests at
// once and then one more every interval. It is safe for concurrent use.
type RateLimiter struct {
	every time.Duration
	burst float64
	idle  time.Duration
	now   func() time.Time

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter returns a limiter allowing burst requests per key and then
// one every interval. A nil now uses time.Now. The sign-in endpoint uses
// NewRateLimiter(12*time.Second, 10, nil): ten attempts, then five a minute.
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
		buckets: make(map[string]*bucket),
	}
}

// Allow consumes one token for key. If none is available it returns false
// and how long to wait until one is.
func (l *RateLimiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)

	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
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

// sweep drops idle keys, at most once per limiterSweepInterval. The caller
// holds l.mu.
func (l *RateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < limiterSweepInterval {
		return
	}
	l.lastSweep = now
	for k, b := range l.buckets {
		if now.Sub(b.last) > l.idle {
			delete(l.buckets, k)
		}
	}
}
