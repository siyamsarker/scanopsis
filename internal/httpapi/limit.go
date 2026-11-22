// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"os"
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

	// bodyBudget is the most memory the bodies of requests that wait for or
	// hold a slot may take together. A body counts by its Content-Length, or
	// by the largest body allowed when it has none.
	bodyBudget = 32 << 20

	// clientBodyShare: one client address may reserve at most this fraction of
	// the body budget at a time.
	clientBodyShare = 4

	// ingestPrefix holds the Trivy upload. It has its own admission control
	// and body limit in the ingest package (memory and per-project limits),
	// so neither limitBody nor the concurrency limit applies to it.
	ingestPrefix = "/api/v1/ingest/"
	uploadPath   = ingestPrefix + "trivy"
)

// limitedAPIPath reports whether the generic limits of the API apply to a
// path: the body limit of limitBody and the concurrency limit. They apply to
// everything below /api/ except the upload paths, whose handler does its own.
// Both use this one test, so no path is left without either.
func limitedAPIPath(path string) bool {
	return strings.HasPrefix(path, "/api/") && !strings.HasPrefix(path, ingestPrefix)
}

// bodyReadTimeout is how long reading a body may take. A client that has
// reserved memory for a body and does not send it loses the reservation after
// this and is answered 408. A variable so that a test can shorten it.
var bodyReadTimeout = 5 * time.Second

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
	size      int
	bodyBytes int64
	mu        sync.Mutex
	m         map[netip.Prefix]*clientShare
}

type clientShare struct {
	slots chan struct{}
	bytes *byteBudget // the client's own part of the body budget
	refs  int
}

func (c *clientShares) get(k netip.Prefix) *clientShare {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.m[k]
	if e == nil {
		e = &clientShare{slots: make(chan struct{}, c.size), bytes: newByteBudget(c.bodyBytes)}
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

// byteBudget is a weighted semaphore over bytes, a few lines instead of a
// dependency.
type byteBudget struct {
	mu      sync.Mutex
	avail   int64
	changed chan struct{} // closed and replaced whenever bytes are returned
}

func newByteBudget(n int64) *byteBudget {
	return &byteBudget{avail: n, changed: make(chan struct{})}
}

// acquire takes n bytes, waiting until they are free, the deadline (asked for
// only when there is something to wait for) fires or done is closed. n must not exceed the whole budget.
func (b *byteBudget) acquire(n int64, deadline func() <-chan time.Time, done <-chan struct{}) (ok, gone bool) {
	for {
		b.mu.Lock()
		if b.avail >= n {
			b.avail -= n
			b.mu.Unlock()
			return true, false
		}
		changed := b.changed
		b.mu.Unlock()
		select {
		case <-changed:
		case <-deadline():
			return false, false
		case <-done:
			return false, true
		}
	}
}

func (b *byteBudget) release(n int64) {
	b.mu.Lock()
	b.avail += n
	close(b.changed)
	b.changed = make(chan struct{})
	b.mu.Unlock()
}

// bodyWeight is what a request's body counts against the budget: its
// Content-Length, or the largest allowed body when the length is unknown.
func bodyWeight(r *http.Request) int64 {
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return 0
	}
	if r.ContentLength < 0 || r.ContentLength > maxAPIBody {
		return maxAPIBody
	}
	return r.ContentLength
}

// bufferBody reads the request body into memory, so that a slow client holds
// no slot while it sends. It enforces the API's size limit itself: it reports
// a body over maxAPIBody as too large. Any other read error is not handled
// here: the handler gets the bytes read so far followed by the same error and
// answers it as it always did.
func bufferBody(w http.ResponseWriter, r *http.Request) (status int) {
	if r.Body == nil || r.Body == http.NoBody {
		return 0
	}
	// A short deadline of its own for the read, restored for the rest of the
	// request (the server sets it again for the next one). Not every
	// ResponseWriter supports it; the server's own read timeout still applies.
	rc := http.NewResponseController(w)
	if rc.SetReadDeadline(time.Now().Add(bodyReadTimeout)) == nil {
		defer func() { _ = rc.SetReadDeadline(time.Time{}) }()
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAPIBody))
	_ = r.Body.Close()
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return http.StatusRequestEntityTooLarge
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return http.StatusRequestTimeout
	}
	var rd io.Reader = bytes.NewReader(data)
	if err != nil {
		rd = io.MultiReader(rd, errReader{err})
	}
	r.Body = io.NopCloser(rd)
	return 0
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
	shares := &clientShares{
		size: max(1, s.MaxConcurrent/clientShareDivisor), bodyBytes: bodyBudget / clientBodyShare,
		m: map[netip.Prefix]*clientShare{},
	}
	budget := newByteBudget(bodyBudget)
	wait := s.BusyWait
	if wait <= 0 {
		wait = DefaultBusyWait
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !limitedAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if r.ContentLength > maxAPIBody {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "The request body is too large.")
			return
		}

		var timer *time.Timer
		defer func() {
			if timer != nil {
				timer.Stop()
			}
		}()
		// deadline is the one wait of the request, started when it first
		// has to wait for anything.
		deadline := func() <-chan time.Time {
			if timer == nil {
				timer = time.NewTimer(wait)
			}
			return timer.C
		}
		// acquire takes a token of sem, waiting for the one deadline of the
		// request. It returns false when the deadline passed or the client left.
		acquire := func(sem chan struct{}) (ok, gone bool) {
			select {
			case sem <- struct{}{}:
				return true, false
			default:
			}
			select {
			case sem <- struct{}{}:
				return true, false
			case <-deadline():
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

		// The client's share comes first, so one address cannot reserve the
		// slots or the memory of the others with requests that only wait.
		var share *clientShare
		if s.ClientIP != nil {
			if key, ok := clientKey(s.ClientIP(r)); ok {
				share = shares.get(key)
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

		// The body is read, within the memory budget of the client and the
		// global one, before a slot is taken.
		if n := bodyWeight(r); n > 0 {
			if share != nil {
				switch got, gone := share.bytes.acquire(n, deadline, r.Context().Done()); {
				case gone:
					return
				case !got:
					busy("client body memory")
					return
				}
				defer share.bytes.release(n)
			}
			switch got, gone := budget.acquire(n, deadline, r.Context().Done()); {
			case gone:
				return
			case !got:
				busy("body memory")
				return
			}
			defer budget.release(n)
			switch bufferBody(w, r) {
			case http.StatusRequestEntityTooLarge:
				writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "The request body is too large.")
				return
			case http.StatusRequestTimeout:
				writeError(w, http.StatusRequestTimeout, "request_timeout", "The request body did not arrive in time.")
				return
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
