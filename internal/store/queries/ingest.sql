-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- name: LockIngestProject :exec
-- Serialises uploads for one GitLab project until the transaction ends, so
-- concurrent uploads cannot create the same service or scan twice.
SELECT pg_advisory_xact_lock(hashtextextended('scanopsis.ingest:' || sqlc.arg(project_id)::text, 0));

-- name: GetServiceByProjectID :one
SELECT * FROM services WHERE gitlab_project_id = $1;

-- name: GetServiceByPath :one
SELECT * FROM services WHERE lower(gitlab_project_path) = lower($1);

-- name: AdoptServiceByPath :one
-- A service added by an administrator learns its project ID from the first
-- upload whose token names its path, in any letter case, and takes GitLab's
-- spelling of the path. Archived services are left alone.
UPDATE services SET gitlab_project_id = @project_id, gitlab_project_path = @project_path
WHERE lower(gitlab_project_path) = lower(@project_path) AND gitlab_project_id IS NULL AND NOT archived
RETURNING *;

-- name: RenameServicePath :execrows
-- Follows a renamed or transferred project. Does nothing when another
-- service already uses the new path.
UPDATE services SET gitlab_project_path = @project_path
WHERE services.id = @id
  AND NOT EXISTS (SELECT 1 FROM services other WHERE lower(other.gitlab_project_path) = lower(@project_path) AND other.id <> @id);

-- name: InsertIngestService :one
-- Returns no row when the path is already used by another service.
INSERT INTO services (name, gitlab_project_id, gitlab_project_path, created_via)
VALUES (@name, @project_id, @project_path, 'ingest')
ON CONFLICT DO NOTHING
RETURNING *;

-- name: DeleteScanForJob :execrows
DELETE FROM scans
WHERE service_id = $1 AND gitlab_job_id = $2 AND artifact_name = $3 AND kind = $4;

-- name: InsertScan :one
INSERT INTO scans (
    service_id, kind, artifact_name, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected,
    commit_sha, trivy_version, schema_version, scanned_at,
    count_critical, count_high, count_medium, count_low, count_unknown,
    count_fixable, count_secrets, count_suppressed, raw_report
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11,
    $12, $13, $14, $15, $16,
    $17, $18, $19, $20
)
RETURNING id;
