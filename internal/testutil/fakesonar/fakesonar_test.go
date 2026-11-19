// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package fakesonar

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func get(t *testing.T, s *Server, token, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, s.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestFake(t *testing.T) {
	s := New("tok")
	defer s.Close()
	date := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	s.Set("p", Project{
		Measures:   map[string]string{"coverage": "80.0"},
		GateStatus: "ERROR",
		Conditions: []Condition{
			{Status: "ERROR", MetricKey: "coverage", Comparator: "LT", ErrorThreshold: "85", ActualValue: "80.0"},
			{Status: "NO_VALUE", MetricKey: "x", Comparator: "GT", ErrorThreshold: "1"},
		},
		Analyses: []Analysis{{Key: "a2", Date: date, Revision: "abc"}, {Key: "a1", Date: date.Add(-time.Hour)}},
	})

	cases := []struct {
		name, token, path string
		status            int
		body              string
	}{
		{"no token", "", "/api/server/version", 401, `{"errors":[{"msg":"Invalid token"}]}` + "\n"},
		{"version", "tok", "/api/server/version", 200, "26.9.0.12345"},
		{"measures only reported", "tok", "/api/measures/component?component=p&metricKeys=coverage,ncloc", 200,
			`{"component":{"key":"p","measures":[{"metric":"coverage","value":"80.0"}]}}` + "\n"},
		{"unknown project", "tok", "/api/measures/component?component=q&metricKeys=coverage", 404,
			`{"errors":[{"msg":"Component key 'q' not found"}]}` + "\n"},
		{"status", "tok", "/api/qualitygates/project_status?projectKey=p", 200,
			`{"projectStatus":{"conditions":[{"actualValue":"80.0","comparator":"LT","errorThreshold":"85","metricKey":"coverage","status":"ERROR"},` +
				`{"comparator":"GT","errorThreshold":"1","metricKey":"x","status":"NO_VALUE"}],"status":"ERROR"}}` + "\n"},
		{"analyses", "tok", "/api/project_analyses/search?project=p&ps=1", 200,
			`{"analyses":[{"date":"2026-11-19T10:00:00+0000","key":"a2","revision":"abc"}],"paging":{"pageIndex":1,"pageSize":1,"total":2}}` + "\n"},
		{"unknown endpoint", "tok", "/api/nope", 404, `{"errors":[{"msg":"Unknown endpoint"}]}` + "\n"},
	}
	for _, c := range cases {
		status, body := get(t, s, c.token, c.path)
		if status != c.status || body != c.body {
			t.Errorf("%s: got %d %q, want %d %q", c.name, status, body, c.status, c.body)
		}
	}

	s.SetVersion("26.9.1")
	s.Fail("api/server/version", 500)
	if status, _ := get(t, s, "tok", "/api/server/version"); status != 500 {
		t.Errorf("failure: %d", status)
	}
	s.Fail("api/server/version", 0)
	if _, body := get(t, s, "tok", "/api/server/version"); body != "26.9.1" {
		t.Errorf("version %q", body)
	}
	s.Fail("*", 404)
	if status, _ := get(t, s, "tok", "/api/server/version"); status != 404 {
		t.Errorf("wildcard failure: %d", status)
	}
	s.Fail("*", 0)

	s.SetDelay(50 * time.Millisecond)
	start := time.Now()
	get(t, s, "tok", "/api/server/version")
	if time.Since(start) < 50*time.Millisecond {
		t.Error("delay not applied")
	}
	if n := s.Count("api/server/version"); n != 6 {
		t.Errorf("count %d", n)
	}
	if len(MQRMeasures()) != 14 || len(StandardMeasures()) != 14 {
		t.Error("fixtures changed")
	}
	if !strings.HasPrefix(s.URL, "http://") {
		t.Error(s.URL)
	}
}
