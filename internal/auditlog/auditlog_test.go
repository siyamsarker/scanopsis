// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package auditlog

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestParseFilterDefaults(t *testing.T) {
	f, msg := parseFilter(url.Values{})
	if msg != "" {
		t.Fatalf("message = %q", msg)
	}
	if f.action != nil || f.actor != nil || f.before != nil || f.limit != 50 {
		t.Fatalf("defaults = %+v", f)
	}
}

func TestParseFilterValid(t *testing.T) {
	f, msg := parseFilter(url.Values{
		"action": {"login.failed"},
		"actor":  {"Alice"},
		"before": {"42"},
		"limit":  {"200"},
	})
	if msg != "" {
		t.Fatalf("message = %q", msg)
	}
	if f.action == nil || *f.action != "login.failed" {
		t.Errorf("action = %v", f.action)
	}
	if f.actor == nil || *f.actor != "Alice" {
		t.Errorf("actor = %v", f.actor)
	}
	if f.before == nil || *f.before != 42 {
		t.Errorf("before = %v", f.before)
	}
	if f.limit != 200 {
		t.Errorf("limit = %d", f.limit)
	}
}

func TestParseFilterEmptyValuesAreAbsent(t *testing.T) {
	f, msg := parseFilter(url.Values{"action": {""}, "actor": {""}, "before": {""}, "limit": {""}})
	if msg != "" {
		t.Fatalf("message = %q", msg)
	}
	if f.action != nil || f.actor != nil || f.before != nil || f.limit != 50 {
		t.Fatalf("filter = %+v", f)
	}
}

func TestParseFilterLengthBoundaries(t *testing.T) {
	if _, msg := parseFilter(url.Values{"action": {strings.Repeat("a", 100)}}); msg != "" {
		t.Errorf("100-character action: %q", msg)
	}
	if _, msg := parseFilter(url.Values{"actor": {strings.Repeat("a", 200)}}); msg != "" {
		t.Errorf("200-character actor: %q", msg)
	}
}

func TestParseFilterInvalid(t *testing.T) {
	for _, tc := range []struct {
		name string
		qs   url.Values
		want string
	}{
		{"action too long", url.Values{"action": {strings.Repeat("a", 101)}},
			"action must be at most 100 characters."},
		{"actor too long", url.Values{"actor": {strings.Repeat("a", 201)}},
			"actor must be at most 200 characters."},
		{"before zero", url.Values{"before": {"0"}},
			"before must be an integer of at least 1."},
		{"before negative", url.Values{"before": {"-3"}},
			"before must be an integer of at least 1."},
		{"before not a number", url.Values{"before": {"abc"}},
			"before must be an integer of at least 1."},
		{"before overflows int64", url.Values{"before": {"9223372036854775808"}},
			"before must be an integer of at least 1."},
		{"before fractional", url.Values{"before": {"1.5"}},
			"before must be an integer of at least 1."},
		{"limit zero", url.Values{"limit": {"0"}},
			"limit must be an integer between 1 and 200."},
		{"limit above maximum", url.Values{"limit": {"201"}},
			"limit must be an integer between 1 and 200."},
		{"limit not a number", url.Values{"limit": {"ten"}},
			"limit must be an integer between 1 and 200."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, msg := parseFilter(tc.qs)
			if msg != tc.want {
				t.Fatalf("message = %q, want %q", msg, tc.want)
			}
			if f.limit != 0 || f.action != nil || f.actor != nil || f.before != nil {
				t.Fatalf("invalid request returned a filter: %+v", f)
			}
		})
	}
}

func TestInvalidParameterResponses(t *testing.T) {
	// The pool is nil: a validation failure must answer before any query.
	h := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil))).Handlers()["listAuditEvents"]
	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{"limit zero", "limit=0", "limit must be an integer between 1 and 200."},
		{"limit above maximum", "limit=201", "limit must be an integer between 1 and 200."},
		{"before zero", "before=0", "before must be an integer of at least 1."},
		{"before not a number", "before=x", "before must be an integer of at least 1."},
		{"action too long", "action=" + strings.Repeat("a", 101), "action must be at most 100 characters."},
		{"actor too long", "actor=" + strings.Repeat("a", 201), "actor must be at most 200 characters."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/admin/audit?"+tc.query, nil)
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d", rec.Code)
			}
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != "invalid_parameter" || body.Error.Message != tc.want {
				t.Fatalf("error = %+v, want invalid_parameter %q", body.Error, tc.want)
			}
		})
	}
}

func TestHandlersKeyedByOperationID(t *testing.T) {
	h := New(nil, slog.New(slog.NewTextHandler(io.Discard, nil))).Handlers()
	if len(h) != 1 || h["listAuditEvents"] == nil {
		t.Fatalf("handlers = %v", h)
	}
}
