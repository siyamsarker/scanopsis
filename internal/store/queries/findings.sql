-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- Read queries of the findings API. "Current scans" are the ids returned by
-- CurrentScans; findings are always read by scan_id so the (scan_id, id)
-- index is used. Nothing here selects raw_report.

-- name: FindingsGetService :one
SELECT id, name, gitlab_project_id, gitlab_project_path, sonar_project_key,
       created_via, archived, created_at
FROM services
WHERE id = $1;

-- FindingsScansPage returns scans of one ref, newest first by (scanned_at, id).
-- before is the id of the last scan of the previous page, or 0; the page
-- continues strictly after that scan in the same order.
-- name: FindingsScansPage :many
SELECT s.id, s.service_id, s.kind, s.artifact_name, s.gitlab_pipeline_id, s.gitlab_job_id, s.ref,
       s.ref_protected, s.commit_sha, s.trivy_version, s.schema_version, s.scanned_at, s.received_at,
       s.count_critical, s.count_high, s.count_medium, s.count_low, s.count_unknown,
       s.count_fixable, s.count_secrets, s.count_suppressed
FROM scans s
WHERE s.service_id = @service_id AND s.ref = @ref
  AND (@before::bigint = 0 OR (s.scanned_at, s.id) < (SELECT b.scanned_at, b.id FROM scans b WHERE b.id = @before))
ORDER BY s.scanned_at DESC, s.id DESC
LIMIT @max_rows;

-- name: FindingsGetScan :one
SELECT id, service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref,
       ref_protected, commit_sha, trivy_version, schema_version, scanned_at, received_at,
       count_critical, count_high, count_medium, count_low, count_unknown,
       count_fixable, count_secrets, count_suppressed
FROM scans
WHERE id = $1;

-- FindingsPreviousScan returns the id of the scan before this one for the same
-- service, ref, artifact and kind.
-- name: FindingsPreviousScan :one
SELECT p.id
FROM scans c
JOIN scans p ON p.service_id = c.service_id AND p.ref = c.ref
            AND p.artifact_name = c.artifact_name AND p.kind = c.kind
            AND (p.scanned_at, p.id) < (c.scanned_at, c.id)
WHERE c.id = $1
ORDER BY p.scanned_at DESC, p.id DESC
LIMIT 1;

-- FindingsClassCounts counts unsuppressed vulnerabilities per Result class
-- and severity over the given scans.
-- name: FindingsClassCounts :many
SELECT class, severity, count(*)::bigint AS findings
FROM vulnerabilities
WHERE scan_id = ANY(@scan_ids::bigint[]) AND NOT suppressed
GROUP BY class, severity;

-- FindingsPackageGroups counts unsuppressed vulnerabilities per package,
-- installed version and severity, with the fixed versions reported.
-- name: FindingsPackageGroups :many
SELECT pkg_name, installed_version, severity, count(*)::bigint AS findings,
       coalesce(array_agg(DISTINCT fixed_version) FILTER (WHERE fixed_version <> ''), '{}')::text[] AS fixed_versions
FROM vulnerabilities
WHERE scan_id = ANY(@scan_ids::bigint[]) AND NOT suppressed
GROUP BY pkg_name, installed_version, severity;

-- FindingsDiffCounts sums, over the given scans, the findings new and fixed
-- against each scan's previous scan. Keys are (vuln_id, pkg_name, target) of
-- unsuppressed vulnerabilities, counted once per scan; a scan without a
-- previous scan has all its keys new.
-- name: FindingsDiffCounts :one
WITH pairs AS (
    SELECT c.id AS cur_id,
           (SELECT p.id FROM scans p
             WHERE p.service_id = c.service_id AND p.ref = c.ref
               AND p.artifact_name = c.artifact_name AND p.kind = c.kind
               AND (p.scanned_at, p.id) < (c.scanned_at, c.id)
             ORDER BY p.scanned_at DESC, p.id DESC
             LIMIT 1) AS prev_id
    FROM scans c
    WHERE c.id = ANY(@scan_ids::bigint[])
)
SELECT coalesce(sum(n.cnt), 0)::bigint AS new_count,
       coalesce(sum(f.cnt), 0)::bigint AS fixed_count
FROM pairs p
CROSS JOIN LATERAL (
    SELECT count(*) AS cnt FROM (
        SELECT vuln_id, pkg_name, target FROM vulnerabilities WHERE scan_id = p.cur_id AND NOT suppressed
        EXCEPT
        SELECT vuln_id, pkg_name, target FROM vulnerabilities WHERE scan_id = p.prev_id AND NOT suppressed
    ) x
) n
CROSS JOIN LATERAL (
    SELECT count(*) AS cnt FROM (
        SELECT vuln_id, pkg_name, target FROM vulnerabilities WHERE scan_id = p.prev_id AND NOT suppressed
        EXCEPT
        SELECT vuln_id, pkg_name, target FROM vulnerabilities WHERE scan_id = p.cur_id AND NOT suppressed
    ) x
) f;

-- FindingsDiffRows returns the unsuppressed vulnerabilities of a scan and its
-- previous scan, most severe first, for the diff.
-- name: FindingsDiffRows :many
SELECT v.id, v.scan_id, s.artifact_name, v.target, v.class, v.pkg_type, v.pkg_name, v.pkg_path,
       v.installed_version, v.fixed_version, v.vuln_id, v.severity, v.status, v.title,
       v.primary_url, v.suppressed, v.suppression
FROM vulnerabilities v
JOIN scans s ON s.id = v.scan_id
WHERE v.scan_id = ANY(@scan_ids::bigint[]) AND NOT v.suppressed
ORDER BY CASE v.severity WHEN 'CRITICAL' THEN 0 WHEN 'HIGH' THEN 1 WHEN 'MEDIUM' THEN 2 WHEN 'LOW' THEN 3 ELSE 4 END, v.id;

-- FindingsVulnerabilityPage returns one page, by id, of the vulnerabilities of
-- the given (current) scans. severities may be empty; fixable and q are
-- optional filters (q is a case-insensitive substring of package, ID or title).
-- name: FindingsVulnerabilityPage :many
SELECT v.id, v.scan_id, s.artifact_name, v.target, v.class, v.pkg_type, v.pkg_name, v.pkg_path,
       v.installed_version, v.fixed_version, v.vuln_id, v.severity, v.status, v.title,
       v.primary_url, v.suppressed, v.suppression
FROM vulnerabilities v
JOIN scans s ON s.id = v.scan_id
WHERE v.scan_id = ANY(@scan_ids::bigint[])
  AND v.id > @after
  AND (@include_suppressed::boolean OR NOT v.suppressed)
  AND (cardinality(@severities::text[]) = 0 OR v.severity = ANY(@severities::text[]))
  AND (sqlc.narg('fixable')::boolean IS NULL OR (v.fixed_version <> '') = sqlc.narg('fixable')::boolean)
  AND (@q::text = ''
       OR strpos(lower(v.pkg_name), lower(@q)) > 0
       OR strpos(lower(v.vuln_id), lower(@q)) > 0
       OR strpos(lower(v.title), lower(@q)) > 0)
ORDER BY v.id
LIMIT @max_rows;

-- FindingsFirstSeen returns, for each given vulnerability row, the scan time of
-- the earliest scan of the same service, ref, artifact and kind that contains
-- the same (vuln_id, pkg_name, target), suppressed or not.
-- name: FindingsFirstSeen :many
SELECT v.id, min(s2.scanned_at)::timestamptz AS first_seen_at
FROM vulnerabilities v
JOIN scans s ON s.id = v.scan_id
JOIN scans s2 ON s2.service_id = s.service_id AND s2.ref = s.ref
             AND s2.artifact_name = s.artifact_name AND s2.kind = s.kind
JOIN vulnerabilities v2 ON v2.scan_id = s2.id AND v2.vuln_id = v.vuln_id
                       AND v2.pkg_name = v.pkg_name AND v2.target = v.target
WHERE v.id = ANY(@ids::bigint[])
GROUP BY v.id;

-- name: FindingsSecrets :many
SELECT f.id, f.scan_id, s.artifact_name, f.target, f.rule_id, f.category, f.severity,
       f.title, f.start_line, f.end_line, f.suppressed
FROM secret_findings f
JOIN scans s ON s.id = f.scan_id
WHERE f.scan_id = ANY(@scan_ids::bigint[])
ORDER BY CASE f.severity WHEN 'CRITICAL' THEN 0 WHEN 'HIGH' THEN 1 WHEN 'MEDIUM' THEN 2 WHEN 'LOW' THEN 3 ELSE 4 END, f.id;

-- name: FindingsTrends :many
SELECT id, kind, scanned_at, gitlab_pipeline_id, commit_sha, artifact_name,
       count_critical, count_high, count_medium, count_low, count_unknown, count_fixable
FROM scans
WHERE service_id = @service_id AND ref = @ref AND scanned_at >= @since
ORDER BY scanned_at, id;

-- FindingsSearch lists the unsuppressed findings of a vulnerability ID in the
-- current state (latest scan per ref, artifact and kind) of every
-- non-archived service. The ID matches case-insensitively and exactly.
-- name: FindingsSearch :many
WITH cur AS (
    SELECT DISTINCT ON (s.service_id, s.ref, s.artifact_name, s.kind)
           s.id AS scan_id, s.service_id, s.ref, s.artifact_name, s.scanned_at
    FROM scans s
    JOIN services sv ON sv.id = s.service_id
    WHERE NOT sv.archived
    ORDER BY s.service_id, s.ref, s.artifact_name, s.kind, s.scanned_at DESC, s.id DESC
)
SELECT sv.id, sv.name, sv.gitlab_project_id, sv.gitlab_project_path, sv.sonar_project_key,
       sv.created_via, sv.archived, sv.created_at,
       cur.ref, cur.artifact_name, cur.scan_id, cur.scanned_at,
       v.pkg_name, v.installed_version, v.fixed_version, v.severity
FROM cur
JOIN services sv ON sv.id = cur.service_id
JOIN vulnerabilities v ON v.scan_id = cur.scan_id
WHERE lower(v.vuln_id) = lower(@vuln_id) AND NOT v.suppressed
ORDER BY sv.name, sv.id, cur.ref, cur.artifact_name, cur.scan_id, v.pkg_name, v.installed_version, v.id;
