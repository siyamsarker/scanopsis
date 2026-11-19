// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/siyamsarker/scanopsis/internal/auth"
)

func ptr[T any](v T) *T { return &v }

func TestValidatePath(t *testing.T) {
	long := strings.Repeat("a", 250) + "/" + strings.Repeat("b", 250)
	for _, tc := range []struct {
		path string
		ok   bool
	}{
		{"group/project", true},
		{"a/b/c/d", true},
		{"my-group/my.project_1", true},
		{"", false},
		{"project", false},
		{"/group/project", false},
		{"group/project/", false},
		{"group//project", false},
		{"group/my project", false},
		{"group/pro\tject", false},
		{"group/pro\nject", false},
		{long, false}, // 501 characters
		{long[:499], true},
	} {
		if err := validatePath(tc.path); (err == nil) != tc.ok {
			t.Errorf("validatePath(%q) = %v, want ok=%v", tc.path, err, tc.ok)
		}
	}
}

func TestValidateCreate(t *testing.T) {
	name, key, err := validateCreate("platform/payments-api", nil, nil)
	if err != nil || name != "payments-api" || key != nil {
		t.Fatalf("default name: %q %v %v", name, key, err)
	}
	name, key, err = validateCreate("platform/payments-api", ptr("Payments"), ptr("pay:api"))
	if err != nil || name != "Payments" || key == nil || *key != "pay:api" {
		t.Fatalf("explicit: %q %v %v", name, key, err)
	}
	for _, tc := range []struct {
		path      string
		name, key *string
	}{
		{"nogroup", nil, nil},
		{"g/p", ptr(""), nil},
		{"g/p", ptr("   "), nil},
		{"g/p", ptr(strings.Repeat("n", 201)), nil},
		{"g/p", nil, ptr("")},
		{"g/p", nil, ptr("has space")},
		{"g/p", nil, ptr(strings.Repeat("k", 401))},
	} {
		if _, _, err := validateCreate(tc.path, tc.name, tc.key); err == nil {
			t.Errorf("validateCreate(%q, %v, %v) accepted", tc.path, tc.name, tc.key)
		}
	}
	if _, _, err := validateCreate("g/p", ptr(strings.Repeat("n", 200)), ptr(strings.Repeat("k", 400))); err != nil {
		t.Errorf("maximum lengths rejected: %v", err)
	}
}

func TestValidateUpdate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		n        *string
		keySet   bool
		key      *string
		archived *bool
		ok       bool
	}{
		{"empty", nil, false, nil, nil, false},
		{"name", ptr("x"), false, nil, nil, true},
		{"blank name", ptr(" "), false, nil, nil, false},
		{"unlink", nil, true, nil, nil, true},
		{"link", nil, true, ptr("k"), nil, true},
		{"empty key", nil, true, ptr(""), nil, false},
		{"archive", nil, false, nil, ptr(true), true},
	} {
		if err := validateUpdate(tc.n, tc.keySet, tc.key, tc.archived); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestConflictMessage(t *testing.T) {
	for _, tc := range []struct {
		err  error
		ok   bool
		want string
	}{
		{&pgconn.PgError{Code: "23505", ConstraintName: "services_gitlab_project_path_key"}, true, "project path"},
		{&pgconn.PgError{Code: "23505", ConstraintName: "services_sonar_project_key_key"}, true, "SonarQube project key"},
		{&pgconn.PgError{Code: "23505", ConstraintName: "other"}, true, "already exists"},
		{&pgconn.PgError{Code: "23503"}, false, ""},
		{errors.New("boom"), false, ""},
		{nil, false, ""},
	} {
		msg, ok := conflictMessage(tc.err)
		if ok != tc.ok || !strings.Contains(msg, tc.want) {
			t.Errorf("conflictMessage(%v) = %q, %v", tc.err, msg, ok)
		}
	}
}

func TestOptString(t *testing.T) {
	var in struct {
		Key optString `json:"k"`
	}
	for _, tc := range []struct {
		body        string
		set         bool
		val         string
		isNil, fail bool
	}{
		{`{}`, false, "", true, false},
		{`{"k":null}`, true, "", true, false},
		{`{"k":"abc"}`, true, "abc", false, false},
		{`{"k":5}`, true, "", true, true},
	} {
		in.Key = optString{}
		err := json.Unmarshal([]byte(tc.body), &in)
		if (err != nil) != tc.fail {
			t.Errorf("%s: err = %v", tc.body, err)
			continue
		}
		if tc.fail {
			continue
		}
		if in.Key.set != tc.set || (in.Key.val == nil) != tc.isNil || (!tc.isNil && *in.Key.val != tc.val) {
			t.Errorf("%s: got %+v", tc.body, in.Key)
		}
	}
}

// Requests that fail validation never reach the database, so a Catalog
// without a pool serves them.
func TestHandlersRejectBadInput(t *testing.T) {
	c := New(nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := c.Handlers()
	for _, op := range []string{"getPortfolio", "listServices", "createService", "getService", "updateService", "deleteService", "getTrivyIntegration"} {
		if h[op] == nil {
			t.Errorf("missing handler %s", op)
		}
	}
	for _, tc := range []struct {
		name, op, method, target, body string
		id                             string
		status                         int
		code                           string
	}{
		{"limit zero", "listServices", "GET", "/?limit=0", "", "", 400, "invalid_request"},
		{"limit big", "listServices", "GET", "/?limit=501", "", "", 400, "invalid_request"},
		{"limit text", "listServices", "GET", "/?limit=x", "", "", 400, "invalid_request"},
		{"after negative", "listServices", "GET", "/?after=-1", "", "", 400, "invalid_request"},
		{"archived text", "listServices", "GET", "/?archived=maybe", "", "", 400, "invalid_request"},
		{"q long", "listServices", "GET", "/?q=" + strings.Repeat("a", 201), "", "", 400, "invalid_request"},
		{"create bad json", "createService", "POST", "/", "{", "", 400, "invalid_request"},
		{"create unknown field", "createService", "POST", "/", `{"gitlab_project_path":"a/b","x":1}`, "", 400, "invalid_request"},
		{"create trailing data", "createService", "POST", "/", `{"gitlab_project_path":"a/b"}{}`, "", 400, "invalid_request"},
		{"create bad path", "createService", "POST", "/", `{"gitlab_project_path":"/a/b"}`, "", 400, "invalid_request"},
		{"create missing path", "createService", "POST", "/", `{}`, "", 400, "invalid_request"},
		{"get bad id", "getService", "GET", "/", "", "abc", 404, "not_found"},
		{"get zero id", "getService", "GET", "/", "", "0", 404, "not_found"},
		{"update bad id", "updateService", "PATCH", "/", `{"archived":true}`, "x", 404, "not_found"},
		{"update empty", "updateService", "PATCH", "/", `{}`, "1", 400, "invalid_request"},
		{"update bad key", "updateService", "PATCH", "/", `{"sonar_project_key":""}`, "1", 400, "invalid_request"},
		{"update unknown field", "updateService", "PATCH", "/", `{"gitlab_project_path":"a/b"}`, "1", 400, "invalid_request"},
		{"delete bad id", "deleteService", "DELETE", "/", "", "-3", 404, "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.target, strings.NewReader(tc.body))
			if tc.id != "" {
				req.SetPathValue("id", tc.id)
			}
			rec := httptest.NewRecorder()
			h[tc.op].ServeHTTP(rec, req)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestActor(t *testing.T) {
	c := &Catalog{}
	if got := c.actor(context.Background()); got != "" {
		t.Errorf("no principal: %q", got)
	}
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Source: "gitlab", Username: "vera"})
	if got := c.actor(ctx); got != "gitlab:vera" {
		t.Errorf("actor = %q", got)
	}
}
