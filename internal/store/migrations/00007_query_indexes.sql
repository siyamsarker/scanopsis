-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- Indexes for the portfolio, the first-seen lookup of the findings pages and
-- the vulnerability search. They are built on tables that can hold millions
-- of rows, so every statement runs outside a transaction with CONCURRENTLY:
-- ingestion and reads continue while an index builds, and each statement is
-- safe to run again. The server runs migrations with a lock_timeout, so DDL
-- that cannot get its lock fails and is retried at the next start instead of
-- queuing behind long transactions. A build that is interrupted (a restart, a cancelled
-- statement) leaves an INVALID index behind; the DO blocks below remove such
-- leftovers so that the next start builds them again from scratch.

-- +goose NO TRANSACTION

-- +goose Up

-- First seen: the same (vuln_id, pkg_name, target) is looked up inside every
-- earlier scan of an artifact. With (scan_id, id) the lookup read every
-- finding of every earlier scan; with the finding key in the index it reads
-- one entry per match. The key is the md5 of the three fields, not the fields
-- themselves: each can be up to 4096 bytes, and a B-tree entry over about
-- 2700 bytes is rejected, which would refuse a report with long paths. The
-- query compares the real columns too, so a hash collision cannot match. The
-- index also serves every
-- lookup by scan_id (findings pages, diffs and cascading deletes of scans),
-- so the old (scan_id, id) index is dropped.
-- +goose StatementBegin
DO $$
DECLARE leftover regclass;
BEGIN
    FOR leftover IN
        SELECT i.indexrelid::regclass FROM pg_index i
        JOIN pg_class c ON c.oid = i.indexrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = current_schema() AND c.relname = 'vulnerabilities_scan_key_idx'
          AND i.indrelid = 'vulnerabilities'::regclass AND NOT i.indisvalid
    LOOP
        EXECUTE format('DROP INDEX %s', leftover);
    END LOOP;
END $$;
-- +goose StatementEnd
CREATE INDEX CONCURRENTLY IF NOT EXISTS vulnerabilities_scan_key_idx
    ON vulnerabilities (scan_id, md5(vuln_id || chr(1) || pkg_name || chr(1) || target));
DROP INDEX CONCURRENTLY IF EXISTS vulnerabilities_scan_idx;

-- Vulnerability search: finds the findings of a vulnerability ID inside one
-- scan, case-insensitively. The key is lower(left(vuln_id, 100)) because IDs
-- are searched up to 100 characters, and a stored ID can be 4096 bytes, too
-- long for a B-tree entry. The query compares lower(vuln_id) as well. This
-- index replaces the one on lower(vuln_id) alone.
-- +goose StatementBegin
DO $$
DECLARE leftover regclass;
BEGIN
    FOR leftover IN
        SELECT i.indexrelid::regclass FROM pg_index i
        JOIN pg_class c ON c.oid = i.indexrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = current_schema() AND c.relname = 'vulnerabilities_vuln_id_scan_idx'
          AND i.indrelid = 'vulnerabilities'::regclass AND NOT i.indisvalid
    LOOP
        EXECUTE format('DROP INDEX %s', leftover);
    END LOOP;
END $$;
-- +goose StatementEnd
CREATE INDEX CONCURRENTLY IF NOT EXISTS vulnerabilities_vuln_id_scan_idx
    ON vulnerabilities (lower(left(vuln_id, 100)), scan_id);
DROP INDEX CONCURRENTLY IF EXISTS vulnerabilities_vuln_id_idx;

-- Portfolio, current scans and the vulnerability search: the columns after
-- INCLUDE are the counts the portfolio sums, so it is answered from the index
-- without reading the table. It has the same key as scans_current_idx, which
-- therefore becomes redundant (the current-scan, retention and search
-- queries that used it use this one).
-- +goose StatementBegin
DO $$
DECLARE leftover regclass;
BEGIN
    FOR leftover IN
        SELECT i.indexrelid::regclass FROM pg_index i
        JOIN pg_class c ON c.oid = i.indexrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = current_schema() AND c.relname = 'scans_current_cover_idx'
          AND i.indrelid = 'scans'::regclass AND NOT i.indisvalid
    LOOP
        EXECUTE format('DROP INDEX %s', leftover);
    END LOOP;
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
DO $$
DECLARE leftover regclass;
BEGIN
    FOR leftover IN
        SELECT i.indexrelid::regclass FROM pg_index i
        JOIN pg_class c ON c.oid = i.indexrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = current_schema() AND c.relname = 'scans_latest_ref_idx'
          AND i.indrelid = 'scans'::regclass AND NOT i.indisvalid
    LOOP
        EXECUTE format('DROP INDEX %s', leftover);
    END LOOP;
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
CREATE INDEX CONCURRENTLY IF NOT EXISTS vulnerabilities_vuln_id_idx ON vulnerabilities (lower(vuln_id));
DROP INDEX CONCURRENTLY IF EXISTS vulnerabilities_vuln_id_scan_idx;
DROP INDEX CONCURRENTLY IF EXISTS vulnerabilities_scan_key_idx;
