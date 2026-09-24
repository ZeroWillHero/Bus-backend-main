-- name: CreateRoute :one
INSERT INTO fleet.routes (operator_id, origin, destination, distance_km)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetRouteByID :one
SELECT * FROM fleet.routes WHERE id = $1;

-- name: ListRoutesByOperator :many
SELECT * FROM fleet.routes
WHERE operator_id = $1
  AND (
    sqlc.narg('cursor_created_at')::timestamptz IS NULL
    OR created_at < sqlc.narg('cursor_created_at')::timestamptz
    OR (created_at = sqlc.narg('cursor_created_at')::timestamptz AND id < sqlc.narg('cursor_id')::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: UpdateRoute :one
UPDATE fleet.routes
SET origin = $2,
    destination = $3,
    distance_km = $4,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteRouteStops :exec
DELETE FROM fleet.route_stops WHERE route_id = $1;

-- name: CreateRouteStop :one
INSERT INTO fleet.route_stops (route_id, stop_name, sequence, arrival_offset_min, departure_offset_min)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListRouteStopsByRouteID :many
SELECT * FROM fleet.route_stops WHERE route_id = $1 ORDER BY sequence;
