// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultBusyWait is how long a request waits for a free slot before the
	// server answers 503 busy.
	DefaultBusyWait = 3 * time.Second

	// busyRetryAfter is the Retry-After value of a 503 busy answer, in seconds.
	busyRetryAfter = 2

	// clientShareDivisor: one client address may hold at most this fraction
	// of the slots (a quarter, at least one) at the same time.
	clientShareDivisor = 4

	// uploadPath is the Trivy upload. It has its own admission control in the
	// ingest package (memory and per-project limits), so it is not counted here.
	uploadPath = "/api/v1/ingest/trivy"
)

// clientKey identifies a client for the per-client share: an IPv4 address
// whole, an IPv6 address by its /64, as the other limiters do.
func clientKey(a netip.Addr) (netip.Prefix, bool) {
	if !a.IsValid() {
		return netip.Prefix{}, false
	}
	a = a.Unmap()
	bits := 32
	if a.Is6() {
		bits = 64
	}
	p, err := a.Prefix(bits)
	return p, err == nil
}

// clientShares holds one counting semaphore per client with a request in
// progress; an entry exists only while it is used.
type clientShares struct {
	size int
	mu   sync.Mutex
	m    map[netip.Prefix]*clientShare
}

type clientShare struct {
	slots chan struct{}
	refs  int
}

func (c *clientShares) get(k netip.Prefix) *clientShare {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.m[k]
	if e == nil {
		e = &clientShare{slots: make(chan struct{}, c.size)}
		c.m[k] = e
	}
	e.refs++
	return e
}

func (c *clientShares) put(k netip.Prefix, e *clientShare) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e.refs--; e.refs == 0 {
		delete(c.m, k)
	}
}

// errReader returns err on every read.
type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// bufferBody reads the request body into memory, so that a slow client holds
// no slot while it sends. The body is already limited in size by limitBody. A
// read error is not handled here: the handler gets the bytes read so far
// followed by the same error and answers it as it always did.
func bufferBody(r *http.Request) {
	if r.Body == nil || r.Body == http.NoBody {
		return
	}
	data, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	var rd io.Reader = bytes.NewReader(data)
	if err != nil {
		rd = io.MultiReader(rd, errReader{err})
	}
	r.Body = io.NopCloser(rd)
}

// limitConcurrency admits at most Deps.MaxConcurrent database-backed API
// requests at a time, so that requests wait for a slot here, for a bounded
// time, instead of queueing for a pooled database connection without limit.
//
//   - A request with a body is read in full first, so a slot is held only
//     while the server works on the request and never while a client sends.
//   - One client address holds at most a quarter of the slots (at least
//     one), so one client cannot take them all. Behind a proxy that is not in
//     SCANOPSIS_TRUSTED_PROXIES every user has the proxy's address and shares
//     one quarter.
//   - A request that cannot get its share and a slot within Deps.BusyWait is
//     answered 503 busy with Retry-After.
//
// Only requests below /api/ are counted: the probes and the web interface are
// served from outside it, and the Trivy upload is admitted by the ingest
// package. A MaxConcurrent of zero or less disables the limit.
func (s *server) limitConcurrency(next http.Handler) http.Handler {
	if s.MaxConcurrent <= 0 {
		return next
	}
	slots := make(chan struct{}, s.MaxConcurrent)
	shares := &clientShares{size: max(1, s.MaxConcurrent/clientShareDivisor), m: map[netip.Prefix]*clientShare{}}
	wait := s.BusyWait
	if wait <= 0 {
		wait = DefaultBusyWait
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == uploadPath {
			next.ServeHTTP(w, r)
			return
		}
		bufferBody(r)

		var timer *time.Timer
		defer func() {
			if timer != nil {
				timer.Stop()
			}
		}()
		// acquire takes a token of sem, waiting for the one deadline of the
		// request. It returns false when the deadline passed or the client left.
		acquire := func(sem chan struct{}) (ok, gone bool) {
			select {
			case sem <- struct{}{}:
				return true, false
			default:
			}
			if timer == nil {
				timer = time.NewTimer(wait)
			}
			select {
			case sem <- struct{}{}:
				return true, false
			case <-timer.C:
				return false, false
			case <-r.Context().Done():
				return false, true
			}
		}
		busy := func(why string) {
			s.Log.WarnContext(r.Context(), "request rejected: too many concurrent requests",
				"method", r.Method, "path", r.URL.Path, "limit", s.MaxConcurrent, "reason", why, "waited", wait.String())
			w.Header().Set("Retry-After", strconv.Itoa(busyRetryAfter))
			writeError(w, http.StatusServiceUnavailable, "busy", "The server is busy. Try again in a moment.")
		}

		if s.ClientIP != nil {
			if key, ok := clientKey(s.ClientIP(r)); ok {
				share := shares.get(key)
				defer shares.put(key, share)
				switch got, gone := acquire(share.slots); {
				case gone:
					return // the client has gone; nobody reads an answer
				case !got:
					busy("client share")
					return
				}
				defer func() { <-share.slots }()
			}
		}
		switch got, gone := acquire(slots); {
		case gone:
			return
		case !got:
			busy("no free slot")
			return
		}
		defer func() { <-slots }()
		next.ServeHTTP(w, r)
	})
}
