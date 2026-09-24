-- name: CreateTrip :one
INSERT INTO fleet.trips (operator_id, route_id, bus_id, departure_at, base_fare, available_seats)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetTripByID :one
SELECT * FROM fleet.trips WHERE id = $1;

-- name: ListTripsByOperator :many
SELECT * FROM fleet.trips
WHERE operator_id = $1
  AND (sqlc.narg('route_id')::uuid IS NULL OR route_id = sqlc.narg('route_id')::uuid)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (
    sqlc.narg('cursor_departure_at')::timestamptz IS NULL
    OR departure_at < sqlc.narg('cursor_departure_at')::timestamptz
    OR (departure_at = sqlc.narg('cursor_departure_at')::timestamptz AND id < sqlc.narg('cursor_id')::uuid)
  )
ORDER BY departure_at DESC, id DESC
LIMIT $2;

-- name: UpdateTripStatus :one
UPDATE fleet.trips SET status = $2, updated_at = now() WHERE id = $1
RETURNING *;

-- name: RescheduleTrip :one
UPDATE fleet.trips SET departure_at = $2, updated_at = now() WHERE id = $1
RETURNING *;

-- name: DecrementTripAvailableSeats :exec
UPDATE fleet.trips SET available_seats = available_seats - 1, updated_at = now()
WHERE id = $1 AND available_seats > 0;

-- name: CreateTripSeatInventory :exec
INSERT INTO fleet.trip_seat_inventory (trip_id, seat_number)
VALUES ($1, $2);

-- name: CountTripSeatInventory :one
SELECT count(*) FROM fleet.trip_seat_inventory WHERE trip_id = $1;
