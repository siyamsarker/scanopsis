-- Copyright 2026 Siyam Sarker
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- The ref (branch) an administrator chose for the dashboards of a service.
-- NULL means none was chosen: the dashboards then show the ref with the most
-- recent scan. The ref need not have scans yet.
ALTER TABLE services ADD COLUMN default_ref text;

-- +goose Down
ALTER TABLE services DROP COLUMN default_ref;
