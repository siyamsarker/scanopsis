-- Copyright 2026 Siyam Sarker
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- Consecutive wrong second-factor codes. At the limit the account refuses
-- every code until an administrator resets two-factor authentication or the
-- password; a correct code sets it back to zero.
ALTER TABLE users ADD COLUMN mfa_failed_attempts integer NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE users DROP COLUMN mfa_failed_attempts;
