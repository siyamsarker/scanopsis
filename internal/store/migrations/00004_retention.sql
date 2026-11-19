-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- The retention job finds the oldest expired rows in batches. Without these
-- indexes each batch would scan the whole table: scans has an index on
-- received_at, not on scanned_at (the two differ for reports that are
-- uploaded late), and sonar_analyses is indexed per service only. The id is
-- the tie-breaker the job pages by. The other expiry columns it uses
-- (sonar_deliveries.received_at, audit_log.at) are already indexed.
CREATE INDEX scans_scanned_at_idx ON scans (scanned_at, id);
CREATE INDEX sonar_analyses_analysed_at_idx ON sonar_analyses (analysed_at, id);

-- +goose Down
DROP INDEX sonar_analyses_analysed_at_idx;
DROP INDEX scans_scanned_at_idx;
