-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- The vulnerability search also looks at every protected ref. Finding them
-- means jumping from one (service, ref, artifact, kind) group of protected
-- scans to the next, and scans_current_cover_idx cannot do that without
-- reading every group of every ref (about 100000 at 500 services after 90 days
-- of branches). This index holds only the scans of protected refs, a small
-- part of the table, so the jump visits the protected groups alone.
--
-- Like the indexes of 00007 it is built with CONCURRENTLY outside a
-- transaction, so ingestion and reads continue while it builds, and a build
-- that was interrupted leaves an INVALID index behind, which the DO block
-- removes so that the next start builds it again from scratch.

-- +goose NO TRANSACTION

-- +goose Up

-- +goose StatementBegin
DO $$
DECLARE leftover regclass;
BEGIN
    FOR leftover IN
        SELECT i.indexrelid::regclass FROM pg_index i
        JOIN pg_class c ON c.oid = i.indexrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = current_schema() AND c.relname = 'scans_protected_idx'
          AND i.indrelid = 'scans'::regclass AND NOT i.indisvalid
    LOOP
        EXECUTE format('DROP INDEX %s', leftover);
    END LOOP;
END $$;
-- +goose StatementEnd
CREATE INDEX CONCURRENTLY IF NOT EXISTS scans_protected_idx
    ON scans (service_id, ref, artifact_name, kind) WHERE ref_protected;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS scans_protected_idx;
