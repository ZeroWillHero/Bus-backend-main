-- name: CreateRefreshToken :one
INSERT INTO identity.refresh_tokens (user_id, token_hash, device_info, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetRefreshTokenByHash :one
SELECT * FROM identity.refresh_tokens WHERE token_hash = $1;

-- name: RevokeRefreshToken :exec
UPDATE identity.refresh_tokens
SET revoked_at = now()
WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeAllRefreshTokensForUser :exec
UPDATE identity.refresh_tokens
SET revoked_at = now()
WHERE user_id = $1 AND revoked_at IS NULL;

-- name: ListActiveRefreshTokensForUser :many
SELECT * FROM identity.refresh_tokens
WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()
ORDER BY created_at DESC;
