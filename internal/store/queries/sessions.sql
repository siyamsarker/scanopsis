-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- name: CreateSession :exec
INSERT INTO sessions (id_hash, user_id, expires_at) VALUES ($1, $2, $3);

-- GetSession returns a session that is neither past its absolute expiry nor
-- idle since before idle_cutoff, together with its user.
-- name: GetSession :one
SELECT sqlc.embed(sessions), sqlc.embed(users)
FROM sessions
JOIN users ON users.id = sessions.user_id
WHERE sessions.id_hash = @id_hash
  AND sessions.expires_at > @now
  AND sessions.last_seen_at > @idle_cutoff;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = @now WHERE id_hash = @id_hash;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id_hash = $1;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= @now OR last_seen_at <= @idle_cutoff;

-- PromoteSession gives a session that has passed the second factor a new
-- token hash and marks it verified, in one statement: the old hash stops
-- working at once and the expiry, creation and last-seen times stay. No row
-- means the old session no longer exists.
-- name: PromoteSession :one
UPDATE sessions SET id_hash = @new_id_hash, mfa_verified = true
WHERE id_hash = @old_id_hash
RETURNING expires_at;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteOtherUserSessions :exec
DELETE FROM sessions WHERE user_id = @user_id AND id_hash <> @keep_id_hash;
