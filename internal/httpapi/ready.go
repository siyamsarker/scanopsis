// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"sync"
	"time"
)

// readyCacheTTL is how long the result of a readiness check is reused.
const readyCacheTTL = time.Second

// readyCache runs the real database check at most once per readyCacheTTL.
// Callers within that time get the last result; callers that arrive while a
// check is running share it instead of starting another. A check that hangs
// therefore cannot pile up callers behind it or open more connections: each
// caller waits for its own deadline at most and is answered not ready.
type readyCache struct {
	mu     sync.Mutex
	at     time.Time     // when the last check finished
	err    error         // its result
	flight chan struct{} // closed when the running check finishes; nil when none runs
}

// check returns the cached result, or the result of the one running or new
// check, or ctx's error when ctx ends first.
func (c *readyCache) check(ctx context.Context, run func(context.Context) error) error {
	c.mu.Lock()
	if !c.at.IsZero() && time.Since(c.at) < readyCacheTTL {
		err := c.err
		c.mu.Unlock()
		return err
	}
	if c.flight == nil {
		flight := make(chan struct{})
		c.flight = flight
		go func() {
			// The check has its own deadline: it must end even when every
			// caller has gone.
			checkCtx, cancel := context.WithTimeout(context.Background(), readyTimeout)
			err := run(checkCtx)
			cancel()
			c.mu.Lock()
			c.at, c.err, c.flight = time.Now(), err, nil
			c.mu.Unlock()
			close(flight)
		}()
	}
	flight := c.flight
	c.mu.Unlock()

	select {
	case <-flight:
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
