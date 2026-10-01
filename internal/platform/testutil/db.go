package testutil

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SeedOperator inserts an approved ops.operators row and returns its id.
func SeedOperator(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO ops.operators (name, business_reg_no, status)
		VALUES ('Test Operator', 'REG-' || gen_random_uuid()::text, 'approved')
		RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatalf("seed operator: %v", err)
	}
	return id
}

// SeedPassenger inserts a passenger identity.users row and returns its id.
func SeedPassenger(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO identity.users (phone, role)
		VALUES ('+94' || (floor(random()*900000000)+100000000)::bigint::text || substr(gen_random_uuid()::text,1,4), 'passenger')
		RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatalf("seed passenger: %v", err)
	}
	return id
}

type SeatSeed struct {
	SeatNumber    string
	Status        string
	SeatType      string // defaults to "standard"
	BookingID     string
	HoldExpiresAt time.Time
}

type TripSeed struct {
	AvailableSeats int
	Seats          []SeatSeed
	// Fares maps seat_type -> server-side fare. Defaults to {"standard": 1000}.
	Fares map[string]float64
}

// SeedTrip creates operator → route → bus → seat map/seats → trip → fares →
// trip_seat_inventory against the real schema and returns the trip id.
func SeedTrip(t *testing.T, pool *pgxpool.Pool, seed TripSeed) string {
	t.Helper()
	ctx := context.Background()
	operatorID := SeedOperator(t, pool)

	fares := seed.Fares
	if fares == nil {
		fares = map[string]float64{"standard": 1000}
	}

	var routeID, busID, mapID, tripID string
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	must("route", pool.QueryRow(ctx, `
		INSERT INTO fleet.routes (operator_id, origin, destination)
		VALUES ($1, 'Colombo', 'Kandy') RETURNING id`, operatorID).Scan(&routeID))
	must("bus", pool.QueryRow(ctx, `
		INSERT INTO fleet.buses (operator_id, registration_no, bus_class)
		VALUES ($1, 'TEST-' || substr(gen_random_uuid()::text,1,8), 'standard') RETURNING id`, operatorID).Scan(&busID))
	must("seat map", pool.QueryRow(ctx, `
		INSERT INTO fleet.seat_maps (bus_id, total_seats) VALUES ($1, $2) RETURNING id`, busID, len(seed.Seats)).Scan(&mapID))

	for i, s := range seed.Seats {
		seatType := s.SeatType
		if seatType == "" {
			seatType = "standard"
		}
		_, err := pool.Exec(ctx, `
			INSERT INTO fleet.seats (seat_map_id, seat_number, seat_type, row_no, col_no)
			VALUES ($1, $2, $3, $4, 1)`, mapID, s.SeatNumber, seatType, i+1)
		must("seat "+s.SeatNumber, err)
	}

	base := 1000.0
	for _, f := range fares {
		base = f
		break
	}
	must("trip", pool.QueryRow(ctx, `
		INSERT INTO fleet.trips (operator_id, route_id, bus_id, departure_at, base_fare, available_seats)
		VALUES ($1, $2, $3, now() + interval '1 day', $4, $5) RETURNING id`,
		operatorID, routeID, busID, base, seed.AvailableSeats).Scan(&tripID))

	for seatType, fare := range fares {
		_, err := pool.Exec(ctx, `INSERT INTO fleet.trip_fares (trip_id, seat_type, fare) VALUES ($1, $2, $3)`,
			tripID, seatType, fare)
		must(fmt.Sprintf("fare %s", seatType), err)
	}

	for _, s := range seed.Seats {
		var holdExpiresAt, bookingID interface{}
		if !s.HoldExpiresAt.IsZero() {
			holdExpiresAt = s.HoldExpiresAt
		}
		if s.BookingID != "" {
			bookingID = s.BookingID
		}
		status := s.Status
		if status == "" {
			status = "available"
		}
		_, err := pool.Exec(ctx, `
			INSERT INTO fleet.trip_seat_inventory (trip_id, seat_number, status, booking_id, hold_expires_at, version)
			VALUES ($1, $2, $3, $4, $5, 0)`, tripID, s.SeatNumber, status, bookingID, holdExpiresAt)
		must("inventory "+s.SeatNumber, err)
	}
	return tripID
}
