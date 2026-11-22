-- Copyright 2026 The Scanopsis Authors
-- SPDX-License-Identifier: Apache-2.0

-- name: DeleteRecoveryCodes :exec
DELETE FROM recovery_codes WHERE user_id = $1;

-- name: InsertRecoveryCode :exec
INSERT INTO recovery_codes (user_id, code_hash, version) VALUES (@user_id, @code_hash, @version);

-- UseRecoveryCode marks an unused code stored with the given version as used
-- and returns it; no row means the code is unknown or already used.
-- name: UseRecoveryCode :one
UPDATE recovery_codes SET used_at = now()
WHERE user_id = @user_id AND code_hash = @code_hash AND version = @version AND used_at IS NULL
RETURNING user_id;

-- name: CountUnusedRecoveryCodes :one
SELECT count(*) FROM recovery_codes WHERE user_id = $1 AND used_at IS NULL;
