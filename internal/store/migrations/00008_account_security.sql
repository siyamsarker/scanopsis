-- Copyright 2026 The Scanopsis Authors
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

-- +goose Down
ALTER TABLE sessions DROP COLUMN user_agent;
ALTER TABLE sessions DROP COLUMN client_ip;
ALTER TABLE sessions DROP COLUMN id;
