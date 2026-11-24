// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package catalog

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestPortfolioTagsAreOneArtifact: a pipeline that tags its image per run
// (web-frontend-stage:2, :3, :4) has one artifact. The portfolio counts and
// every history point hold the latest tag only, not the sum of the tags.
func TestPortfolioTagsAreOneArtifact(t *testing.T) {
	c, pool, _ := newCatalog(t, "")
	now := time.Now().UTC()
	svc := addService(t, pool, "web-frontend", "demo/web-frontend", false)
	tags := []counts{{Critical: 3, High: 40}, {Critical: 2, High: 36}, {Critical: 2, High: 35}}
	for i, c := range tags {
		mustExec(t, pool, `INSERT INTO scans (service_id, kind, artifact_name, artifact_key, gitlab_pipeline_id,
			gitlab_job_id, ref, ref_protected, commit_sha, schema_version, scanned_at, count_critical, count_high,
			count_fixable, raw_report)
			VALUES ($1, 'image', $2, 'web-frontend-stage', $3, $4, 'main', true, 'abc', 2, $5, $6, $7, $7, '\x1f8b')`,
			svc, fmt.Sprintf("web-frontend-stage:%d", i+2), i+1, 800000+i,
			now.Add(-time.Duration(3-i)*time.Hour), c.Critical, c.High)
	}

	pf := decode[pfResponse](t, call(t, c, "getPortfolio", "GET", "/", "", ""), http.StatusOK)
	if len(pf.Items) != 1 {
		t.Fatalf("items = %+v", pf.Items)
	}
	if got := lowerCounts(pf.Items[0].Counts); got != tags[2] || pf.Items[0].Fixable != 35 {
		t.Errorf("counts = %+v, fixable %d; want %+v, 35 (the :4 scan only)", got, pf.Items[0].Fixable, tags[2])
	}
	if pf.Totals.Fixable != 35 {
		t.Errorf("total fixable = %d, want 35", pf.Totals.Fixable)
	}

	h := decode[historyResponse](t, call(t, c, "getPortfolioHistory", "GET", "/", "", ""), http.StatusOK)
	if len(h.Services) != 1 || len(h.Services[0].Points) != 3 {
		t.Fatalf("history = %+v", h.Services)
	}
	for i, p := range h.Services[0].Points {
		if p.PipelineID != int64(i+1) || lowerCounts(p.Counts) != tags[i] {
			t.Errorf("point %d = pipeline %d %+v, want pipeline %d %+v", i, p.PipelineID, p.Counts, i+1, tags[i])
		}
	}
}
