-- name: GetUserByEmail :one
SELECT * FROM identity.users WHERE email = $1;

-- name: GetUserByPhone :one
SELECT * FROM identity.users WHERE phone = $1;

-- name: GetUserByID :one
SELECT * FROM identity.users WHERE id = $1;

-- name: CreateUser :one
INSERT INTO identity.users (phone, email, password_hash, role, operator_id, mfa_totp_secret)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: CreatePassengerUser :one
INSERT INTO identity.users (phone, role)
VALUES ($1, 'passenger')
RETURNING *;

-- name: IncrementFailedLoginAttempts :one
UPDATE identity.users
SET failed_login_attempts = failed_login_attempts + 1,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetUserLockedUntil :exec
UPDATE identity.users
SET locked_until = $2,
    updated_at = now()
WHERE id = $1;

-- name: ResetUserLockout :exec
UPDATE identity.users
SET failed_login_attempts = 0,
    locked_until = NULL,
    updated_at = now()
WHERE id = $1;

-- name: SetUserMFASecret :exec
UPDATE identity.users
SET mfa_totp_secret = $2,
    updated_at = now()
WHERE id = $1;

-- name: UpdateUserPassword :exec
UPDATE identity.users
SET password_hash = $2,
    updated_at = now()
WHERE id = $1;
