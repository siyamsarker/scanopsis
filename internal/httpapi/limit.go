// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBusyWait is how long a request waits for a free slot before the
	// server answers 503 busy.
	DefaultBusyWait = 3 * time.Second

	// busyRetryAfter is the Retry-After value of a 503 busy answer, in seconds.
	busyRetryAfter = 2

	// uploadPath is the Trivy upload. It has its own admission control in the
	// ingest package (memory and per-project limits), so it is not counted here.
	uploadPath = "/api/v1/ingest/trivy"
)

// limitConcurrency admits at most Deps.MaxConcurrent database-backed API
// requests at a time, so that requests wait for a slot here, for a bounded
// time, instead of queueing for a pooled database connection without limit.
// A request that cannot get a slot within Deps.BusyWait is answered 503 busy
// with Retry-After. Only requests below /api/ are counted: the probes and the
// web interface never touch the database through this path, and the Trivy
// upload is admitted by the ingest package. A MaxConcurrent of zero or less
// disables the limit.
func (s *server) limitConcurrency(next http.Handler) http.Handler {
	if s.MaxConcurrent <= 0 {
		return next
	}
	slots := make(chan struct{}, s.MaxConcurrent)
	wait := s.BusyWait
	if wait <= 0 {
		wait = DefaultBusyWait
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == uploadPath {
			next.ServeHTTP(w, r)
			return
		}
		select {
		case slots <- struct{}{}: // free slot, no timer needed
		default:
			timer := time.NewTimer(wait)
			select {
			case slots <- struct{}{}:
				timer.Stop()
			case <-timer.C:
				s.Log.WarnContext(r.Context(), "request rejected: too many concurrent requests",
					"method", r.Method, "path", r.URL.Path, "limit", s.MaxConcurrent, "waited", wait.String())
				w.Header().Set("Retry-After", strconv.Itoa(busyRetryAfter))
				writeError(w, http.StatusServiceUnavailable, "busy", "The server is busy. Try again in a moment.")
				return
			case <-r.Context().Done():
				timer.Stop()
				return // the client has gone; nobody reads an answer
			}
		}
		defer func() { <-slots }()
		next.ServeHTTP(w, r)
	})
}
