-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- name: SonarServiceByGitlabProject :one
SELECT id, sonar_project_key FROM services WHERE gitlab_project_id = $1 AND NOT archived;

-- name: SonarServiceByKey :one
SELECT id FROM services WHERE sonar_project_key = $1 AND NOT archived;

-- PinSonarKey links an unlinked service to a SonarQube project; zero rows
-- mean (pgx.ErrNoRows) the service was linked in the meantime. It returns the project path.
-- name: PinSonarKey :one
UPDATE services SET sonar_project_key = $2
WHERE id = $1 AND sonar_project_key IS NULL AND NOT archived
RETURNING gitlab_project_path;

-- name: SonarServiceKey :one
SELECT sonar_project_key FROM services WHERE id = $1;

-- name: SonarLinkedServices :many
SELECT id, sonar_project_key::text AS sonar_project_key
FROM services
WHERE sonar_project_key IS NOT NULL AND NOT archived
ORDER BY id;

-- InsertSonarDelivery records a delivery as pending; zero rows mean the task
-- ID was delivered before.
-- InsertSonarDelivery records a delivery as pending. A redelivery of a task
-- whose earlier processing failed is accepted again (an administrator can
-- redeliver it from SonarQube), but only for the same project, at least a
-- minute after the previous attempt and at most 5 attempts in all, so a
-- captured delivery cannot be replayed without bound. Any other duplicate
-- changes nothing and affects no row.
-- name: InsertSonarDelivery :execrows
INSERT INTO sonar_deliveries (task_id, project_key, received_at, outcome, detail)
VALUES ($1, $2, $3, 'pending', '')
ON CONFLICT (task_id) DO UPDATE
SET outcome = 'pending', detail = '', service_id = NULL,
    received_at = EXCLUDED.received_at, attempts = sonar_deliveries.attempts + 1
WHERE sonar_deliveries.outcome = 'failed'
  AND sonar_deliveries.project_key = EXCLUDED.project_key
  AND sonar_deliveries.attempts < 5
  AND sonar_deliveries.received_at < EXCLUDED.received_at - interval '1 minute';

-- name: FinishSonarDelivery :exec
UPDATE sonar_deliveries
SET outcome = $2, service_id = $3, detail = $4
WHERE task_id = $1;

-- InterruptSonarDeliveries fails deliveries that stayed pending since before
-- the cutoff, because the server stopped while processing them. A delivery
-- that waits for the sync because the processing queue was full (its detail
-- is @queued_detail) is given until @queued_cutoff instead, so a sync can
-- still read its analysis first.
-- name: InterruptSonarDeliveries :execrows
UPDATE sonar_deliveries
SET outcome = 'failed', detail = @detail::text
WHERE outcome = 'pending'
  AND ((detail = @queued_detail::text AND received_at < @queued_cutoff::timestamptz)
    OR (detail <> @queued_detail::text AND received_at < @cutoff::timestamptz));

-- ResolveQueuedSonarDeliveries marks the deliveries that waited for the sync
-- because the processing queue was full as linked, for projects whose newest
-- analysis the sync has just read and stored.
-- name: ResolveQueuedSonarDeliveries :execrows
UPDATE sonar_deliveries d
SET outcome = 'linked', detail = @detail::text,
    service_id = (SELECT sv.id FROM services sv WHERE sv.sonar_project_key = d.project_key)
WHERE d.outcome = 'pending' AND d.detail = @queued_detail::text
  AND d.project_key = ANY(@project_keys::text[]);

-- name: InsertSonarAnalysis :exec
INSERT INTO sonar_analyses (
    service_id, task_id, analysis_key, analysed_at, revision, quality_gate_status,
    gate_conditions, measures, mode, reliability_rating, security_rating,
    maintainability_rating, security_review_rating, coverage,
    duplicated_lines_density, gitlab_pipeline_id, received_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
)
ON CONFLICT (task_id) DO NOTHING;

-- name: SonarAnalysisKnown :one
SELECT EXISTS (
    SELECT 1 FROM sonar_analyses WHERE service_id = $1 AND analysis_key = $2
) AS known;

-- name: ListSonarAnalyses :many
SELECT * FROM sonar_analyses
WHERE service_id = $1
ORDER BY analysed_at DESC, id DESC
LIMIT $2;

-- name: ListSonarDeliveries :many
SELECT task_id, project_key, received_at, outcome, service_id, detail
FROM sonar_deliveries
ORDER BY received_at DESC, id DESC
LIMIT 50;

-- name: LastSonarDeliveryAt :one
SELECT max(received_at)::timestamptz AS last_at FROM sonar_deliveries;
