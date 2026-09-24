-- name: CreateBus :one
-- No app-generated id needed (unlike ops.operators' self-registration
-- case): operator_id here already equals the caller's existing tenant
-- context, so the RLS SELECT policy backing this RETURNING clause is
-- satisfied immediately — verified empirically, see plan notes.
INSERT INTO fleet.buses (operator_id, registration_no, bus_class, amenities, active)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetBusByID :one
SELECT * FROM fleet.buses WHERE id = $1;

-- name: ListBusesByOperator :many
SELECT * FROM fleet.buses
WHERE operator_id = $1
  AND (
    sqlc.narg('cursor_created_at')::timestamptz IS NULL
    OR created_at < sqlc.narg('cursor_created_at')::timestamptz
    OR (created_at = sqlc.narg('cursor_created_at')::timestamptz AND id < sqlc.narg('cursor_id')::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: UpdateBus :one
UPDATE fleet.buses
SET registration_no = $2,
    bus_class = $3,
    amenities = $4,
    active = $5,
    updated_at = now()
WHERE id = $1
RETURNING *;
