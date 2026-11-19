// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Package fakesonar is a fake SonarQube server for tests. It implements only
// the four Web API endpoints Scanopsis reads: api/measures/component,
// api/qualitygates/project_status, api/project_analyses/search and
// api/server/version. The response shapes are built from the documented
// shapes; they are not recorded from a live server. It must never be used
// outside tests.
package fakesonar

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

// Condition is one quality gate condition as project_status reports it.
type Condition struct {
	Status         string
	MetricKey      string
	Comparator     string
	ErrorThreshold string
	ActualValue    string // empty: not reported
}

// Analysis is one entry of api/project_analyses/search.
type Analysis struct {
	Key      string
	Date     time.Time
	Revision string
}

// Project is the data served for one project key.
type Project struct {
	// Measures are the measures SonarQube reports; a metric that is absent
	// here is not returned, as SonarQube omits metrics it has no value for.
	Measures map[string]string
	// GateStatus is projectStatus.status (OK, WARN, ERROR or NONE).
	GateStatus string
	Conditions []Condition
	// Analyses are returned newest first.
	Analyses []Analysis
}

// Server is a running fake. Its URL is the SonarQube base URL.
type Server struct {
	*httptest.Server
	token string

	mu       sync.Mutex
	projects map[string]Project
	version  string
	failures map[string]int
	delay    time.Duration
	requests []string
}

// New starts a fake that accepts the given bearer token.
func New(token string) *Server {
	s := &Server{
		token:    token,
		projects: map[string]Project{},
		version:  "26.9.0.12345",
		failures: map[string]int{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Set stores the data for a project key.
func (s *Server) Set(key string, p Project) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projects[key] = p
}

// SetVersion sets the plain text answer of api/server/version.
func (s *Server) SetVersion(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version = v
}

// Fail makes an endpoint (for example "api/measures/component") answer with
// the given HTTP status; 0 removes the failure. The key "*" affects every
// endpoint.
func (s *Server) Fail(endpoint string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if status == 0 {
		delete(s.failures, endpoint)
		return
	}
	s.failures[endpoint] = status
}

// SetDelay makes every answer wait this long before it is sent.
func (s *Server) SetDelay(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delay = d
}

// Count returns how many requests reached an endpoint (for example
// "api/project_analyses/search"), counting rejected ones too.
func (s *Server) Count(endpoint string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.requests {
		if r == endpoint {
			n++
		}
	}
	return n
}

// MQRMeasures returns the measures of a project in MQR mode. Callers delete
// the keys they want to be unreported.
func MQRMeasures() map[string]string {
	return map[string]string{
		"software_quality_reliability_rating":     "1.0",
		"software_quality_security_rating":        "2.0",
		"software_quality_maintainability_rating": "1.0",
		"software_quality_reliability_issues":     "0",
		"software_quality_security_issues":        "3",
		"software_quality_maintainability_issues": "42",
		"security_review_rating":                  "1.0",
		"security_hotspots":                       "4",
		"security_hotspots_reviewed":              "75.0",
		"coverage":                                "81.3",
		"new_coverage":                            "90.5",
		"duplicated_lines_density":                "2.4",
		"ncloc":                                   "12345",
		"alert_status":                            "OK",
	}
}

// StandardMeasures returns the measures of a project in Standard mode.
func StandardMeasures() map[string]string {
	return map[string]string{
		"reliability_rating":         "3.0",
		"security_rating":            "1.0",
		"sqale_rating":               "2.0",
		"bugs":                       "5",
		"vulnerabilities":            "0",
		"code_smells":                "42",
		"security_review_rating":     "1.0",
		"security_hotspots":          "4",
		"security_hotspots_reviewed": "75.0",
		"coverage":                   "81.3",
		"new_coverage":               "90.5",
		"duplicated_lines_density":   "2.4",
		"ncloc":                      "12345",
		"alert_status":               "ERROR",
	}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	endpoint := strings.TrimPrefix(r.URL.Path, "/")
	s.mu.Lock()
	s.requests = append(s.requests, endpoint)
	delay := s.delay
	status := s.failures[endpoint]
	if status == 0 {
		status = s.failures["*"]
	}
	s.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if r.Header.Get("Authorization") != "Bearer "+s.token {
		writeError(w, http.StatusUnauthorized, "Invalid token")
		return
	}
	if status != 0 {
		writeError(w, status, "Injected failure")
		return
	}
	q := r.URL.Query()
	switch endpoint {
	case "api/server/version":
		s.mu.Lock()
		v := s.version
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(v))
	case "api/measures/component":
		s.measures(w, q.Get("component"), q.Get("metricKeys"))
	case "api/qualitygates/project_status":
		s.projectStatus(w, q.Get("projectKey"))
	case "api/project_analyses/search":
		s.analyses(w, q.Get("project"))
	default:
		writeError(w, http.StatusNotFound, "Unknown endpoint")
	}
}

func (s *Server) project(w http.ResponseWriter, key string) (Project, bool) {
	s.mu.Lock()
	p, ok := s.projects[key]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "Component key '"+key+"' not found")
	}
	return p, ok
}

type measure struct {
	Metric string `json:"metric"`
	Value  string `json:"value"`
}

func (s *Server) measures(w http.ResponseWriter, key, metricKeys string) {
	p, ok := s.project(w, key)
	if !ok {
		return
	}
	out := []measure{}
	for _, m := range strings.Split(metricKeys, ",") {
		if v, ok := p.Measures[m]; ok {
			out = append(out, measure{Metric: m, Value: v})
		}
	}
	writeJSON(w, map[string]any{"component": map[string]any{"key": key, "measures": out}})
}

func (s *Server) projectStatus(w http.ResponseWriter, key string) {
	p, ok := s.project(w, key)
	if !ok {
		return
	}
	conds := []map[string]any{}
	for _, c := range p.Conditions {
		m := map[string]any{
			"status": c.Status, "metricKey": c.MetricKey,
			"comparator": c.Comparator, "errorThreshold": c.ErrorThreshold,
		}
		if c.ActualValue != "" {
			m["actualValue"] = c.ActualValue
		}
		conds = append(conds, m)
	}
	writeJSON(w, map[string]any{"projectStatus": map[string]any{
		"status": p.GateStatus, "conditions": conds,
	}})
}

func (s *Server) analyses(w http.ResponseWriter, key string) {
	p, ok := s.project(w, key)
	if !ok {
		return
	}
	out := []map[string]any{}
	for _, a := range p.Analyses {
		m := map[string]any{"key": a.Key, "date": a.Date.Format("2006-01-02T15:04:05-0700")}
		if a.Revision != "" {
			m["revision"] = a.Revision
		}
		out = append(out, m)
	}
	// ps=1 is what Scanopsis asks for: the newest analysis only.
	if len(out) > 1 {
		out = out[:1]
	}
	writeJSON(w, map[string]any{"paging": map[string]int{"pageIndex": 1, "pageSize": 1, "total": len(p.Analyses)}, "analyses": out})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"msg": msg}}})
}
