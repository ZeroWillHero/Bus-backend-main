-- name: CreateTripFare :one
INSERT INTO fleet.trip_fares (trip_id, seat_type, fare)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListTripFaresByTripID :many
SELECT * FROM fleet.trip_fares WHERE trip_id = $1 ORDER BY seat_type;

-- name: UpdateTripFare :one
UPDATE fleet.trip_fares SET fare = $3 WHERE trip_id = $1 AND seat_type = $2
RETURNING *;
