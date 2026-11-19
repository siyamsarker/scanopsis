// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package sonar

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/siyamsarker/scanopsis/internal/settings"
)

const testSecret = "hook-secret"

// fixedSettings is a SettingsSource tests can change.
type fixedSettings struct {
	mu   sync.Mutex
	snap settings.Snapshot
}

func (f *fixedSettings) Current() settings.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}

func sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// hook builds a webhook body as SonarQube sends it. Fields of over replace
// or add top-level fields.
func hook(taskID, projectKey string, over map[string]any) []byte {
	m := map[string]any{
		"serverUrl":  "https://sonarqube.example.com",
		"taskId":     taskID,
		"status":     "SUCCESS",
		"analysedAt": "2026-11-19T10:00:00+0000",
		"revision":   "abc123",
		"project":    map[string]any{"key": projectKey, "name": "Payments API", "url": "https://sonarqube.example.com/dashboard?id=" + projectKey},
		"qualityGate": map[string]any{
			"name": "Sonar way", "status": "ERROR",
			"conditions": []map[string]any{
				{"metric": "new_coverage", "operator": "LESS_THAN", "value": "70.5", "status": "ERROR", "errorThreshold": "80", "onLeakPeriod": true},
				{"metric": "new_violations", "operator": "GREATER_THAN", "status": "NO_VALUE", "errorThreshold": "0", "onLeakPeriod": true},
			},
		},
		"properties": map[string]string{},
	}
	for k, v := range over {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

// post sends a signed webhook to h.
func post(h http.Handler, secret string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/hooks/sonarqube", bytes.NewReader(body))
	if secret != "" {
		req.Header.Set(signatureHeader, sign(secret, body))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
