-- Copyright 2026 Siyam Sarker
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- A stable number for each session, so users can list and end their own
-- sessions without the token hash leaving the server. It stays the same when
-- the token is replaced after the second factor. Existing sessions are
-- numbered when the column is added.
ALTER TABLE sessions ADD COLUMN id bigint NOT NULL GENERATED ALWAYS AS IDENTITY;
ALTER TABLE sessions ADD CONSTRAINT sessions_id_key UNIQUE (id);
-- Where and with what the session was started, as recorded at sign-in: the
-- client address and the first 200 bytes of the User-Agent header. Empty for
-- sessions that started before this migration.
ALTER TABLE sessions ADD COLUMN client_ip text NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN user_agent text NOT NULL DEFAULT '';

-- How each recovery code is stored. 1: SHA-256 of a 10-character code, as
-- issued before this migration; such codes keep working until they are used
-- or replaced. 2: HMAC-SHA-256 of a 20-character code and the user id, with a
-- key derived from SCANOPSIS_SECRET_KEY. New codes are always version 2, so
-- the column has no default once existing rows are marked.
ALTER TABLE recovery_codes ADD COLUMN version smallint NOT NULL DEFAULT 1
    CONSTRAINT recovery_codes_version_check CHECK (version IN (1, 2));
ALTER TABLE recovery_codes ALTER COLUMN version DROP DEFAULT;

-- Set while an administrator has disabled the account: every sign-in is
-- refused and the account has no sessions. NULL for an enabled account.
ALTER TABLE users ADD COLUMN disabled_at timestamptz;

-- The GitLab instance a GitLab user signed in from: its base URL, which is
-- also the OpenID Connect issuer. GitLab user ids are unique only within one
-- instance, so a user is identified by the pair, and a sign-in from another
-- instance never matches an existing row. Existing GitLab users signed in
-- from the GitLab URL stored in the settings now; without one they get an
-- empty issuer, which no sign-in matches, and their next sign-in creates a
-- new row.
ALTER TABLE users ADD COLUMN gitlab_issuer text;
UPDATE users
SET gitlab_issuer = coalesce((SELECT value #>> '{}' FROM settings WHERE key = 'gitlab.url'), '')
WHERE source = 'gitlab';
ALTER TABLE users ADD CONSTRAINT users_gitlab_issuer_check
    CHECK ((source = 'gitlab') = (gitlab_issuer IS NOT NULL));
ALTER TABLE users DROP CONSTRAINT users_gitlab_user_id_key;
ALTER TABLE users ADD CONSTRAINT users_gitlab_identity_key UNIQUE (gitlab_issuer, gitlab_user_id);

-- +goose Down
-- Fails if two GitLab instances have a user with the same id; delete one of
-- the rows first.
ALTER TABLE users DROP CONSTRAINT users_gitlab_identity_key;
ALTER TABLE users ADD CONSTRAINT users_gitlab_user_id_key UNIQUE (gitlab_user_id);
ALTER TABLE users DROP CONSTRAINT users_gitlab_issuer_check;
ALTER TABLE users DROP COLUMN gitlab_issuer;
ALTER TABLE users DROP COLUMN disabled_at;
-- Version 2 codes cannot be checked without this column: remove them, so the
-- users concerned fall back to their authenticator app.
DELETE FROM recovery_codes WHERE version <> 1;
ALTER TABLE recovery_codes DROP COLUMN version;
ALTER TABLE sessions DROP COLUMN user_agent;
ALTER TABLE sessions DROP COLUMN client_ip;
ALTER TABLE sessions DROP COLUMN id;
