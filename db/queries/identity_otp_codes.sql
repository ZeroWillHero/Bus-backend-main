-- name: CreateOTPCode :one
INSERT INTO identity.otp_codes (phone, code_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetLatestUnconsumedOTP :one
SELECT * FROM identity.otp_codes
WHERE phone = $1 AND consumed_at IS NULL
ORDER BY created_at DESC
LIMIT 1;

-- name: ConsumeOTPCode :exec
UPDATE identity.otp_codes
SET consumed_at = now()
WHERE id = $1;

-- name: CountOTPCodesSince :one
SELECT count(*) FROM identity.otp_codes
WHERE phone = $1 AND created_at > $2;
