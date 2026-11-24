-- Copyright 2026 Siyam Sarker
-- SPDX-License-Identifier: Apache-2.0

-- name: ListServices :many
SELECT id, name, gitlab_project_id, gitlab_project_path, sonar_project_key,
       created_via, archived, created_at, default_ref, sonar_key_declared, sonar_key_declared_at
FROM services
WHERE id > @after AND archived = @archived
  AND (@q::text = '' OR strpos(lower(name), lower(@q)) > 0
                     OR strpos(lower(gitlab_project_path), lower(@q)) > 0)
ORDER BY id
LIMIT @max_rows;

-- name: GetService :one
SELECT id, name, gitlab_project_id, gitlab_project_path, sonar_project_key,
       created_via, archived, created_at, default_ref, sonar_key_declared, sonar_key_declared_at
FROM services WHERE id = $1;

-- name: CreateService :one
INSERT INTO services (name, gitlab_project_path, sonar_project_key, created_via)
VALUES (@name, @gitlab_project_path, sqlc.narg(sonar_project_key), 'manual')
RETURNING id, name, gitlab_project_id, gitlab_project_path, sonar_project_key,
          created_via, archived, created_at, default_ref, sonar_key_declared, sonar_key_declared_at;

-- UpdateService applies the given changes and also returns the previous
-- default ref and declared SonarQube key, read under the same row lock, for
-- the audit event. An administrator who sets or clears the SonarQube key
-- also clears the key the service's pipeline declared: the administrator's
-- choice wins.
-- name: UpdateService :one
UPDATE services SET
    name = COALESCE(sqlc.narg(name), services.name),
    sonar_project_key = CASE WHEN @set_sonar_key::boolean THEN sqlc.narg(sonar_project_key) ELSE services.sonar_project_key END,
    sonar_key_declared = CASE WHEN @set_sonar_key::boolean THEN NULL ELSE services.sonar_key_declared END,
    sonar_key_declared_at = CASE WHEN @set_sonar_key::boolean THEN NULL ELSE services.sonar_key_declared_at END,
    archived = COALESCE(sqlc.narg(archived), services.archived),
    default_ref = CASE WHEN @set_default_ref::boolean THEN sqlc.narg(default_ref) ELSE services.default_ref END
FROM (SELECT p.id, p.default_ref, p.sonar_key_declared FROM services p WHERE p.id = @id FOR UPDATE) old
WHERE services.id = old.id
RETURNING sqlc.embed(services), old.default_ref AS previous_default_ref,
          old.sonar_key_declared AS previous_sonar_key_declared;

-- name: DeleteService :one
DELETE FROM services WHERE id = $1 RETURNING gitlab_project_path;

-- name: TrivyIntegrationStats :one
SELECT (SELECT max(received_at) FROM scans)::timestamptz AS last_upload_at,
       (SELECT count(*) FROM scans WHERE received_at > now() - interval '24 hours')::bigint AS uploads_24h,
       (SELECT count(*) FROM audit_log WHERE action = 'ingest.rejected'
                                         AND at > now() - interval '24 hours')::bigint AS rejections_24h;

-- RecentIngestRejections lists the newest rejected uploads with the service
-- registered for the project path, when there is one.
-- name: RecentIngestRejections :many
SELECT a.at, a.detail, s.id AS service_id, s.name AS service_name
FROM audit_log a
LEFT JOIN services s ON lower(s.gitlab_project_path) = lower(a.detail->>'project_path')
WHERE a.action = 'ingest.rejected'
ORDER BY a.at DESC, a.id DESC
LIMIT 20;

-- TrivyDailyActivity counts uploads and recorded rejections per UTC day over
-- the last 30 days, oldest first. Days without either are included with
-- zeros. Both counts read only their day range through the received_at and
-- (action, at) indexes.
-- name: TrivyDailyActivity :many
WITH bounds AS (
    SELECT ((now() AT TIME ZONE 'UTC')::date - 29) AS first_day
), days AS (
    SELECT (bounds.first_day + n)::date AS day
    FROM bounds, generate_series(0, 29) AS n
), uploads AS (
    SELECT (sc.received_at AT TIME ZONE 'UTC')::date AS day, count(*) AS n
    FROM scans sc, bounds
    WHERE sc.received_at >= (bounds.first_day::timestamp AT TIME ZONE 'UTC')
    GROUP BY 1
), rejections AS (
    SELECT (al.at AT TIME ZONE 'UTC')::date AS day, count(*) AS n
    FROM audit_log al, bounds
    WHERE al.action = 'ingest.rejected'
      AND al.at >= (bounds.first_day::timestamp AT TIME ZONE 'UTC')
    GROUP BY 1
)
SELECT days.day::date AS day,
       coalesce(uploads.n, 0)::bigint AS uploads,
       coalesce(rejections.n, 0)::bigint AS rejections
FROM days
LEFT JOIN uploads ON uploads.day = days.day
LEFT JOIN rejections ON rejections.day = days.day
ORDER BY days.day;
