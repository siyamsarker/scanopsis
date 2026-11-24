-- Copyright 2026 Siyam Sarker
-- SPDX-License-Identifier: Apache-2.0

-- The artifact key says which scans are of the same artifact. Until now that
-- was artifact_name, the name Trivy reports, which for an image includes its
-- tag: a pipeline that tags each run's image differently (app:2, app:3) made
-- every run a separate image, and the totals of a service added up over
-- pipelines. artifact_name stays as reported, for display and for replacing
-- the scan of a repeated job; artifact_key is what the current state, the
-- diffs, the trends, the search and data retention group by.
--
-- The key of an existing scan is derived from its name:
--   * image: the name without its digest (from "@") and without its tag (a
--     ":" in the last "/"-separated part; a registry port such as
--     registry:5000/team/app is not a tag). Letter case is kept.
--   * secret-fs: a scan of the repository is named after the folder Trivy
--     was given. "." stays "."; an absolute path that ends in
--     /<project path> (the runner's build folder) becomes "."; an absolute
--     path that contains /<project path>/ becomes the part after it
--     (services/api, in the letter case of the name), "." if nothing
--     follows. The project path is the service's GitLab project path,
--     matched in any letter case, as GitLab treats paths. Any other name is
--     its own key.
-- A name for which these rules leave nothing is its own key.
--
-- scans can hold hundreds of thousands of rows, so the migration runs
-- outside a transaction, like 00007: the column is added without a default
-- (no table rewrite), the keys are filled in batches by id, each batch
-- committed on its own, and NOT NULL is set through a validated CHECK
-- constraint, so the only scan of the whole table happens under a lock that
-- does not block reads or writes. The indexes that grouped scans by
-- artifact_name are then built again on artifact_key with CONCURRENTLY and
-- the old ones dropped. Every statement is safe to run again: a start that
-- stops part-way (a restart, a lock timeout) continues with the scans that
-- have no key yet, and a build that was interrupted leaves an INVALID index,
-- which the DO blocks remove so that it is built again from scratch.

-- +goose NO TRANSACTION

-- +goose Up
ALTER TABLE scans ADD COLUMN IF NOT EXISTS artifact_key text;

-- Batches of 5000 scans by id. A batch is committed only when this block
-- runs as a statement of its own, as goose runs it; a restore replays the
-- migration inside its own transaction, which already holds a transaction
-- id (from adding the column), and there every batch stays in that
-- transaction. The upper bound is read again after each batch, so scans
-- stored meanwhile are filled in too.
-- +goose StatementBegin
DO $$
DECLARE
    own_transaction constant boolean := pg_current_xact_id_if_assigned() IS NULL;
    batch constant bigint := 5000;
    done bigint;
BEGIN
    SELECT min(id) - 1 INTO done FROM scans;
    WHILE done < (SELECT max(id) FROM scans) LOOP
        UPDATE scans s
        SET artifact_key = coalesce(nullif(CASE
                WHEN s.kind = 'image' THEN
                    regexp_replace(regexp_replace(s.artifact_name, '@.*$', ''), ':[^/]*$', '')
                WHEN left(s.artifact_name, 1) <> '/' THEN
                    s.artifact_name
                WHEN lower(right(s.artifact_name, length(sv.gitlab_project_path) + 1)) = '/' || lower(sv.gitlab_project_path) THEN
                    '.'
                WHEN strpos(lower(s.artifact_name), '/' || lower(sv.gitlab_project_path) || '/') > 0 THEN
                    coalesce(nullif(rtrim(substr(s.artifact_name,
                        strpos(lower(s.artifact_name), '/' || lower(sv.gitlab_project_path) || '/') + length(sv.gitlab_project_path) + 2), '/'), ''), '.')
                ELSE s.artifact_name
            END, ''), s.artifact_name)
        FROM services sv
        WHERE sv.id = s.service_id
          AND s.id > done AND s.id <= done + batch
          AND s.artifact_key IS NULL;
        done := done + batch;
        IF own_transaction THEN
            COMMIT;
        END IF;
    END LOOP;
END $$;
-- +goose StatementEnd

-- SET NOT NULL skips its scan of the table when a valid CHECK constraint
-- already proves the column has no NULL. VALIDATE scans under a lock that
-- lets reads and writes continue; adding the unvalidated constraint, setting
-- NOT NULL and dropping the constraint each take a brief exclusive lock.
ALTER TABLE scans DROP CONSTRAINT IF EXISTS scans_artifact_key_filled;
ALTER TABLE scans ADD CONSTRAINT scans_artifact_key_filled CHECK (artifact_key IS NOT NULL) NOT VALID;
ALTER TABLE scans VALIDATE CONSTRAINT scans_artifact_key_filled;
ALTER TABLE scans ALTER COLUMN artifact_key SET NOT NULL;
ALTER TABLE scans DROP CONSTRAINT scans_artifact_key_filled;

-- The current state, retention and the search: scans_current_cover_idx of
-- 00007 with artifact_key in place of artifact_name.
-- +goose StatementBegin
DO $$
DECLARE leftover regclass;
BEGIN
    FOR leftover IN
        SELECT i.indexrelid::regclass FROM pg_index i
        JOIN pg_class c ON c.oid = i.indexrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = current_schema() AND c.relname = 'scans_current_key_idx'
          AND i.indrelid = 'scans'::regclass AND NOT i.indisvalid
    LOOP
        EXECUTE format('DROP INDEX %s', leftover);
    END LOOP;
END $$;
-- +goose StatementEnd
CREATE INDEX CONCURRENTLY IF NOT EXISTS scans_current_key_idx
    ON scans (service_id, ref, artifact_key, kind, scanned_at DESC, id DESC)
    INCLUDE (count_critical, count_high, count_medium, count_low, count_unknown, count_fixable, count_secrets);
DROP INDEX CONCURRENTLY IF EXISTS scans_current_cover_idx;

-- The protected refs of the search: scans_protected_idx of 00010 with
-- artifact_key in place of artifact_name.
-- +goose StatementBegin
DO $$
DECLARE leftover regclass;
BEGIN
    FOR leftover IN
        SELECT i.indexrelid::regclass FROM pg_index i
        JOIN pg_class c ON c.oid = i.indexrelid
        JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = current_schema() AND c.relname = 'scans_protected_key_idx'
          AND i.indrelid = 'scans'::regclass AND NOT i.indisvalid
    LOOP
        EXECUTE format('DROP INDEX %s', leftover);
    END LOOP;
END $$;
-- +goose StatementEnd
CREATE INDEX CONCURRENTLY IF NOT EXISTS scans_protected_key_idx
    ON scans (service_id, ref, artifact_key, kind) WHERE ref_protected;
DROP INDEX CONCURRENTLY IF EXISTS scans_protected_idx;

-- +goose Down
-- Recreates the indexes of the earlier schema; dropping the column drops
-- the indexes on it.
CREATE INDEX CONCURRENTLY IF NOT EXISTS scans_current_cover_idx
    ON scans (service_id, ref, artifact_name, kind, scanned_at DESC, id DESC)
    INCLUDE (count_critical, count_high, count_medium, count_low, count_unknown, count_fixable, count_secrets);
CREATE INDEX CONCURRENTLY IF NOT EXISTS scans_protected_idx
    ON scans (service_id, ref, artifact_name, kind) WHERE ref_protected;
ALTER TABLE scans DROP COLUMN IF EXISTS artifact_key;
