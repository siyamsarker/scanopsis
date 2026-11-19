-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- Set by a password reset from the command line; the user must choose a new
-- password before the session is fully usable.
ALTER TABLE users ADD COLUMN must_change_password boolean NOT NULL DEFAULT false;
-- TOTP secret encrypted with AES-256-GCM (nonce || ciphertext). While
-- totp_enabled_at is NULL the secret is a pending enrolment.
ALTER TABLE users ADD COLUMN totp_secret_enc bytea;
ALTER TABLE users ADD COLUMN totp_enabled_at timestamptz;
-- Last accepted TOTP time step, so a code cannot be used twice.
ALTER TABLE users ADD COLUMN totp_last_step bigint NOT NULL DEFAULT 0;
ALTER TABLE users ADD CONSTRAINT users_totp_enabled_has_secret
    CHECK (totp_enabled_at IS NULL OR totp_secret_enc IS NOT NULL);

CREATE TABLE recovery_codes (
    user_id   bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- SHA-256 of the normalised code; codes are random and high-entropy.
    code_hash bytea       NOT NULL CHECK (length(code_hash) = 32),
    used_at   timestamptz,
    PRIMARY KEY (user_id, code_hash)
);

-- A session starts after the first factor; it becomes fully usable once
-- the second factor has been verified.
ALTER TABLE sessions ADD COLUMN mfa_verified boolean NOT NULL DEFAULT false;

CREATE TABLE settings (
    key        text        PRIMARY KEY,
    -- Non-secret values as JSON; NULL for secrets.
    value      jsonb,
    -- Secret values encrypted with AES-256-GCM; NULL for non-secrets.
    secret_enc bytea,
    source     text        NOT NULL CHECK (source IN ('env', 'ui')),
    updated_by text        NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((value IS NULL) <> (secret_enc IS NULL))
);

-- +goose Down
DROP TABLE settings;
ALTER TABLE sessions DROP COLUMN mfa_verified;
DROP TABLE recovery_codes;
ALTER TABLE users DROP CONSTRAINT users_totp_enabled_has_secret;
ALTER TABLE users DROP COLUMN totp_last_step;
ALTER TABLE users DROP COLUMN totp_enabled_at;
ALTER TABLE users DROP COLUMN totp_secret_enc;
ALTER TABLE users DROP COLUMN must_change_password;
