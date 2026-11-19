-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetLocalUserByUsername :one
SELECT * FROM users WHERE source = 'local' AND username = $1;

-- name: CountAdmins :one
SELECT count(*) FROM users WHERE role = 'admin';

-- name: CreateLocalUser :one
INSERT INTO users (source, username, role, password_hash)
VALUES ('local', $1, $2, $3)
RETURNING *;

-- UpsertGitLabUser records a GitLab sign-in. A new user always gets the
-- viewer role; an existing user keeps their role.
-- name: UpsertGitLabUser :one
INSERT INTO users (source, gitlab_user_id, username, name, email, role, last_login_at)
VALUES ('gitlab', @gitlab_user_id, @username, @name, @email,
        'viewer', now())
ON CONFLICT (gitlab_user_id) DO UPDATE
SET username      = EXCLUDED.username,
    name          = EXCLUDED.name,
    email         = EXCLUDED.email,
    last_login_at = now()
RETURNING *;

-- name: TouchLastLogin :exec
UPDATE users SET last_login_at = now() WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users
WHERE id > @after_id
ORDER BY id
LIMIT @max_rows;

-- name: LockAdmins :many
SELECT id FROM users WHERE role = 'admin' FOR UPDATE;

-- name: SetUserRole :one
UPDATE users SET role = @role WHERE id = @id RETURNING *;
