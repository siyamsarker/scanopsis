-- Copyright 2026 Siyam Sarker
-- SPDX-License-Identifier: Apache-2.0

-- Data retention. Every statement deletes or changes at most @batch_size
-- rows, so no statement holds locks for long. "Latest" matches the
-- current-state queries: the newest (scanned_at, id) of a (service, ref,
-- artifact_key, kind), so the scans of earlier tags of an image expire.
--
-- A scan is "kept" (never expires, and keeps its raw report) when it is the
-- latest of its (service, ref, artifact, kind) and one of these holds:
--   * its ref is the service's chosen dashboard branch (services.default_ref);
--   * the scan's ref was protected (scans.ref_protected);
--   * the service has no chosen dashboard branch and the scan's ref is the
--     ref the dashboards then show: the ref of the service's most recent
--     scan, ties broken by ref (see ListRefs and findings.pickRef).
-- Every other scan expires, including the latest scan of an unprotected ref
-- that is not the dashboard branch. The two statements below repeat the same
-- condition; keep them in step with each other and with pickRef.

-- DeleteExpiredScans deletes the oldest scans that are older than @cutoff
-- and are not kept; their findings go with them (ON DELETE CASCADE). Scans at
-- or before the (@after_at, @after_id) position are not looked at again, so
-- the kept scans that are skipped are not re-examined by every batch. It
-- returns the position of each deleted scan.
-- name: DeleteExpiredScans :many
WITH doomed AS (
    SELECT s.id, s.scanned_at
    FROM scans s
    WHERE (s.scanned_at, s.id) > (@after_at::timestamptz, @after_id::bigint)
      AND s.scanned_at < @cutoff::timestamptz
      AND (
          EXISTS (
              SELECT 1 FROM scans n
              WHERE n.service_id = s.service_id AND n.ref = s.ref
                AND n.artifact_key = s.artifact_key AND n.kind = s.kind
                AND (n.scanned_at, n.id) > (s.scanned_at, s.id)
          )
          OR NOT (
              s.ref_protected
              OR EXISTS (
                  SELECT 1 FROM services sv
                  WHERE sv.id = s.service_id
                    AND (sv.default_ref = s.ref
                         OR (sv.default_ref IS NULL AND s.ref = (
                             SELECT l.ref FROM scans l WHERE l.service_id = sv.id
                             ORDER BY l.scanned_at DESC, l.ref LIMIT 1)))
              )
          )
      )
    ORDER BY s.scanned_at, s.id
    LIMIT @batch_size::int
)
DELETE FROM scans
WHERE id IN (SELECT id FROM doomed)
RETURNING id, scanned_at;

-- ClearExpiredRawReports empties raw_report (the compressed original report)
-- of scans that are older than @cutoff and are not kept (see above). An empty
-- value means the report was cleared. Paging is as in DeleteExpiredScans.
-- name: ClearExpiredRawReports :many
WITH todo AS (
    SELECT s.id, s.scanned_at
    FROM scans s
    WHERE (s.scanned_at, s.id) > (@after_at::timestamptz, @after_id::bigint)
      AND s.scanned_at < @cutoff::timestamptz
      AND octet_length(s.raw_report) > 0
      AND (
          EXISTS (
              SELECT 1 FROM scans n
              WHERE n.service_id = s.service_id AND n.ref = s.ref
                AND n.artifact_key = s.artifact_key AND n.kind = s.kind
                AND (n.scanned_at, n.id) > (s.scanned_at, s.id)
          )
          OR NOT (
              s.ref_protected
              OR EXISTS (
                  SELECT 1 FROM services sv
                  WHERE sv.id = s.service_id
                    AND (sv.default_ref = s.ref
                         OR (sv.default_ref IS NULL AND s.ref = (
                             SELECT l.ref FROM scans l WHERE l.service_id = sv.id
                             ORDER BY l.scanned_at DESC, l.ref LIMIT 1)))
              )
          )
      )
    ORDER BY s.scanned_at, s.id
    LIMIT @batch_size::int
)
UPDATE scans
SET raw_report = ''::bytea
FROM todo
WHERE scans.id = todo.id
RETURNING scans.id, scans.scanned_at;

-- DeleteExpiredSonarAnalyses is the same for SonarQube analyses; the latest
-- analysis of each service is kept.
-- name: DeleteExpiredSonarAnalyses :many
WITH doomed AS (
    SELECT a.id, a.analysed_at
    FROM sonar_analyses a
    WHERE (a.analysed_at, a.id) > (@after_at::timestamptz, @after_id::bigint)
      AND a.analysed_at < @cutoff::timestamptz
      AND EXISTS (
          SELECT 1 FROM sonar_analyses n
          WHERE n.service_id = a.service_id
            AND (n.analysed_at, n.id) > (a.analysed_at, a.id)
      )
    ORDER BY a.analysed_at, a.id
    LIMIT @batch_size::int
)
DELETE FROM sonar_analyses
WHERE id IN (SELECT id FROM doomed)
RETURNING id, analysed_at;

-- name: DeleteExpiredSonarDeliveries :execrows
DELETE FROM sonar_deliveries
WHERE id IN (
    SELECT id FROM sonar_deliveries
    WHERE received_at < @cutoff::timestamptz
    ORDER BY received_at
    LIMIT @batch_size::int
);

-- name: DeleteExpiredAuditEvents :execrows
DELETE FROM audit_log
WHERE id IN (
    SELECT id FROM audit_log
    WHERE at < @cutoff::timestamptz
    ORDER BY at
    LIMIT @batch_size::int
);
