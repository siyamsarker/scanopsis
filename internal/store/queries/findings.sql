-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- Read queries of the findings API. "Current scans" are the ids returned by
-- CurrentScans; findings are always read by scan_id so the (scan_id, id)
-- index is used. Nothing here selects raw_report.

-- name: FindingsGetService :one
SELECT id, name, gitlab_project_id, gitlab_project_path, sonar_project_key,
       created_via, archived, created_at, default_ref
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
-- The filtered rows are materialized and then sorted: without that, the
-- planner may walk the primary key in id order, expecting to meet the first
-- rows of the scans soon, and for a service whose scans are the newest in the
-- table read every finding ever stored (half a second at four million rows).
-- name: FindingsVulnerabilityPage :many
WITH hits AS MATERIALIZED (
    SELECT v.id, v.scan_id, v.target, v.class, v.pkg_type, v.pkg_name, v.pkg_path,
           v.installed_version, v.fixed_version, v.vuln_id, v.severity, v.status, v.title,
           v.primary_url, v.suppressed, v.suppression
    FROM vulnerabilities v
    WHERE v.scan_id = ANY(@scan_ids::bigint[])
      AND v.id > @after
      AND (@include_suppressed::boolean OR NOT v.suppressed)
      AND (cardinality(@severities::text[]) = 0 OR v.severity = ANY(@severities::text[]))
      AND (sqlc.narg('fixable')::boolean IS NULL OR (v.fixed_version <> '') = sqlc.narg('fixable')::boolean)
      AND (@q::text = ''
           OR strpos(lower(v.pkg_name), lower(@q)) > 0
           OR strpos(lower(v.vuln_id), lower(@q)) > 0
           OR strpos(lower(v.title), lower(@q)) > 0)
)
SELECT v.id, v.scan_id, s.artifact_name, v.target, v.class, v.pkg_type, v.pkg_name, v.pkg_path,
       v.installed_version, v.fixed_version, v.vuln_id, v.severity, v.status, v.title,
       v.primary_url, v.suppressed, v.suppression
FROM hits v
JOIN scans s ON s.id = v.scan_id
ORDER BY v.id
LIMIT @max_rows;

-- FindingsFirstSeen returns, for each given vulnerability row, the scan time of
-- the earliest scan of the same service, ref, artifact and kind that contains
-- the same (vuln_id, pkg_name, target), suppressed or not. The md5 condition
-- is the expression of vulnerabilities_scan_key_idx, which makes the lookup
-- an index probe; the column comparisons rule out hash collisions.
-- name: FindingsFirstSeen :many
SELECT v.id, min(s2.scanned_at)::timestamptz AS first_seen_at
FROM vulnerabilities v
JOIN scans s ON s.id = v.scan_id
JOIN scans s2 ON s2.service_id = s.service_id AND s2.ref = s.ref
             AND s2.artifact_name = s.artifact_name AND s2.kind = s.kind
JOIN vulnerabilities v2 ON v2.scan_id = s2.id
                       AND md5(v2.vuln_id || chr(1) || v2.pkg_name || chr(1) || v2.target)
                         = md5(v.vuln_id || chr(1) || v.pkg_name || chr(1) || v.target)
                       AND v2.vuln_id = v.vuln_id AND v2.pkg_name = v.pkg_name AND v2.target = v.target
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
-- current state (latest scan per ref, artifact and kind) of the non-archived
-- services. The ID matches case-insensitively and exactly.
--
-- @all_refs says which refs are searched. When false, the default, a
-- service's current state is searched for
--   * the ref its dashboards show: the branch an administrator chose
--     (services.default_ref), else the ref of its most recent scan, ties
--     broken by ref (the rule of findings.pickRef and of the portfolio
--     query), and
--   * every protected ref: a (ref, artifact, kind) whose latest scan has
--     ref_protected set (the rule data retention uses to keep a scan).
-- When true, every ref of every service is searched.
--
-- It first finds the current scans by jumping from one (service, ref,
-- artifact, kind) group to the next (a recursive "skip scan"), then probes
-- vulnerabilities_vuln_id_scan_idx once per current scan. The work therefore
-- follows the number of current scans in scope, not the number of historical
-- findings with that ID: an ID that is in most scans costs the same as a rare
-- one. For the dashboard refs the jump stays inside one (service, ref) in
-- scans_current_cover_idx. The protected groups are found by the same jump in
-- scans_protected_idx, which holds only scans of protected refs, and each is
-- checked against the latest scan of its group. Only @all_refs reads every
-- group, which is about 100000 at 500 services after 90 days of branches.
-- OFFSET 0 keeps the planner from turning the per-scan probe into a join over
-- every historical match.
--
-- The most severe findings come first and at most @max_rows rows are
-- returned; every row carries the true totals (matching current findings and
-- the services they are in, within the scope searched), so the caller can
-- report a cut list honestly.
-- name: FindingsSearch :many
WITH RECURSIVE all_cur AS (
    SELECT * FROM (
        SELECT id, service_id, ref, artifact_name, kind, scanned_at
        FROM scans
        ORDER BY service_id, ref, artifact_name, kind, scanned_at DESC, id DESC
        LIMIT 1
    ) first
    WHERE @all_refs::boolean
    UNION ALL
    SELECT n.*
    FROM all_cur, LATERAL (
        SELECT id, service_id, ref, artifact_name, kind, scanned_at
        FROM scans
        WHERE (service_id, ref, artifact_name, kind) > (all_cur.service_id, all_cur.ref, all_cur.artifact_name, all_cur.kind)
        ORDER BY service_id, ref, artifact_name, kind, scanned_at DESC, id DESC
        LIMIT 1
    ) n
), dash AS (
    SELECT sv.id AS service_id, COALESCE(sv.default_ref, l.ref) AS ref
    FROM services sv
    LEFT JOIN LATERAL (
        SELECT ref FROM scans WHERE service_id = sv.id ORDER BY scanned_at DESC, ref LIMIT 1
    ) l ON sv.default_ref IS NULL
    WHERE NOT sv.archived AND NOT @all_refs::boolean
), dash_cur AS (
    SELECT f.*
    FROM dash d, LATERAL (
        SELECT id, service_id, ref, artifact_name, kind, scanned_at
        FROM scans
        WHERE service_id = d.service_id AND ref = d.ref
        ORDER BY artifact_name, kind, scanned_at DESC, id DESC
        LIMIT 1
    ) f
    UNION ALL
    SELECT n.*
    FROM dash_cur, LATERAL (
        SELECT id, service_id, ref, artifact_name, kind, scanned_at
        FROM scans
        WHERE service_id = dash_cur.service_id AND ref = dash_cur.ref
          AND (artifact_name, kind) > (dash_cur.artifact_name, dash_cur.kind)
        ORDER BY artifact_name, kind, scanned_at DESC, id DESC
        LIMIT 1
    ) n
), prot_key AS (
    SELECT * FROM (
        SELECT service_id, ref, artifact_name, kind
        FROM scans
        WHERE ref_protected
        ORDER BY service_id, ref, artifact_name, kind
        LIMIT 1
    ) first
    WHERE NOT @all_refs::boolean
    UNION ALL
    SELECT n.*
    FROM prot_key, LATERAL (
        SELECT service_id, ref, artifact_name, kind
        FROM scans
        WHERE ref_protected
          AND (service_id, ref, artifact_name, kind) > (prot_key.service_id, prot_key.ref, prot_key.artifact_name, prot_key.kind)
        ORDER BY service_id, ref, artifact_name, kind
        LIMIT 1
    ) n
), prot_cur AS (
    SELECT l.id, l.service_id, l.ref, l.artifact_name, l.kind, l.scanned_at
    FROM prot_key k, LATERAL (
        SELECT id, service_id, ref, artifact_name, kind, scanned_at, ref_protected
        FROM scans
        WHERE service_id = k.service_id AND ref = k.ref AND artifact_name = k.artifact_name AND kind = k.kind
        ORDER BY scanned_at DESC, id DESC
        LIMIT 1
    ) l
    WHERE l.ref_protected
), cur AS (
    SELECT * FROM all_cur
    UNION
    SELECT * FROM dash_cur
    UNION
    SELECT * FROM prot_cur
), found AS MATERIALIZED (
    SELECT cur.service_id, cur.ref, cur.artifact_name, cur.id AS scan_id, cur.scanned_at,
           h.id AS vuln_row_id, h.pkg_name, h.installed_version, h.fixed_version, h.severity
    FROM cur
    JOIN services s0 ON s0.id = cur.service_id AND NOT s0.archived
    CROSS JOIN LATERAL (
        SELECT v.id, v.pkg_name, v.installed_version, v.fixed_version, v.severity
        FROM vulnerabilities v
        WHERE v.scan_id = cur.id
          AND lower(left(v.vuln_id, 100)) = lower(@vuln_id::text)
          AND lower(v.vuln_id) = lower(@vuln_id::text)
          AND NOT v.suppressed
        OFFSET 0
    ) h
), totals AS (
    SELECT count(*)::bigint AS total_findings, count(DISTINCT service_id)::bigint AS total_services
    FROM found
)
SELECT sv.id, sv.name, sv.gitlab_project_id, sv.gitlab_project_path, sv.sonar_project_key,
       sv.created_via, sv.archived, sv.created_at, sv.default_ref,
       f.ref, f.artifact_name, f.scan_id, f.scanned_at,
       f.pkg_name, f.installed_version, f.fixed_version, f.severity,
       totals.total_findings, totals.total_services
FROM found f
JOIN services sv ON sv.id = f.service_id
CROSS JOIN totals
ORDER BY CASE f.severity WHEN 'CRITICAL' THEN 0 WHEN 'HIGH' THEN 1 WHEN 'MEDIUM' THEN 2 WHEN 'LOW' THEN 3 ELSE 4 END,
         sv.name, sv.id, f.ref, f.artifact_name, f.scan_id, f.pkg_name, f.installed_version, f.vuln_row_id
LIMIT @max_rows;
