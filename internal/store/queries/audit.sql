-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- name: InsertAudit :exec
INSERT INTO audit_log (actor, action, detail) VALUES ($1, $2, $3);
