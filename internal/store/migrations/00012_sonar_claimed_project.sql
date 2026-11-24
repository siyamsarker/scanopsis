-- Copyright 2026 Siyam Sarker
-- SPDX-License-Identifier: Apache-2.0

-- The GitLab project a SonarQube delivery names in its
-- sonar.analysis.gitlabProjectId scanner property. A pipeline that runs its
-- analysis before its first Trivy scan delivers the analysis before the
-- service of the project exists, so the delivery stays unlinked. When the
-- service is created, or receives its GitLab project ID, the deliveries that
-- named that project are linked to it. The column is added without a default,
-- so no rows are rewritten; older deliveries keep NULL.

-- +goose Up
ALTER TABLE sonar_deliveries ADD COLUMN claimed_project_id bigint;

-- +goose Down
ALTER TABLE sonar_deliveries DROP COLUMN claimed_project_id;
