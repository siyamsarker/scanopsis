-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- The ref of the most recent scan of a service is the ref a dashboard shows
-- when no dashboard branch is chosen. Retention needs it for every service
-- while it decides which scans to keep, and the portfolio needs it for every
-- service on every load. Without this index the lookup walks the scans of
-- other services in time order until it meets one of this service.
CREATE INDEX scans_service_recent_idx ON scans (service_id, scanned_at DESC, ref);

-- +goose Down
DROP INDEX scans_service_recent_idx;
