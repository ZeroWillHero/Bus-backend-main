-- name: GetTrustedDevice :one
SELECT * FROM identity.trusted_devices
WHERE user_id = $1 AND device_fingerprint = $2;

-- name: UpsertTrustedDevice :one
INSERT INTO identity.trusted_devices (user_id, device_fingerprint, trusted_until)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, device_fingerprint)
DO UPDATE SET trusted_until = EXCLUDED.trusted_until
RETURNING *;
