// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package fakegitlab_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func postClaims(t *testing.T, ts *httptest.Server, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/test/ci-token", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func verified(t *testing.T, ts *httptest.Server, body string) map[string]any {
	t.Helper()
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	parsed, err := jwt.ParseSigned(resp.Token, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := parsed.Claims(pubKey(t, ts), &got); err != nil {
		t.Fatalf("token does not verify with the published key: %v", err)
	}
	return got
}

func TestCITokenEndpointIsOffByDefault(t *testing.T) {
	_, ts := newFake(t)
	if status, _ := postClaims(t, ts, `{"aud":"x"}`); status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", status)
	}
}

func TestCITokenEndpoint(t *testing.T) {
	f, ts := newFake(t)
	f.EnableTestEndpoints()

	status, body := postClaims(t, ts, `{"aud":"http://localhost:18080","project_id":"42","ref_protected":"true"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, body)
	}
	got := verified(t, ts, body)
	if got["iss"] != f.IssuerURL() || got["aud"] != "http://localhost:18080" || got["project_id"] != "42" {
		t.Errorf("claims = %v", got)
	}
	if got["iat"] == nil || got["exp"] == nil {
		t.Errorf("iat and exp should default, claims = %v", got)
	}

	// Posted values win over the defaults.
	_, body = postClaims(t, ts, `{"iss":"http://other","exp":1}`)
	got = verified(t, ts, body)
	if got["iss"] != "http://other" || got["exp"] != float64(1) {
		t.Errorf("claims = %v", got)
	}

	if status, _ := postClaims(t, ts, `not json`); status != http.StatusBadRequest {
		t.Errorf("bad body status = %d, want 400", status)
	}
	if status, _ := postClaims(t, ts, `null`); status != http.StatusBadRequest {
		t.Errorf("null body status = %d, want 400", status)
	}
}
