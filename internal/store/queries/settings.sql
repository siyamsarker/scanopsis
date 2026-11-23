-- Copyright 2026 Siyam Sarker
-- SPDX-License-Identifier: Apache-2.0

-- name: ListSettings :many
SELECT * FROM settings ORDER BY key;

-- SeedSetting stores an environment value only if the key has no row yet.
-- name: SeedSetting :exec
INSERT INTO settings (key, value, secret_enc, source, updated_by)
VALUES (@key, @value, @secret_enc, 'env', 'system')
ON CONFLICT (key) DO NOTHING;

-- name: UpsertSetting :exec
INSERT INTO settings (key, value, secret_enc, source, updated_by, updated_at)
VALUES (@key, @value, @secret_enc, 'ui', @updated_by, now())
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value, secret_enc = EXCLUDED.secret_enc, source = 'ui',
    updated_by = EXCLUDED.updated_by, updated_at = now();

-- name: DeleteSetting :exec
DELETE FROM settings WHERE key = $1;
