// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package sonar

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/siyamsarker/scanopsis/internal/settings"
)

func TestValidSignature(t *testing.T) {
	body := []byte(`{"taskId":"t1"}`)
	good := sign(testSecret, body)
	cases := []struct {
		name string
		sec  string
		body []byte
		sig  string
		want bool
	}{
		{"valid lowercase", testSecret, body, good, true},
		{"valid uppercase hex", testSecret, body, strings.ToUpper(good), true},
		{"wrong secret", "other", body, good, false},
		{"tampered body", testSecret, []byte(`{"taskId":"t2"}`), good, false},
		{"missing header", testSecret, body, "", false},
		{"not hex", testSecret, body, "zz" + good[2:], false},
		{"truncated", testSecret, body, good[:len(good)-2], false},
	}
	for _, c := range cases {
		if got := validSignature(c.sec, c.body, c.sig); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// A known vector, so a change of algorithm or encoding is caught.
func TestSignatureVector(t *testing.T) {
	got := sign("key", []byte("The quick brown fox jumps over the lazy dog"))
	if got != "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8" {
		t.Errorf("HMAC-SHA256 vector: %s", got)
	}
}

func newUnitSonar(snap settings.Snapshot) (*Sonar, http.Handler) {
	s := New(nil, &fixedSettings{snap: snap}, Options{})
	return s, s.Handlers()["sonarWebhook"]
}

var enabled = settings.Snapshot{SonarURL: "https://sonarqube.example.com", SonarToken: "t", SonarWebhookSecret: testSecret}

func TestWebhookRejections(t *testing.T) {
	_, h := newUnitSonar(enabled)
	body := hook("t1", "platform:payments-api", nil)

	check := func(name string, rec *httptest.ResponseRecorder, status int, code string) {
		t.Helper()
		if rec.Code != status {
			t.Errorf("%s: status %d, want %d (%s)", name, rec.Code, status, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), `"code":"`+code+`"`) {
			t.Errorf("%s: body %s, want code %s", name, rec.Body, code)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: missing no-store", name)
		}
	}

	check("wrong secret", post(h, "other", body), 401, "invalid_signature")
	check("missing header", post(h, "", body), 401, "invalid_signature")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", bytes.NewReader(append([]byte(" "), body...)))
	req.Header.Set(signatureHeader, sign(testSecret, body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	check("tampered body", rec, 401, "invalid_signature")

	check("not JSON", post(h, testSecret, []byte("nope")), 400, "invalid_payload")
	check("no taskId", post(h, testSecret, hook("", "k", nil)), 400, "invalid_payload")
	check("no project key", post(h, testSecret, hook("t", "", nil)), 400, "invalid_payload")

	big := hook("t", "k", map[string]any{"pad": strings.Repeat("x", maxWebhookBody)})
	check("oversize", post(h, testSecret, big), 413, "payload_too_large")
}

func TestWebhookNotConfigured(t *testing.T) {
	body := hook("t1", "k", nil)
	for name, snap := range map[string]settings.Snapshot{
		"nothing":   {},
		"no secret": {SonarURL: "https://sonarqube.example.com", SonarToken: "t"},
		"no token":  {SonarURL: "https://sonarqube.example.com", SonarWebhookSecret: testSecret},
		"no URL":    {SonarToken: "t", SonarWebhookSecret: testSecret},
	} {
		_, h := newUnitSonar(snap)
		rec := post(h, testSecret, body)
		if rec.Code != 503 || !strings.Contains(rec.Body.String(), `"code":"sonar_not_configured"`) {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}
