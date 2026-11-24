-- Copyright 2026 Siyam Sarker
-- SPDX-License-Identifier: Apache-2.0

-- The analysedAt time of a SonarQube delivery. A delivery that waited for the
-- first scan of its pipeline is linked later, and its analysis is then read
-- back from the SonarQube Web API, which does not return the scanner's
-- sonar.analysis.pipelineId property. The pipeline the delivery named is
-- given to that analysis only when its date is exactly the delivered
-- analysedAt, so a newer analysis never gets it. The column is added without
-- a default, so no rows are rewritten; older deliveries keep NULL.

-- +goose Up
ALTER TABLE sonar_deliveries ADD COLUMN analysed_at timestamptz;

-- +goose Down
ALTER TABLE sonar_deliveries DROP COLUMN analysed_at;
