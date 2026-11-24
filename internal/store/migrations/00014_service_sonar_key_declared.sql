-- Copyright 2026 Siyam Sarker
-- SPDX-License-Identifier: Apache-2.0

-- The SonarQube project key a service's own pipeline declared in an upload
-- (the sonar_key upload parameter), and when. The CI token proves which
-- GitLab project uploads, not that the project owns the key, so the key is
-- set only once SonarQube delivered an analysis of it that names the same
-- GitLab project; until then the declaration waits here, and no analysis of
-- another key is linked to the service by the scanner properties. The
-- columns are added without a default, so no rows are rewritten.

-- +goose Up
ALTER TABLE services ADD COLUMN sonar_key_declared text;
ALTER TABLE services ADD COLUMN sonar_key_declared_at timestamptz;

-- +goose Down
ALTER TABLE services DROP COLUMN sonar_key_declared_at;
ALTER TABLE services DROP COLUMN sonar_key_declared;
