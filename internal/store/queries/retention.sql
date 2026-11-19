-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- Data retention. Every statement deletes at most @batch_size rows, so no
-- statement holds locks for long. "Latest" matches the current-state
-- queries: the newest (scanned_at, id) of a (service, ref, artifact, kind).

-- DeleteExpiredScans deletes the oldest scans that are older than @cutoff
-- and have a newer scan with the same service, ref, artifact and kind; their
-- findings go with them (ON DELETE CASCADE). Scans at or before the
-- (@after_at, @after_id) position are not looked at again, so the latest
-- scans that are skipped are not re-examined by every batch. It returns the
-- position of each deleted scan.
-- name: DeleteExpiredScans :many
WITH doomed AS (
    SELECT s.id, s.scanned_at
    FROM scans s
    WHERE (s.scanned_at, s.id) > (@after_at::timestamptz, @after_id::bigint)
      AND s.scanned_at < @cutoff::timestamptz
      AND EXISTS (
          SELECT 1 FROM scans n
          WHERE n.service_id = s.service_id AND n.ref = s.ref
            AND n.artifact_name = s.artifact_name AND n.kind = s.kind
            AND (n.scanned_at, n.id) > (s.scanned_at, s.id)
      )
    ORDER BY s.scanned_at, s.id
    LIMIT @batch_size::int
)
DELETE FROM scans
WHERE id IN (SELECT id FROM doomed)
RETURNING id, scanned_at;

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
