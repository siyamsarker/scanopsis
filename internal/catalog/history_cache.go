// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

const (
	// historyGrace is the least time between two computations of the
	// history while a previous one is there to serve: after the data
	// changes, the last good history is served until it is this old, so a
	// burst of uploads costs one computation per grace period at most.
	historyGrace = 30 * time.Second
	// historyMaxAge bounds how long a history is served while the data it
	// was computed from looks unchanged. Changes the fingerprint does not
	// see (retention removing old scans, the day boundaries moving) show
	// within it.
	historyMaxAge = 2 * time.Minute
	// historyTimeout bounds one computation, which runs detached from the
	// request that started it, so it never holds a pool connection for long.
	historyTimeout = 20 * time.Second
)

// fingerprintSQL changes whenever a scan is stored (an upload, including a
// replaced one, inserts a new row with a higher id) and whenever a service
// is added, removed, archived or given another dashboard branch. It reads
// one index entry and the services table.
const fingerprintSQL = `
SELECT (SELECT COALESCE(max(id), 0) FROM scans)::text || ':' ||
       COALESCE((SELECT md5(string_agg(id::text || chr(1) || archived::text || chr(1) || COALESCE(default_ref, chr(2)),
                                       chr(3) ORDER BY id))
                 FROM services), '')`

// historyCache keeps the last good history, encoded, and runs at most one
// computation at a time: the history reads tens of thousands of scan rows,
// and computations side by side would compete for the same CPU and
// connections.
//
// A request is served the cached history while the data fingerprint is
// unchanged and the history is younger than maxAge, and also, whatever the
// fingerprint, while the last computation started less than grace ago or
// one is running. Otherwise it starts a computation, or joins the running
// one when there is nothing cached, and waits for it as long as its own
// context allows. The computation itself is bounded by timeout.
type historyCache struct {
	// Set once by newHistoryCache; tests replace them before first use.
	fingerprint func(context.Context) (string, error)
	compute     func(context.Context) ([]byte, error)
	now         func() time.Time
	grace       time.Duration
	maxAge      time.Duration
	timeout     time.Duration

	mu        sync.Mutex
	body      []byte    // last good history, never modified once stored
	bodyPrint string    // fingerprint read before body was computed
	bodyAt    time.Time // when the computation of body started
	startedAt time.Time // when the latest computation started
	running   *historyFill
}

// historyFill is a computation in progress; done is closed when it ends.
type historyFill struct {
	done chan struct{}
	body []byte
	err  error
}

func newHistoryCache(c *Catalog) *historyCache {
	return &historyCache{
		fingerprint: func(ctx context.Context) (string, error) {
			var fp string
			err := c.pool.QueryRow(ctx, fingerprintSQL).Scan(&fp)
			return fp, err
		},
		compute: func(ctx context.Context) ([]byte, error) {
			out, err := c.loadHistory(ctx, time.Now().UTC())
			if err != nil {
				return nil, err
			}
			body, err := json.Marshal(out)
			if err != nil {
				return nil, err
			}
			// As json.Encoder ends every other response.
			return append(body, '\n'), nil
		},
		now:     time.Now,
		grace:   historyGrace,
		maxAge:  historyMaxAge,
		timeout: historyTimeout,
	}
}

// get returns the encoded history, following the rules of historyCache.
func (h *historyCache) get(ctx context.Context) ([]byte, error) {
	fp, err := h.fingerprint(ctx)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	now := h.now()
	if h.body != nil {
		current := fp == h.bodyPrint && now.Sub(h.bodyAt) < h.maxAge
		if current || h.running != nil || now.Sub(h.startedAt) < h.grace {
			body := h.body
			h.mu.Unlock()
			return body, nil
		}
	}
	f := h.running
	if f == nil {
		f = h.start(ctx, fp, now)
	}
	h.mu.Unlock()
	select {
	case <-f.done:
		return f.body, f.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// start runs one computation in the background. It is called with mu held.
func (h *historyCache) start(parent context.Context, fp string, now time.Time) *historyFill {
	f := &historyFill{done: make(chan struct{})}
	h.running, h.startedAt = f, now
	// Detached from the request that starts it (keeping its values for the
	// log), so a caller that gives up does not cancel it for the others,
	// and bounded on its own.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), h.timeout)
	go func() {
		defer cancel()
		f.body, f.err = h.compute(ctx)
		h.mu.Lock()
		if f.err == nil {
			h.body, h.bodyPrint, h.bodyAt = f.body, fp, now
		}
		h.running = nil
		h.mu.Unlock()
		close(f.done)
	}()
	return f
}
