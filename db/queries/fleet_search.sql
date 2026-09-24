-- Cross-operator search (FR-SRCH-1) — runs under a synthetic
-- platform_admin tenant context (see fleet/service/search_service.go),
-- since this is an intentionally cross-tenant read, restricted instead to
-- only approved operators' scheduled trips.
--
-- Two dedicated queries rather than one dynamic-ORDER-BY query: sqlc
-- needs static SQL, and a single CASE-based ORDER BY doesn't compose
-- cleanly with cursor pagination (the cursor's shape depends on which
-- column is sorted) — ENGINEERING_STANDARDS.md §4 requires cursor
-- pagination, never offset, for list endpoints, search included.

-- name: SearchTripsByPrice :many
SELECT
    t.id, t.operator_id, t.route_id, t.bus_id, t.departure_at, t.status,
    t.base_fare, t.available_seats,
    r.origin, r.destination,
    b.bus_class, b.amenities,
    o.name AS operator_name
FROM fleet.trips t
JOIN fleet.routes r ON r.id = t.route_id
JOIN fleet.buses b ON b.id = t.bus_id
JOIN ops.operators o ON o.id = t.operator_id
WHERE t.status = 'scheduled'
  AND o.status = 'approved'
  AND lower(r.origin) = lower(sqlc.arg('origin')::text)
  AND lower(r.destination) = lower(sqlc.arg('destination')::text)
  AND t.departure_at::date = sqlc.arg('travel_date')::date
  AND (sqlc.narg('bus_class')::text IS NULL OR b.bus_class = sqlc.narg('bus_class')::text)
  AND (sqlc.narg('min_price')::numeric IS NULL OR t.base_fare >= sqlc.narg('min_price')::numeric)
  AND (sqlc.narg('max_price')::numeric IS NULL OR t.base_fare <= sqlc.narg('max_price')::numeric)
  AND (
    sqlc.narg('cursor_price')::numeric IS NULL
    OR t.base_fare > sqlc.narg('cursor_price')::numeric
    OR (t.base_fare = sqlc.narg('cursor_price')::numeric AND t.id > sqlc.narg('cursor_id')::uuid)
  )
ORDER BY t.base_fare ASC, t.id ASC
LIMIT $1;

-- name: SearchTripsByDeparture :many
SELECT
    t.id, t.operator_id, t.route_id, t.bus_id, t.departure_at, t.status,
    t.base_fare, t.available_seats,
    r.origin, r.destination,
    b.bus_class, b.amenities,
    o.name AS operator_name
FROM fleet.trips t
JOIN fleet.routes r ON r.id = t.route_id
JOIN fleet.buses b ON b.id = t.bus_id
JOIN ops.operators o ON o.id = t.operator_id
WHERE t.status = 'scheduled'
  AND o.status = 'approved'
  AND lower(r.origin) = lower(sqlc.arg('origin')::text)
  AND lower(r.destination) = lower(sqlc.arg('destination')::text)
  AND t.departure_at::date = sqlc.arg('travel_date')::date
  AND (sqlc.narg('bus_class')::text IS NULL OR b.bus_class = sqlc.narg('bus_class')::text)
  AND (sqlc.narg('min_price')::numeric IS NULL OR t.base_fare >= sqlc.narg('min_price')::numeric)
  AND (sqlc.narg('max_price')::numeric IS NULL OR t.base_fare <= sqlc.narg('max_price')::numeric)
  AND (
    sqlc.narg('cursor_departure_at')::timestamptz IS NULL
    OR t.departure_at > sqlc.narg('cursor_departure_at')::timestamptz
    OR (t.departure_at = sqlc.narg('cursor_departure_at')::timestamptz AND t.id > sqlc.narg('cursor_id')::uuid)
  )
ORDER BY t.departure_at ASC, t.id ASC
LIMIT $1;
