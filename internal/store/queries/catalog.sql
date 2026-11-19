-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- name: ListServices :many
SELECT id, name, gitlab_project_id, gitlab_project_path, sonar_project_key,
       created_via, archived, created_at, default_ref
FROM services
WHERE id > @after AND archived = @archived
  AND (@q::text = '' OR strpos(lower(name), lower(@q)) > 0
                     OR strpos(lower(gitlab_project_path), lower(@q)) > 0)
ORDER BY id
LIMIT @max_rows;

-- name: GetService :one
SELECT id, name, gitlab_project_id, gitlab_project_path, sonar_project_key,
       created_via, archived, created_at, default_ref
FROM services WHERE id = $1;

-- name: CreateService :one
INSERT INTO services (name, gitlab_project_path, sonar_project_key, created_via)
VALUES (@name, @gitlab_project_path, sqlc.narg(sonar_project_key), 'manual')
RETURNING id, name, gitlab_project_id, gitlab_project_path, sonar_project_key,
          created_via, archived, created_at, default_ref;

-- UpdateService applies the given changes and also returns the previous
-- default ref, read under the same row lock, for the audit event.
-- name: UpdateService :one
UPDATE services SET
    name = COALESCE(sqlc.narg(name), services.name),
    sonar_project_key = CASE WHEN @set_sonar_key::boolean THEN sqlc.narg(sonar_project_key) ELSE services.sonar_project_key END,
    archived = COALESCE(sqlc.narg(archived), services.archived),
    default_ref = CASE WHEN @set_default_ref::boolean THEN sqlc.narg(default_ref) ELSE services.default_ref END
FROM (SELECT p.id, p.default_ref FROM services p WHERE p.id = @id FOR UPDATE) old
WHERE services.id = old.id
RETURNING sqlc.embed(services), old.default_ref AS previous_default_ref;

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
