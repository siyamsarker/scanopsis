// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package sonar

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/testutil/fakesonar"
)

func TestMetricKeySets(t *testing.T) {
	if len(metricKeySets[0]) != 20 || len(metricKeySets[1]) != 14 || len(metricKeySets[2]) != 14 {
		t.Fatalf("sizes %d %d %d", len(metricKeySets[0]), len(metricKeySets[1]), len(metricKeySets[2]))
	}
	for _, k := range metricKeySets[1] {
		if k == "sqale_rating" {
			t.Error("MQR set has a Standard key")
		}
	}
	for _, k := range metricKeySets[2] {
		if strings.HasPrefix(k, "software_quality_") {
			t.Error("Standard set has an MQR key")
		}
	}
}

func TestMeasuresAdaptToTheServersMode(t *testing.T) {
	cases := []struct {
		mode     string
		status   int
		measures map[string]string
		calls    int // calls of the first read: combined, then the sets tried
		rating   string
	}{
		{"", 0, fakesonar.MQRMeasures(), 1, "mqr"},
		{"mqr", 400, fakesonar.MQRMeasures(), 2, "mqr"},
		{"mqr", 404, fakesonar.MQRMeasures(), 2, "mqr"},
		{"standard", 400, fakesonar.StandardMeasures(), 3, "standard"},
		{"standard", 404, fakesonar.StandardMeasures(), 3, "standard"},
	}
	for _, c := range cases {
		t.Run(c.mode+"/"+string(rune('0'+c.status/100))+string(rune('0'+c.status/10%10)), func(t *testing.T) {
			srv := fakesonar.New(testToken)
			defer srv.Close()
			srv.RejectUnknownMetrics(c.mode, c.status)
			srv.Set("p", fakesonar.Project{Measures: c.measures})
			s := New(nil, &fixedSettings{}, Options{HTTPClient: srv.Client()})
			snap := settings.Snapshot{SonarURL: srv.URL, SonarToken: testToken}
			ctx := context.Background()

			got, err := s.client(snap).measures(ctx, "p")
			if err != nil || !reflect.DeepEqual(got, c.measures) {
				t.Fatalf("measures: %v %v", got, err)
			}
			if n := srv.Count("api/measures/component"); n != c.calls {
				t.Errorf("%d calls, want %d", n, c.calls)
			}
			if normalise(got).Mode != c.rating {
				t.Errorf("mode %q", normalise(got).Mode)
			}

			// The working set is remembered: one call from now on.
			if _, err := s.client(snap).measures(ctx, "p"); err != nil {
				t.Fatal(err)
			}
			if n := srv.Count("api/measures/component"); n != c.calls+1 {
				t.Errorf("%d calls after the second read, want %d", n, c.calls+1)
			}

			// A new token or URL starts again from the combined set.
			snap.SonarToken = testToken // unchanged: still remembered
			s.client(snap)
			if s.keys.set != map[string]int{"": 0, "mqr": 1, "standard": 2}[c.mode] {
				t.Errorf("cache lost on an unchanged configuration: %d", s.keys.set)
			}
			s.client(settings.Snapshot{SonarURL: srv.URL, SonarToken: "other"})
			if s.keys.set != 0 {
				t.Errorf("cache not reset by a token change: %d", s.keys.set)
			}
			s.client(snap)
			s.keys.set = 2
			s.client(settings.Snapshot{SonarURL: srv.URL + "/", SonarToken: testToken})
			if s.keys.set != 0 {
				t.Errorf("cache not reset by a URL change: %d", s.keys.set)
			}
		})
	}
}

func TestMeasuresRetryOnlyForMetricKeyErrors(t *testing.T) {
	srv := fakesonar.New(testToken)
	defer srv.Close()
	srv.Set("p", fakesonar.Project{})
	s := New(nil, &fixedSettings{}, Options{HTTPClient: srv.Client()})
	_, err := s.client(settings.Snapshot{SonarURL: srv.URL, SonarToken: testToken}).measures(context.Background(), "missing")
	if err == nil || err.Error() != "SonarQube returned HTTP 404 for api/measures/component" {
		t.Errorf("%v", err)
	}
	// "Component key ... not found" is about the project, not the metric
	// keys: other key sets would fail the same way, so nothing is retried.
	if n := srv.Count("api/measures/component"); n != 1 {
		t.Errorf("%d calls for an unknown project, want 1", n)
	}
	// Other failures are not retried.
	srv.Fail("api/measures/component", 500)
	before := srv.Count("api/measures/component")
	if _, err := s.client(settings.Snapshot{SonarURL: srv.URL, SonarToken: testToken}).measures(context.Background(), "p"); err == nil {
		t.Error("want an error")
	}
	if n := srv.Count("api/measures/component") - before; n != 1 {
		t.Errorf("%d calls for a 500, want 1", n)
	}
}

func TestKeySetCacheIgnoresStaleConfiguration(t *testing.T) {
	var k keySetCache
	old := k.forConfig("https://sonarqube.example.com", "token-a")
	k.putFor(old, 2)
	current := k.forConfig("https://sonarqube.example.com", "token-b")
	// A client built for the old configuration finishes late: its result must
	// not be remembered for the new one.
	k.putFor(old, 1)
	if got := k.getFor(current); got != 0 {
		t.Fatalf("stale write changed the cache: %d", got)
	}
	if got := k.getFor(old); got != 0 {
		t.Fatalf("an old configuration must not read the new one's state: %d", got)
	}
	k.putFor(current, 2)
	if got := k.getFor(current); got != 2 {
		t.Fatalf("getFor(current) = %d, want 2", got)
	}
}

func TestKeySetCacheDoesNotKeepTheToken(t *testing.T) {
	var k keySetCache
	k.forConfig("https://sonarqube.example.com", "squ_secret_token_value")
	if strings.Contains(fmt.Sprintf("%+v", k.cfg), "squ_secret") || strings.Contains(string(k.cfg[:]), "squ_secret") {
		t.Fatal("the cache keeps the token in clear")
	}
}

func TestMeasuresRecoverWhenTheServerModeChanges(t *testing.T) {
	srv := fakesonar.New(testToken)
	defer srv.Close()
	srv.Set("p", fakesonar.Project{})
	s := New(nil, &fixedSettings{}, Options{HTTPClient: srv.Client()})
	snap := settings.Snapshot{SonarURL: srv.URL, SonarToken: testToken}
	srv.RejectUnknownMetrics("standard", 400)
	if _, err := s.client(snap).measures(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
	// The administrator switches the server to MQR mode: the remembered
	// Standard set is now rejected and the client finds the MQR set.
	srv.RejectUnknownMetrics("mqr", 400)
	if _, err := s.client(snap).measures(context.Background(), "p"); err != nil {
		t.Fatalf("after a mode change: %v", err)
	}
}
