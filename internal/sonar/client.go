// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Package sonar integrates SonarQube: it receives the analysis webhook, reads
// the analysis from the Web API, keeps the results in PostgreSQL and serves
// them to the dashboard.
package sonar

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	requestTimeout = 10 * time.Second
	maxResponse    = 4 << 20
)

// sharedMetricKeys are reported in both modes.
var sharedMetricKeys = []string{
	"security_review_rating", "security_hotspots", "security_hotspots_reviewed",
	"coverage", "new_coverage", "duplicated_lines_density", "ncloc", "alert_status",
}

var (
	mqrMetricKeys = []string{
		"software_quality_reliability_rating", "software_quality_security_rating",
		"software_quality_maintainability_rating",
		"software_quality_reliability_issues", "software_quality_security_issues",
		"software_quality_maintainability_issues",
	}
	standardMetricKeys = []string{
		"reliability_rating", "security_rating", "sqale_rating",
		"bugs", "vulnerabilities", "code_smells",
	}
	// metricKeySets are tried in order: every key in one call, then only the
	// MQR keys, then only the Standard keys. A server that rejects metric keys
	// it does not know is thereby served the keys of its own mode.
	metricKeySets = [][]string{
		append(append(append([]string{}, mqrMetricKeys...), standardMetricKeys...), sharedMetricKeys...),
		append(append([]string{}, mqrMetricKeys...), sharedMetricKeys...),
		append(append([]string{}, standardMetricKeys...), sharedMetricKeys...),
	}
)

// keySetCache remembers which metric key set a SonarQube configuration
// accepts. It is reset when the URL or token changes.
type keySetCache struct {
	mu  sync.Mutex
	cfg [sha256.Size]byte // digest of URL and token; the token itself is not kept
	set int
}

// configDigest identifies a SonarQube configuration without retaining the
// token.
func configDigest(url, token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(url + "\x00" + token))
}

// forConfig resets the cache when the configuration differs from the last
// and returns the configuration's digest for getFor and putFor.
func (k *keySetCache) forConfig(url, token string) [sha256.Size]byte {
	d := configDigest(url, token)
	k.mu.Lock()
	defer k.mu.Unlock()
	if d != k.cfg {
		k.cfg, k.set = d, 0
	}
	return d
}

// getFor returns the remembered key set for cfg, or 0 for another
// configuration.
func (k *keySetCache) getFor(cfg [sha256.Size]byte) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	if cfg != k.cfg {
		return 0
	}
	return k.set
}

// putFor remembers a key set, unless the configuration changed meanwhile.
func (k *keySetCache) putFor(cfg [sha256.Size]byte, i int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if cfg == k.cfg {
		k.set = i
	}
}

// apiError is a failed Web API call. It carries only the endpoint and the
// status code, never the token or the response body.
type apiError struct {
	Endpoint string
	Status   int
	// MetricKeys is set when SonarQube's error message is about unknown
	// metric keys (as opposed to, for example, an unknown project).
	MetricKeys bool
}

func (e *apiError) Error() string {
	return fmt.Sprintf("SonarQube returned HTTP %d for %s", e.Status, e.Endpoint)
}

type client struct {
	http  *http.Client
	base  string
	token string
	// keys remembers the working metric key set for configuration cfg; nil
	// means no memory.
	keys *keySetCache
	cfg  [sha256.Size]byte
}

func (c *client) get(ctx context.Context, endpoint string, q url.Values) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	u := strings.TrimRight(c.base, "/") + "/" + endpoint
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid SonarQube URL", endpoint)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		// Report the cause without the request URL.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("request to %s failed: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// Only the classification is kept, never the body itself.
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, &apiError{Endpoint: endpoint, Status: resp.StatusCode,
			MetricKeys: strings.Contains(strings.ToLower(string(msg)), "metric")}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return nil, fmt.Errorf("reading %s failed: %w", endpoint, err)
	}
	return b, nil
}

// measures returns every measure SonarQube reports for the project, by
// metric key. Metrics without a value are absent.
func (c *client) measures(ctx context.Context, key string) (map[string]string, error) {
	// One deadline for the whole call, however many key sets are tried.
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	start := 0
	if c.keys != nil {
		start = c.keys.getFor(c.cfg)
	}
	// Try the remembered set first, then the others once each, so a server
	// whose mode changed is handled too.
	var err error
	for n := range metricKeySets {
		i := (start + n) % len(metricKeySets)
		var m map[string]string
		if m, err = c.measuresFor(ctx, key, metricKeySets[i]); err == nil {
			if c.keys != nil {
				c.keys.putFor(c.cfg, i)
			}
			return m, nil
		}
		// Move on only when SonarQube rejected the metric keys; an unknown
		// project, a permission problem or a server error is final.
		var ae *apiError
		if !errors.As(err, &ae) || (ae.Status != http.StatusBadRequest && ae.Status != http.StatusNotFound) || !ae.MetricKeys {
			return nil, err
		}
	}
	return nil, err
}

func (c *client) measuresFor(ctx context.Context, key string, keys []string) (map[string]string, error) {
	const endpoint = "api/measures/component"
	b, err := c.get(ctx, endpoint, url.Values{
		"component": {key}, "metricKeys": {strings.Join(keys, ",")},
	})
	if err != nil {
		return nil, err
	}
	var body struct {
		Component struct {
			Measures []struct {
				Metric string  `json:"metric"`
				Value  *string `json:"value"`
				// Metrics of the new code period carry their value here.
				Period *struct {
					Value *string `json:"value"`
				} `json:"period"`
			} `json:"measures"`
		} `json:"component"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		return nil, fmt.Errorf("%s: unreadable response", endpoint)
	}
	out := map[string]string{}
	for _, m := range body.Component.Measures {
		switch {
		case m.Value != nil:
			out[m.Metric] = *m.Value
		case m.Period != nil && m.Period.Value != nil:
			out[m.Metric] = *m.Period.Value
		}
	}
	return out, nil
}

// projectStatus returns the quality gate status and conditions.
func (c *client) projectStatus(ctx context.Context, key string) (string, []Condition, error) {
	const endpoint = "api/qualitygates/project_status"
	b, err := c.get(ctx, endpoint, url.Values{"projectKey": {key}})
	if err != nil {
		return "", nil, err
	}
	var body struct {
		ProjectStatus struct {
			Status     string `json:"status"`
			Conditions []struct {
				Status         string  `json:"status"`
				MetricKey      string  `json:"metricKey"`
				Comparator     string  `json:"comparator"`
				ErrorThreshold string  `json:"errorThreshold"`
				ActualValue    *string `json:"actualValue"`
			} `json:"conditions"`
		} `json:"projectStatus"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		return "", nil, fmt.Errorf("%s: unreadable response", endpoint)
	}
	conds := []Condition{}
	for _, c := range body.ProjectStatus.Conditions {
		conds = append(conds, Condition{
			Metric: c.MetricKey, Operator: operatorName(c.Comparator),
			Threshold: c.ErrorThreshold, Value: c.ActualValue, Status: c.Status,
		})
	}
	return body.ProjectStatus.Status, conds, nil
}

// analysis is the newest analysis of a project.
type analysis struct {
	Key      string
	Date     time.Time
	Revision string
}

// latestAnalysis returns the newest analysis, or nil when there is none.
func (c *client) latestAnalysis(ctx context.Context, key string) (*analysis, error) {
	const endpoint = "api/project_analyses/search"
	b, err := c.get(ctx, endpoint, url.Values{"project": {key}, "ps": {"1"}})
	if err != nil {
		return nil, err
	}
	var body struct {
		Analyses []struct {
			Key      string `json:"key"`
			Date     string `json:"date"`
			Revision string `json:"revision"`
		} `json:"analyses"`
	}
	if err := json.Unmarshal(b, &body); err != nil {
		return nil, fmt.Errorf("%s: unreadable response", endpoint)
	}
	if len(body.Analyses) == 0 {
		return nil, nil
	}
	a := body.Analyses[0]
	t, err := parseTime(a.Date)
	if err != nil {
		return nil, fmt.Errorf("%s: unreadable analysis date", endpoint)
	}
	return &analysis{Key: a.Key, Date: t, Revision: a.Revision}, nil
}

// serverVersion returns the SonarQube version string.
func (c *client) serverVersion(ctx context.Context) (string, error) {
	b, err := c.get(ctx, "api/server/version", nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// parseTime reads the timestamp forms SonarQube uses: ISO 8601 with a numeric
// offset without a colon (2026-11-19T10:00:00+0000), with or without
// milliseconds, and RFC 3339.
func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02T15:04:05-0700", "2006-01-02T15:04:05.000-0700", time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised time %q", s)
}
