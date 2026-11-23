// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// unavailableRetryAfter is the Retry-After value, in seconds, of a response
// for an unreachable database. A restarted database is usually back within
// half a minute.
const unavailableRetryAfter = 30

// isUnavailable reports whether err comes from a database that could not be
// reached or could not take the work now, as opposed to a bug or a rejected
// statement. The client is not at fault and a later attempt is likely to
// work, so it is told to retry instead of that something went wrong.
//
// ctx is the request's context: its own end (a client that went away) is not
// the database's fault, but any other deadline is, for example the time the
// pool waits for a free connection.
func isUnavailable(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	// A report that cannot be read is not a database problem, whatever the
	// error wraps.
	for _, report := range []error{ErrInvalidJSON, ErrNotTrivy, ErrTooManyFindings, errTooLarge, errTrailingData} {
		if errors.Is(err, report) {
			return false
		}
	}
	var connect *pgconn.ConnectError
	var pg *pgconn.PgError
	var netErr net.Error
	switch {
	case errors.As(err, &connect):
		return true
	case errors.Is(err, context.DeadlineExceeded):
		return ctx.Err() == nil
	case errors.Is(err, context.Canceled):
		return false
	case errors.As(err, &pg):
		// 08: connection exception, 57P01 to 57P03: the server is shutting
		// down or starting, 53300: too many connections.
		return strings.HasPrefix(pg.Code, "08") || pg.Code == "57P01" || pg.Code == "57P02" || pg.Code == "57P03" || pg.Code == "53300"
	case errors.As(err, &netErr), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		// The connection broke while the work was running.
		return true
	}
	// pgx and its pool report a closed connection and a closed pool (the
	// server is stopping) only as text.
	msg := err.Error()
	return strings.Contains(msg, "conn closed") || strings.Contains(msg, "closed pool")
}
