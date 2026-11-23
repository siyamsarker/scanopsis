// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"fmt"
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
		go func() { //nolint:gosec // G118: the check is shared by callers and must outlive any one request.
			var err error
			// However the check ends, even by a panic, its result is
			// published and flight is closed, so no caller waits for ever.
			defer func() {
				if v := recover(); v != nil {
					err = fmt.Errorf("readiness check panicked: %v", v)
				}
				c.mu.Lock()
				c.at, c.err, c.flight = time.Now(), err, nil
				c.mu.Unlock()
				close(flight)
			}()
			// The check has its own deadline: it must end even when every
			// caller has gone.
			checkCtx, cancel := context.WithTimeout(context.Background(), readyTimeout)
			defer cancel()
			err = run(checkCtx)
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
