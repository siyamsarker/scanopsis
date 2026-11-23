-- Copyright 2026 Siyam Sarker
-- SPDX-License-Identifier: Apache-2.0

-- name: CreateSession :exec
INSERT INTO sessions (id_hash, user_id, expires_at, client_ip, user_agent)
VALUES (@id_hash, @user_id, @expires_at, @client_ip, @user_agent);

-- GetSession returns a session that is neither past its absolute expiry nor
-- idle since before idle_cutoff, together with its user. A disabled user has
-- no valid session, even if one was left behind.
-- name: GetSession :one
SELECT sqlc.embed(sessions), sqlc.embed(users)
FROM sessions
JOIN users ON users.id = sessions.user_id
WHERE sessions.id_hash = @id_hash
  AND sessions.expires_at > @now
  AND sessions.last_seen_at > @idle_cutoff
  AND users.disabled_at IS NULL;

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

-- name: DeleteOtherUserSessions :execrows
DELETE FROM sessions WHERE user_id = @user_id AND id_hash <> @keep_id_hash;

-- ListUserSessions returns the user's sessions that are neither past their
-- absolute expiry nor idle since before idle_cutoff, most recently used
-- first. The token hash is returned only to recognise the current session.
-- name: ListUserSessions :many
SELECT id, id_hash, created_at, last_seen_at, expires_at, mfa_verified, client_ip, user_agent
FROM sessions
WHERE user_id = @user_id
  AND expires_at > @now
  AND last_seen_at > @idle_cutoff
ORDER BY last_seen_at DESC, id DESC
LIMIT @max_rows;

-- DeleteUserSession ends one session of a user, never the one whose token
-- hash is keep_id_hash. No row means there is no such other session.
-- name: DeleteUserSession :execrows
DELETE FROM sessions
WHERE id = @id AND user_id = @user_id AND id_hash <> @keep_id_hash;
