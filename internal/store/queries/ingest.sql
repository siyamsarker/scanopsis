-- Copyright 2026 Siyam Sarker
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
-- The previous spelling is returned for the audit record.
WITH old AS (
    SELECT id, gitlab_project_path FROM services
    WHERE lower(gitlab_project_path) = lower(@project_path) AND gitlab_project_id IS NULL AND NOT archived
    FOR UPDATE
)
UPDATE services SET gitlab_project_id = @project_id, gitlab_project_path = @project_path
FROM old
WHERE services.id = old.id
RETURNING sqlc.embed(services), old.gitlab_project_path AS previous_path;

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

-- LockIngestService locks the service row of an upload until the upload's
-- transaction ends and returns its SonarQube key, declared key and archived
-- flag as they are now. An administrator's archive or delete of the service
-- waits for the upload, and the upload sees one committed before it: no
-- row means the service was deleted. A declared key is decided on while no
-- webhook can link the service. FOR NO KEY UPDATE, as only other columns
-- change: rows that reference the service (scans) can still be inserted
-- meanwhile.
-- name: LockIngestService :one
SELECT sonar_project_key, sonar_key_declared, archived FROM services WHERE id = $1 FOR NO KEY UPDATE;

-- name: DeleteScanForJob :execrows
-- A repeated job replaces its own scan of the same reported name, whatever
-- the artifact key.
DELETE FROM scans
WHERE service_id = $1 AND gitlab_job_id = $2 AND artifact_name = $3 AND kind = $4;

-- name: InsertScan :one
INSERT INTO scans (
    service_id, kind, artifact_name, artifact_key, gitlab_pipeline_id, gitlab_job_id, ref, ref_protected,
    commit_sha, trivy_version, schema_version, scanned_at,
    count_critical, count_high, count_medium, count_low, count_unknown,
    count_fixable, count_secrets, count_suppressed, raw_report
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8,
    $9, $10, $11, $12,
    $13, $14, $15, $16, $17,
    $18, $19, $20, $21
)
RETURNING id;
