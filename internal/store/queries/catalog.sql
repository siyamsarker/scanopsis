-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- name: ListServices :many
SELECT id, name, gitlab_project_id, gitlab_project_path, sonar_project_key,
       created_via, archived, created_at
FROM services
WHERE id > @after AND archived = @archived
  AND (@q::text = '' OR strpos(lower(name), lower(@q)) > 0
                     OR strpos(lower(gitlab_project_path), lower(@q)) > 0)
ORDER BY id
LIMIT @max_rows;

-- name: GetService :one
SELECT id, name, gitlab_project_id, gitlab_project_path, sonar_project_key,
       created_via, archived, created_at
FROM services WHERE id = $1;

-- name: CreateService :one
INSERT INTO services (name, gitlab_project_path, sonar_project_key, created_via)
VALUES (@name, @gitlab_project_path, sqlc.narg(sonar_project_key), 'manual')
RETURNING id, name, gitlab_project_id, gitlab_project_path, sonar_project_key,
          created_via, archived, created_at;

-- name: UpdateService :one
UPDATE services SET
    name = COALESCE(sqlc.narg(name), name),
    sonar_project_key = CASE WHEN @set_sonar_key::boolean THEN sqlc.narg(sonar_project_key) ELSE sonar_project_key END,
    archived = COALESCE(sqlc.narg(archived), archived)
WHERE id = @id
RETURNING id, name, gitlab_project_id, gitlab_project_path, sonar_project_key,
          created_via, archived, created_at;

-- name: DeleteService :one
DELETE FROM services WHERE id = $1 RETURNING gitlab_project_path;

-- name: TrivyIntegrationStats :one
SELECT (SELECT max(received_at) FROM scans)::timestamptz AS last_upload_at,
       (SELECT count(*) FROM scans WHERE received_at > now() - interval '24 hours')::bigint AS uploads_24h,
       (SELECT count(*) FROM audit_log WHERE action = 'ingest.rejected'
                                         AND at > now() - interval '24 hours')::bigint AS rejections_24h;

-- name: RecentIngestRejections :many
SELECT at, detail FROM audit_log
WHERE action = 'ingest.rejected'
ORDER BY at DESC, id DESC
LIMIT 20;
