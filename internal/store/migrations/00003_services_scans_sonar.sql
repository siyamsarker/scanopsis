-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- +goose Up
-- One service per GitLab project. Created by the first verified upload
-- (created_via 'ingest', with the project ID from the CI token) or by an
-- administrator by project path (created_via 'manual'; the project ID is
-- filled in by the first upload whose token names that path).
CREATE TABLE services (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name                text        NOT NULL CHECK (name <> ''),
    gitlab_project_id   bigint      UNIQUE,
    gitlab_project_path text        NOT NULL UNIQUE CHECK (gitlab_project_path <> ''),
    sonar_project_key   text        UNIQUE,
    created_via         text        NOT NULL CHECK (created_via IN ('ingest', 'manual')),
    archived            boolean     NOT NULL DEFAULT false,
    created_at          timestamptz NOT NULL DEFAULT now()
);

-- One Trivy report for one artifact in one CI job. A repeated upload for the
-- same (service, job, artifact, kind) replaces the earlier one.
CREATE TABLE scans (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    service_id         bigint      NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    kind               text        NOT NULL CHECK (kind IN ('image', 'secret-fs')),
    artifact_name      text        NOT NULL,
    gitlab_pipeline_id bigint      NOT NULL,
    gitlab_job_id      bigint      NOT NULL,
    ref                text        NOT NULL,
    ref_protected      boolean     NOT NULL,
    commit_sha         text        NOT NULL,
    trivy_version      text        NOT NULL DEFAULT '',
    schema_version     integer     NOT NULL,
    scanned_at         timestamptz NOT NULL,
    received_at        timestamptz NOT NULL DEFAULT now(),
    -- Counts of unsuppressed findings, kept on the row for fast overviews.
    count_critical     integer     NOT NULL DEFAULT 0,
    count_high         integer     NOT NULL DEFAULT 0,
    count_medium       integer     NOT NULL DEFAULT 0,
    count_low          integer     NOT NULL DEFAULT 0,
    count_unknown      integer     NOT NULL DEFAULT 0,
    count_fixable      integer     NOT NULL DEFAULT 0,
    count_secrets      integer     NOT NULL DEFAULT 0,
    count_suppressed   integer     NOT NULL DEFAULT 0,
    -- The uploaded report, gzip-compressed, for audit and re-processing.
    raw_report         bytea       NOT NULL,
    UNIQUE (service_id, gitlab_job_id, artifact_name, kind)
);

CREATE INDEX scans_current_idx ON scans (service_id, ref, artifact_name, kind, scanned_at DESC, id DESC);
CREATE INDEX scans_received_at_idx ON scans (received_at);

CREATE TABLE vulnerabilities (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    scan_id           bigint  NOT NULL REFERENCES scans (id) ON DELETE CASCADE,
    target            text    NOT NULL,
    class             text    NOT NULL,
    pkg_type          text    NOT NULL DEFAULT '',
    pkg_name          text    NOT NULL,
    pkg_path          text    NOT NULL DEFAULT '',
    installed_version text    NOT NULL DEFAULT '',
    fixed_version     text    NOT NULL DEFAULT '',
    vuln_id           text    NOT NULL,
    severity          text    NOT NULL CHECK (severity IN ('CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'UNKNOWN')),
    status            text    NOT NULL DEFAULT '',
    title             text    NOT NULL DEFAULT '',
    primary_url       text    NOT NULL DEFAULT '',
    -- Suppressed by .trivyignore or a policy (reported by --show-suppressed).
    suppressed        boolean NOT NULL DEFAULT false,
    suppression       text    NOT NULL DEFAULT ''
);

CREATE INDEX vulnerabilities_scan_idx ON vulnerabilities (scan_id, id);
-- Cross-service search matches IDs case-insensitively (GHSA IDs are lower case).
CREATE INDEX vulnerabilities_vuln_id_idx ON vulnerabilities (lower(vuln_id));

-- Secret findings: rule and location only. The matched value is never stored.
CREATE TABLE secret_findings (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    scan_id     bigint  NOT NULL REFERENCES scans (id) ON DELETE CASCADE,
    target      text    NOT NULL,
    rule_id     text    NOT NULL,
    category    text    NOT NULL DEFAULT '',
    severity    text    NOT NULL CHECK (severity IN ('CRITICAL', 'HIGH', 'MEDIUM', 'LOW', 'UNKNOWN')),
    title       text    NOT NULL DEFAULT '',
    start_line  integer NOT NULL DEFAULT 0,
    end_line    integer NOT NULL DEFAULT 0,
    suppressed  boolean NOT NULL DEFAULT false
);

CREATE INDEX secret_findings_scan_idx ON secret_findings (scan_id, id);

-- Snapshot of one SonarQube analysis of a linked service.
CREATE TABLE sonar_analyses (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    service_id          bigint      NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    -- Webhook task ID, or 'sync:<analysis key>' for analyses found by sync.
    task_id             text        NOT NULL UNIQUE,
    analysis_key        text        NOT NULL DEFAULT '',
    analysed_at         timestamptz NOT NULL,
    revision            text        NOT NULL DEFAULT '',
    quality_gate_status text        NOT NULL DEFAULT '',
    -- [{metric, operator, threshold, value, status}]
    gate_conditions     jsonb       NOT NULL DEFAULT '[]'::jsonb,
    -- {metric key: value as reported (string)}; unknown metrics are absent.
    measures            jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- 'mqr' or 'standard' (SonarQube mode the measures came from).
    mode                text        NOT NULL DEFAULT '',
    -- Normalised values, independent of the mode, for overviews. Ratings
    -- are 'A' to 'E', or '' when SonarQube did not report the metric.
    reliability_rating      text NOT NULL DEFAULT '' CHECK (reliability_rating IN ('', 'A', 'B', 'C', 'D', 'E')),
    security_rating         text NOT NULL DEFAULT '' CHECK (security_rating IN ('', 'A', 'B', 'C', 'D', 'E')),
    maintainability_rating  text NOT NULL DEFAULT '' CHECK (maintainability_rating IN ('', 'A', 'B', 'C', 'D', 'E')),
    security_review_rating  text NOT NULL DEFAULT '' CHECK (security_review_rating IN ('', 'A', 'B', 'C', 'D', 'E')),
    -- Percentages; NULL when not reported (never 0 for "unknown").
    coverage                 double precision,
    duplicated_lines_density double precision,
    gitlab_pipeline_id  bigint,
    received_at         timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sonar_analyses_service_idx ON sonar_analyses (service_id, analysed_at DESC);

-- Every webhook delivery, linked or not, for the integration health page.
CREATE TABLE sonar_deliveries (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id     text        NOT NULL UNIQUE,
    project_key text        NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    -- 'pending' from receipt until the delivery has been processed.
    outcome     text        NOT NULL CHECK (outcome IN ('pending', 'linked', 'unlinked', 'failed')),
    service_id  bigint      REFERENCES services (id) ON DELETE SET NULL,
    detail      text        NOT NULL DEFAULT ''
);

CREATE INDEX sonar_deliveries_received_idx ON sonar_deliveries (received_at DESC);

-- Integration health lists recent events of one action (rejected uploads).
CREATE INDEX audit_log_action_at_idx ON audit_log (action, at DESC);

-- +goose Down
DROP INDEX audit_log_action_at_idx;
DROP TABLE sonar_deliveries;
DROP TABLE sonar_analyses;
DROP TABLE secret_findings;
DROP TABLE vulnerabilities;
DROP TABLE scans;
DROP TABLE services;
