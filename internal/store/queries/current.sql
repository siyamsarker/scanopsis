-- Copyright 2026 Siyam Sarker
-- SPDX-License-Identifier: Apache-2.0

-- The "current state" of a service for a ref is the latest scan per
-- (artifact_key, kind): every tag of an image counts as one artifact.
-- Lists never select raw_report.

-- ListRefs returns the refs of a service with scans, most recent first.
-- name: ListRefs :many
SELECT ref, max(scanned_at)::timestamptz AS last_scanned_at
FROM scans
WHERE service_id = $1
GROUP BY ref
ORDER BY last_scanned_at DESC, ref;

-- CurrentScans returns the latest scan per artifact and kind for a ref.
-- name: CurrentScans :many
SELECT DISTINCT ON (artifact_key, kind)
       id, service_id, kind, artifact_name, artifact_key, gitlab_pipeline_id, gitlab_job_id, ref,
       ref_protected, commit_sha, trivy_version, schema_version, scanned_at, received_at,
       count_critical, count_high, count_medium, count_low, count_unknown,
       count_fixable, count_secrets, count_suppressed
FROM scans
WHERE service_id = @service_id AND ref = @ref
ORDER BY artifact_key, kind, scanned_at DESC, id DESC;
