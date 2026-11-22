-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- Indexes for the portfolio, the first-seen lookup of the findings pages and
-- the vulnerability search. They are built on tables that can hold millions
-- of rows, so every statement runs outside a transaction with CONCURRENTLY:
-- ingestion and reads continue while an index builds, and each statement is
-- safe to run again. A build that is interrupted (a restart, a cancelled
-- statement) leaves an INVALID index behind; the DO blocks below remove such
-- leftovers so that the next start builds them again from scratch.

-- +goose NO TRANSACTION

-- +goose Up

-- First seen: the same (vuln_id, pkg_name, target) is looked up inside every
-- earlier scan of an artifact. With (scan_id, id) the lookup read every
-- finding of every earlier scan; with the finding key in the index it reads
-- one entry per match, from the index alone. The index also serves every
-- lookup by scan_id (findings pages, diffs and cascading deletes of scans),
-- so the old (scan_id, id) index is dropped.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
               WHERE c.relname = 'vulnerabilities_scan_key_idx' AND NOT i.indisvalid) THEN
        DROP INDEX vulnerabilities_scan_key_idx;
    END IF;
END $$;
-- +goose StatementEnd
CREATE INDEX CONCURRENTLY IF NOT EXISTS vulnerabilities_scan_key_idx
    ON vulnerabilities (scan_id, vuln_id, pkg_name, target);
DROP INDEX CONCURRENTLY IF EXISTS vulnerabilities_scan_idx;

-- Portfolio, current scans and the vulnerability search: the columns after
-- INCLUDE are the counts the portfolio sums, so it is answered from the index
-- without reading the table. It has the same key as scans_current_idx, which
-- therefore becomes redundant (the current-scan, retention and search
-- queries that used it use this one).
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
               WHERE c.relname = 'scans_current_cover_idx' AND NOT i.indisvalid) THEN
        DROP INDEX scans_current_cover_idx;
    END IF;
END $$;
-- +goose StatementEnd
CREATE INDEX CONCURRENTLY IF NOT EXISTS scans_current_cover_idx
    ON scans (service_id, ref, artifact_name, kind, scanned_at DESC, id DESC)
    INCLUDE (count_critical, count_high, count_medium, count_low, count_unknown, count_fixable, count_secrets);
DROP INDEX CONCURRENTLY IF EXISTS scans_current_idx;

-- The ref with the most recent scan of a service (the portfolio and the
-- default ref of the findings pages). Without this index the planner walks
-- scans_scanned_at_idx from the newest scan of all services until it meets
-- one of the service, over a hundred thousand buffers per portfolio
-- request; the covering index above does not help because scanned_at is
-- behind ref in its key.
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
               WHERE c.relname = 'scans_latest_ref_idx' AND NOT i.indisvalid) THEN
        DROP INDEX scans_latest_ref_idx;
    END IF;
END $$;
-- +goose StatementEnd
CREATE INDEX CONCURRENTLY IF NOT EXISTS scans_latest_ref_idx ON scans (service_id, scanned_at DESC, ref);

-- +goose Down
-- Recreates the indexes of the earlier schema, then drops the new ones.
CREATE INDEX CONCURRENTLY IF NOT EXISTS scans_current_idx
    ON scans (service_id, ref, artifact_name, kind, scanned_at DESC, id DESC);
DROP INDEX CONCURRENTLY IF EXISTS scans_current_cover_idx;
DROP INDEX CONCURRENTLY IF EXISTS scans_latest_ref_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS vulnerabilities_scan_idx ON vulnerabilities (scan_id, id);
DROP INDEX CONCURRENTLY IF EXISTS vulnerabilities_scan_key_idx;
