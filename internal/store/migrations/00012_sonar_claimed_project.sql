-- Copyright 2026 Siyam Sarker
-- SPDX-License-Identifier: Apache-2.0

-- The GitLab project and pipeline a SonarQube delivery names in its
-- sonar.analysis.gitlabProjectId and sonar.analysis.pipelineId scanner
-- properties. A pipeline that runs its analysis before its first Trivy scan
-- delivers the analysis before the service of the project exists, so the
-- delivery stays unlinked. When the first upload of that same pipeline
-- creates the service (or gives it its GitLab project ID), the delivery is
-- linked to it. Any SonarQube user can set the properties, so the pipeline
-- must match the verified upload. The columns are added without a default,
-- so no rows are rewritten; older deliveries keep NULL.

-- +goose Up
ALTER TABLE sonar_deliveries ADD COLUMN claimed_project_id bigint;
ALTER TABLE sonar_deliveries ADD COLUMN claimed_pipeline_id bigint;

-- +goose Down
ALTER TABLE sonar_deliveries DROP COLUMN claimed_pipeline_id;
ALTER TABLE sonar_deliveries DROP COLUMN claimed_project_id;
