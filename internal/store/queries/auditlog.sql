-- Copyright 2026 Siyam Sarker
-- SPDX-License-Identifier: Apache-2.0

-- name: ListAuditEvents :many
SELECT id, at, actor, action, detail
FROM audit_log
WHERE (sqlc.narg(action_name)::text IS NULL OR action = sqlc.narg(action_name)::text)
  AND (sqlc.narg(actor_part)::text IS NULL
       OR strpos(lower(actor), lower(sqlc.narg(actor_part)::text)) > 0)
  AND (sqlc.narg(before_id)::bigint IS NULL OR id < sqlc.narg(before_id)::bigint)
ORDER BY id DESC
LIMIT @max_rows;
