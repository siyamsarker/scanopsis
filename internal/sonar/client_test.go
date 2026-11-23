// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package sonar

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/testutil/fakesonar"
)

const testToken = "squ_secret_token_value"

func str(s string) *string   { return &s }
func f64(f float64) *float64 { return &f }

func newClient(s *fakesonar.Server) *client {
	return &client{http: s.Client(), base: s.URL, token: testToken}
}

func TestRating(t *testing.T) {
	cases := map[string]string{
		"1.0": "A", "2.0": "B", "3.0": "C", "4.0": "D", "5.0": "E",
		"1": "A", "2": "B", " 5 ": "E",
		"": "", "0": "", "0.0": "", "6.0": "", "1.5": "", "x": "", "NaN": "", "Inf": "", "-1": "",
	}
	for in, want := range cases {
		if got := rating(in); got != want {
			t.Errorf("rating(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormaliseMQR(t *testing.T) {
	got := normalise(fakesonar.MQRMeasures())
	want := normalised{Mode: "mqr", Reliability: "A", Security: "B", Maintainability: "A", SecurityReview: "A", Coverage: f64(81.3), Duplication: f64(2.4)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestNormaliseStandard(t *testing.T) {
	got := normalise(fakesonar.StandardMeasures())
	want := normalised{Mode: "standard", Reliability: "C", Security: "A", Maintainability: "B", SecurityReview: "A", Coverage: f64(81.3), Duplication: f64(2.4)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestNormaliseUnreportedStaysNull(t *testing.T) {
	m := fakesonar.MQRMeasures()
	delete(m, "coverage")
	delete(m, "duplicated_lines_density")
	delete(m, "security_review_rating")
	delete(m, "software_quality_security_rating")
	got := normalise(m)
	if got.Coverage != nil || got.Duplication != nil {
		t.Errorf("percentages must be nil: %+v", got)
	}
	if got.SecurityReview != "" || got.Security != "" || got.Reliability != "A" {
		t.Errorf("ratings: %+v", got)
	}

	// Nothing reported: no mode, no values.
	empty := normalise(map[string]string{"ncloc": "10"})
	if !reflect.DeepEqual(empty, normalised{}) {
		t.Errorf("empty: %+v", empty)
	}
	// A reported 0 coverage stays 0; an unparseable value becomes null.
	if c := normalise(map[string]string{"coverage": "0.0"}).Coverage; c == nil || *c != 0 {
		t.Errorf("coverage 0.0: %v", c)
	}
	if c := normalise(map[string]string{"coverage": "NaN"}).Coverage; c != nil {
		t.Errorf("coverage NaN: %v", *c)
	}
	// MQR wins when both families are present (legacy keys remain in MQR mode).
	both := map[string]string{"software_quality_security_rating": "3.0", "security_rating": "1.0"}
	if n := normalise(both); n.Mode != "mqr" || n.Security != "C" {
		t.Errorf("both: %+v", n)
	}
}

func TestOperatorName(t *testing.T) {
	for in, want := range map[string]string{"GT": "GREATER_THAN", "LT": "LESS_THAN", "EQ": "EQUALS", "NE": "NOT_EQUALS", "GREATER_THAN": "GREATER_THAN", "?": "?"} {
		if got := operatorName(in); got != want {
			t.Errorf("operatorName(%q) = %q", in, got)
		}
	}
}

func TestParseTime(t *testing.T) {
	want := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	for _, in := range []string{"2026-11-19T10:00:00+0000", "2026-11-19T10:00:00.000+0000", "2026-11-19T10:00:00Z", "2026-11-19T12:00:00+0200"} {
		got, err := parseTime(in)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseTime(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := parseTime("yesterday"); err == nil {
		t.Error("want error")
	}
}

func TestClientEndpoints(t *testing.T) {
	s := fakesonar.New(testToken)
	defer s.Close()
	date := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	s.Set("platform:payments-api", fakesonar.Project{
		Measures:   fakesonar.MQRMeasures(),
		GateStatus: "ERROR",
		Conditions: []fakesonar.Condition{
			{Status: "ERROR", MetricKey: "coverage", Comparator: "LT", ErrorThreshold: "85", ActualValue: "81.3"},
			{Status: "NO_VALUE", MetricKey: "new_coverage", Comparator: "LT", ErrorThreshold: "80"},
		},
		Analyses: []fakesonar.Analysis{{Key: "AY1", Date: date, Revision: "abc123"}},
	})
	c := newClient(s)
	ctx := context.Background()

	m, err := c.measures(ctx, "platform:payments-api")
	if err != nil || !reflect.DeepEqual(m, fakesonar.MQRMeasures()) {
		t.Errorf("measures: %v %v", m, err)
	}
	status, conds, err := c.projectStatus(ctx, "platform:payments-api")
	wantConds := []Condition{
		{Metric: "coverage", Operator: "LESS_THAN", Threshold: "85", Value: str("81.3"), Status: "ERROR"},
		{Metric: "new_coverage", Operator: "LESS_THAN", Threshold: "80", Value: nil, Status: "NO_VALUE"},
	}
	if err != nil || status != "ERROR" || !reflect.DeepEqual(conds, wantConds) {
		t.Errorf("projectStatus: %q %+v %v", status, conds, err)
	}
	a, err := c.latestAnalysis(ctx, "platform:payments-api")
	if err != nil || a == nil || a.Key != "AY1" || !a.Date.Equal(date) || a.Revision != "abc123" {
		t.Errorf("latestAnalysis: %+v %v", a, err)
	}
	v, err := c.serverVersion(ctx)
	if err != nil || v != "26.9.0.12345" {
		t.Errorf("version %q %v", v, err)
	}

	// No analyses yet.
	s.Set("empty", fakesonar.Project{})
	if a, err := c.latestAnalysis(ctx, "empty"); a != nil || err != nil {
		t.Errorf("empty: %+v %v", a, err)
	}
}

func TestClientErrors(t *testing.T) {
	s := fakesonar.New(testToken)
	defer s.Close()
	s.Set("p", fakesonar.Project{})
	ctx := context.Background()

	var ae *apiError
	_, err := newClient(s).measures(ctx, "missing")
	if !errors.As(err, &ae) || ae.Status != 404 || ae.Endpoint != "api/measures/component" {
		t.Errorf("404: %v", err)
	}
	bad := &client{http: s.Client(), base: s.URL, token: "wrong"}
	_, err = bad.measures(ctx, "p")
	if !errors.As(err, &ae) || ae.Status != 401 {
		t.Errorf("401: %v", err)
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Errorf("error leaks the token: %v", err)
	}
	s.Fail("*", 500)
	if _, _, err := newClient(s).projectStatus(ctx, "p"); !errors.As(err, &ae) || ae.Status != 500 || err.Error() != "SonarQube returned HTTP 500 for api/qualitygates/project_status" {
		t.Errorf("500: %v", err)
	}
	s.Fail("*", 0)

	// Unreadable bodies.
	junk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) }))
	defer junk.Close()
	jc := &client{http: junk.Client(), base: junk.URL, token: testToken}
	if _, err := jc.measures(ctx, "p"); err == nil || err.Error() != "api/measures/component: unreadable response" {
		t.Errorf("junk measures: %v", err)
	}
	if _, _, err := jc.projectStatus(ctx, "p"); err == nil {
		t.Error("junk status")
	}
	if _, err := jc.latestAnalysis(ctx, "p"); err == nil {
		t.Error("junk analyses")
	}
	badDate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"analyses":[{"key":"k","date":"nope"}]}`))
	}))
	defer badDate.Close()
	if _, err := (&client{http: badDate.Client(), base: badDate.URL}).latestAnalysis(ctx, "p"); err == nil || err.Error() != "api/project_analyses/search: unreadable analysis date" {
		t.Errorf("bad date: %v", err)
	}

	// Connection refused: the message names the endpoint, not the URL.
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	_, err = (&client{http: http.DefaultClient, base: dead.URL, token: testToken}).serverVersion(ctx)
	if err == nil || strings.Contains(err.Error(), dead.URL) || !strings.HasPrefix(err.Error(), "request to api/server/version failed") {
		t.Errorf("dial: %v", err)
	}
	if _, err := (&client{http: http.DefaultClient, base: "http://bad host"}).serverVersion(ctx); err == nil {
		t.Error("invalid URL")
	}
}

func TestClientTimeout(t *testing.T) {
	s := fakesonar.New(testToken)
	defer s.Close()
	s.SetDelay(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := newClient(s).serverVersion(ctx); err == nil {
		t.Error("want a timeout error")
	}
}

func TestMeasuresNewCodePeriod(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"component":{"measures":[{"metric":"new_coverage","period":{"index":1,"value":"77.5"},"bestValue":false},` +
			`{"metric":"coverage","value":"0.0"},{"metric":"x","period":{"index":1}}]}}`))
	}))
	defer srv.Close()
	m, err := (&client{http: srv.Client(), base: srv.URL}).measures(context.Background(), "p")
	if err != nil || !reflect.DeepEqual(m, map[string]string{"new_coverage": "77.5", "coverage": "0.0"}) {
		t.Errorf("%v %v", m, err)
	}
}
