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
-- viewer role; an existing user keeps their role and stays disabled if an
-- administrator disabled the account, in which case the sign-in time is not
-- updated (the caller refuses the sign-in).
-- name: UpsertGitLabUser :one
INSERT INTO users (source, gitlab_user_id, username, name, email, role, last_login_at)
VALUES ('gitlab', @gitlab_user_id, @username, @name, @email,
        'viewer', now())
ON CONFLICT (gitlab_user_id) DO UPDATE
SET username      = EXCLUDED.username,
    name          = EXCLUDED.name,
    email         = EXCLUDED.email,
    last_login_at = CASE WHEN users.disabled_at IS NULL THEN now() ELSE users.last_login_at END
RETURNING *;

-- name: TouchLastLogin :exec
UPDATE users SET last_login_at = now() WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users
WHERE id > @after_id
ORDER BY id
LIMIT @max_rows;

-- LockAdmins locks the enabled administrators and returns their ids. A
-- concurrent change that disables or demotes one of them waits, and the rows
-- are checked again afterwards, so two such changes cannot both remove the
-- last one.
-- name: LockAdmins :many
SELECT id FROM users WHERE role = 'admin' AND disabled_at IS NULL FOR UPDATE;

-- SetUserDisabled disables (keeping the first time) or enables a user.
-- name: SetUserDisabled :one
UPDATE users
SET disabled_at = CASE WHEN @disabled::boolean THEN coalesce(disabled_at, now()) ELSE NULL END
WHERE id = @id
RETURNING *;

-- DeleteUser removes a user. Sessions and recovery codes go with it; audit
-- events keep the actor name they were recorded with.
-- name: DeleteUser :one
DELETE FROM users WHERE id = $1 RETURNING *;

-- name: SetUserRole :one
UPDATE users SET role = @role WHERE id = @id RETURNING *;

-- name: SetPassword :exec
UPDATE users SET password_hash = @password_hash, must_change_password = @must_change
WHERE id = @id AND source = 'local';

-- name: SetPendingTOTP :exec
UPDATE users SET totp_secret_enc = @secret_enc, totp_enabled_at = NULL, totp_last_step = 0
WHERE id = @id;

-- name: EnableTOTP :exec
UPDATE users SET totp_enabled_at = now(), totp_last_step = @last_step
WHERE id = @id AND totp_secret_enc IS NOT NULL;

-- UseTOTPStep records an accepted TOTP time step. It updates nothing (and
-- returns no row) if the step is not newer than the last accepted one.
-- name: UseTOTPStep :one
UPDATE users SET totp_last_step = @step
WHERE id = @id AND totp_last_step < @step
RETURNING id;

-- name: ResetTOTP :exec
UPDATE users SET totp_secret_enc = NULL, totp_enabled_at = NULL, totp_last_step = 0,
    mfa_failed_attempts = 0
WHERE id = @id;

-- RecordMFAFailure counts one more wrong second-factor code in a single
-- statement and returns the new count.
-- name: RecordMFAFailure :one
UPDATE users SET mfa_failed_attempts = mfa_failed_attempts + 1
WHERE id = @id
RETURNING mfa_failed_attempts;

-- name: ResetMFAFailures :exec
UPDATE users SET mfa_failed_attempts = 0 WHERE id = @id;
