-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
CREATE TABLE users (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source         text        NOT NULL CHECK (source IN ('gitlab', 'local')),
    gitlab_user_id bigint      UNIQUE,
    username       text        NOT NULL CHECK (username <> ''),
    name           text        NOT NULL DEFAULT '',
    email          text        NOT NULL DEFAULT '',
    role           text        NOT NULL CHECK (role IN ('admin', 'viewer')),
    -- Argon2id hash in PHC string format; local users only.
    password_hash  text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    last_login_at  timestamptz,
    CHECK ((source = 'gitlab') = (gitlab_user_id IS NOT NULL)),
    CHECK ((source = 'local') = (password_hash IS NOT NULL))
);

CREATE UNIQUE INDEX users_local_username_key ON users (username) WHERE source = 'local';

CREATE TABLE sessions (
    -- SHA-256 of the session token; the token itself is never stored.
    id_hash      bytea       PRIMARY KEY CHECK (length(id_hash) = 32),
    user_id      bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE audit_log (
    id     bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at     timestamptz NOT NULL DEFAULT now(),
    actor  text        NOT NULL,
    action text        NOT NULL,
    detail jsonb       NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX audit_log_at_idx ON audit_log (at);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE sessions;
DROP TABLE users;
