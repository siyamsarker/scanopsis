// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsUnavailable(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	live := context.Background()
	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"nil", live, nil, false},
		{"connect error", live, fmt.Errorf("acquire connection: %w", &pgconn.ConnectError{Config: &pgconn.Config{}}), true},
		{"network error", live, fmt.Errorf("insert scan: %w", &net.OpError{Op: "read", Err: errors.New("connection reset by peer")}), true},
		{"unexpected eof", live, fmt.Errorf("insert scan: %w", io.ErrUnexpectedEOF), true},
		{"eof", live, fmt.Errorf("lock project: %w", io.EOF), true},
		{"conn closed", live, errors.New("lock project: conn closed"), true},
		{"closed pool", live, errors.New("acquire connection: closed pool"), true},
		{"pool acquire deadline", live, fmt.Errorf("acquire connection: %w", context.DeadlineExceeded), true},
		{"request deadline", mustDeadlineCtx(t), fmt.Errorf("insert scan: %w", context.DeadlineExceeded), false},
		{"request cancelled", cancelled, fmt.Errorf("insert scan: %w", context.Canceled), false},
		{"cancelled error on a live request", live, context.Canceled, false},
		{"server shutdown", live, &pgconn.PgError{Code: "57P01"}, true},
		{"starting up", live, &pgconn.PgError{Code: "57P03"}, true},
		{"connection exception", live, &pgconn.PgError{Code: "08006"}, true},
		{"too many connections", live, &pgconn.PgError{Code: "53300"}, true},
		{"unique violation", live, &pgconn.PgError{Code: "23505"}, false},
		{"syntax error", live, &pgconn.PgError{Code: "42601"}, false},
		{"a bug", live, errors.New("boom"), false},
		{"report error with a network error inside", live, fmt.Errorf("%w: %w", ErrInvalidJSON, io.ErrUnexpectedEOF), false},
		{"too large", live, errTooLarge, false},
	}
	for _, tc := range tests {
		if got := isUnavailable(tc.ctx, tc.err); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func mustDeadlineCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	t.Cleanup(cancel)
	<-ctx.Done()
	return ctx
}

// A database that cannot be reached is answered 503 unavailable with
// Retry-After, is logged, is not audited and is not described to the client;
// other failures stay 500.
func TestIngestDatabaseOutageIs503(t *testing.T) {
	body := []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)
	for name, err := range map[string]error{
		"connect":  fmt.Errorf("acquire connection: %w", &pgconn.ConnectError{Config: &pgconn.Config{Host: "secret-host"}}),
		"shutdown": fmt.Errorf("lock project: %w", &pgconn.PgError{Code: "57P01", Message: "terminating connection"}),
		"timeout":  fmt.Errorf("acquire connection: %w", context.DeadlineExceeded),
	} {
		t.Run(name, func(t *testing.T) {
			e := newHEnv(t, nil)
			e.save.err = err
			w := e.do(req{body: body})
			if w.Code != http.StatusServiceUnavailable || errCode(t, w) != "unavailable" || w.Header().Get("Retry-After") != "30" {
				t.Fatalf("%d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body)
			}
			if strings.Contains(w.Body.String(), "secret-host") || strings.Contains(w.Body.String(), "57P01") {
				t.Errorf("response shows the cause: %s", w.Body)
			}
			if !strings.Contains(e.logs.String(), "the database is not available") {
				t.Errorf("not logged: %s", e.logs)
			}
			if ev := e.audit.list(); len(ev) != 0 {
				t.Errorf("audited: %+v", ev)
			}
			if n := e.in.budget.inUse(); n != 0 {
				t.Errorf("%d bytes still held", n)
			}
		})
	}
	for name, err := range map[string]error{
		"bug":               errors.New("boom"),
		"constraint":        &pgconn.PgError{Code: "23505"},
		"request cancelled": context.Canceled,
	} {
		t.Run("stays 500: "+name, func(t *testing.T) {
			e := newHEnv(t, nil)
			e.save.err = err
			w := e.do(req{body: body})
			if w.Code != http.StatusInternalServerError || errCode(t, w) != "internal_error" || w.Header().Get("Retry-After") != "" {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
}
