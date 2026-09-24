-- name: UpsertSeatMap :one
INSERT INTO fleet.seat_maps (bus_id, total_seats)
VALUES ($1, $2)
ON CONFLICT (bus_id) DO UPDATE SET total_seats = EXCLUDED.total_seats, updated_at = now()
RETURNING *;

-- name: GetSeatMapByBusID :one
SELECT * FROM fleet.seat_maps WHERE bus_id = $1;

-- name: DeleteSeatsForSeatMap :exec
DELETE FROM fleet.seats WHERE seat_map_id = $1;

-- name: CreateSeat :one
INSERT INTO fleet.seats (seat_map_id, seat_number, seat_type, row_no, col_no)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListSeatsBySeatMapID :many
SELECT * FROM fleet.seats WHERE seat_map_id = $1 ORDER BY row_no, col_no;
