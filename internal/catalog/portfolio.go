// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"net/http"
	"time"

	"github.com/siyamsarker/scanopsis/internal/httpapi"
)

// portfolioSQL returns every active service with its current Trivy state for
// its dashboard ref and its latest SonarQube analysis, in one statement. The
// dashboard ref is the branch an administrator chose (default_ref), even when
// it has no scans yet, else the ref of the most recent scan (ties broken by
// ref as in ListRefs); findings.pickRef applies the same rule. The current
// state of a ref is the latest scan per (artifact_name, kind), as in
// CurrentScans. It finds the latest scan of each (artifact_name, kind) by
// jumping from one group to the next in scans_current_cover_idx (a recursive
// "skip scan") instead of reading every scan of the ref, so its cost follows
// the number of artifacts, not the number of scans in the retention window;
// the counts come from the index without reading the table. It is written by
// hand rather than generated because sqlc types the columns of LEFT JOIN
// LATERAL subqueries as not null.
const portfolioSQL = `
SELECT s.id, s.name, s.gitlab_project_id, s.gitlab_project_path, s.sonar_project_key,
       s.created_via, s.archived, s.created_at, s.default_ref,
       dr.ref, dr.source,
       COALESCE(cs.critical, 0), COALESCE(cs.high, 0), COALESCE(cs.medium, 0),
       COALESCE(cs.low, 0), COALESCE(cs.unknown, 0), COALESCE(cs.fixable, 0),
       COALESCE(cs.secrets, 0), cs.last_scan_at,
       sa.quality_gate_status,
       NULLIF(sa.reliability_rating, ''), NULLIF(sa.security_rating, ''),
       NULLIF(sa.maintainability_rating, ''), sa.coverage, sa.analysed_at
FROM services s
LEFT JOIN LATERAL (
    SELECT COALESCE(s.default_ref, l.ref) AS ref,
           CASE WHEN s.default_ref IS NOT NULL THEN 'default' ELSE 'latest' END AS source
    FROM (SELECT NULL::text) base
    LEFT JOIN LATERAL (
        SELECT ref FROM scans WHERE service_id = s.id ORDER BY scanned_at DESC, ref LIMIT 1
    ) l ON s.default_ref IS NULL
) dr ON true
LEFT JOIN LATERAL (
    WITH RECURSIVE c AS (
        (SELECT artifact_name, kind, count_critical, count_high, count_medium, count_low,
                count_unknown, count_fixable, count_secrets, scanned_at
         FROM scans
         WHERE service_id = s.id AND ref = dr.ref
         ORDER BY artifact_name, kind, scanned_at DESC, id DESC
         LIMIT 1)
        UNION ALL
        SELECT n.*
        FROM c, LATERAL (
            SELECT artifact_name, kind, count_critical, count_high, count_medium, count_low,
                   count_unknown, count_fixable, count_secrets, scanned_at
            FROM scans
            WHERE service_id = s.id AND ref = dr.ref AND (artifact_name, kind) > (c.artifact_name, c.kind)
            ORDER BY artifact_name, kind, scanned_at DESC, id DESC
            LIMIT 1
        ) n
    )
    SELECT sum(count_critical)::bigint AS critical, sum(count_high)::bigint AS high,
           sum(count_medium)::bigint AS medium, sum(count_low)::bigint AS low,
           sum(count_unknown)::bigint AS unknown, sum(count_fixable)::bigint AS fixable,
           sum(count_secrets)::bigint AS secrets, max(scanned_at) AS last_scan_at
    FROM c
) cs ON true
LEFT JOIN LATERAL (
    SELECT quality_gate_status, reliability_rating, security_rating, maintainability_rating,
           coverage, analysed_at
    FROM sonar_analyses
    WHERE service_id = s.id
    ORDER BY analysed_at DESC, id DESC
    LIMIT 1
) sa ON true
WHERE NOT s.archived
ORDER BY s.name, s.id`

type severityCounts struct {
	Critical int64 `json:"critical"`
	High     int64 `json:"high"`
	Medium   int64 `json:"medium"`
	Low      int64 `json:"low"`
	Unknown  int64 `json:"unknown"`
}

type portfolioItem struct {
	Service               serviceJSON    `json:"service"`
	Ref                   *string        `json:"ref"`
	RefSource             string         `json:"ref_source"`
	Counts                severityCounts `json:"counts"`
	Fixable               int64          `json:"fixable"`
	Secrets               int64          `json:"secrets"`
	LastScanAt            *time.Time     `json:"last_scan_at"`
	QualityGateStatus     *string        `json:"quality_gate_status"`
	ReliabilityRating     *string        `json:"reliability_rating"`
	SecurityRating        *string        `json:"security_rating"`
	MaintainabilityRating *string        `json:"maintainability_rating"`
	Coverage              *float64       `json:"coverage"`
	LastAnalysisAt        *time.Time     `json:"last_analysis_at"`
}

type portfolioTotals struct {
	Services             int   `json:"services"`
	FailingQualityGates  int   `json:"failing_quality_gates"`
	ServicesWithCritical int   `json:"services_with_critical"`
	Fixable              int64 `json:"fixable"`
}

func (c *Catalog) getPortfolio(w http.ResponseWriter, r *http.Request) {
	rows, err := c.pool.Query(r.Context(), portfolioSQL)
	if err != nil {
		c.internalError(w, r, "cannot query portfolio", err)
		return
	}
	defer rows.Close()
	items := []portfolioItem{}
	var tot portfolioTotals
	for rows.Next() {
		var it portfolioItem
		s := &it.Service
		var created time.Time
		if err := rows.Scan(&s.ID, &s.Name, &s.GitLabProjectID, &s.GitLabProjectPath, &s.SonarProjectKey,
			&s.CreatedVia, &s.Archived, &created, &s.DefaultRef,
			&it.Ref, &it.RefSource,
			&it.Counts.Critical, &it.Counts.High, &it.Counts.Medium, &it.Counts.Low, &it.Counts.Unknown,
			&it.Fixable, &it.Secrets, &it.LastScanAt,
			&it.QualityGateStatus, &it.ReliabilityRating, &it.SecurityRating, &it.MaintainabilityRating,
			&it.Coverage, &it.LastAnalysisAt); err != nil {
			c.internalError(w, r, "cannot read portfolio row", err)
			return
		}
		s.CreatedAt = created
		tot.Services++
		tot.Fixable += it.Fixable
		if it.Counts.Critical > 0 {
			tot.ServicesWithCritical++
		}
		if it.QualityGateStatus != nil && *it.QualityGateStatus == "ERROR" {
			tot.FailingQualityGates++
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		c.internalError(w, r, "cannot read portfolio", err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, struct {
		Items       []portfolioItem `json:"items"`
		Totals      portfolioTotals `json:"totals"`
		GeneratedAt time.Time       `json:"generated_at"`
	}{items, tot, time.Now().UTC()})
}
