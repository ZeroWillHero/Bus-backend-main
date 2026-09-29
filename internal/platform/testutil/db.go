package testutil

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/platform/db"
)

// NewTestPool creates a test database connection pool pointing to the test database.
// Caller is responsible for calling pool.Close().
func NewTestPool(t *testing.T) *pgxpool.Pool {
	testDSN := os.Getenv("TEST_DATABASE_URL")
	if testDSN == "" {
		t.Skip("TEST_DATABASE_URL not set, skipping database tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := db.NewPool(ctx, testDSN)
	if err != nil {
		t.Fatalf("failed to create test pool: %v", err)
	}

	// Clean up before returning
	t.Cleanup(func() {
		pool.Close()
	})

	return pool
}

type SeatSeed struct {
	SeatNumber    string
	Status        string
	BookingID     string
	HoldExpiresAt time.Time
}

type TripSeed struct {
	AvailableSeats int
	Seats          []SeatSeed
}

// SeedTrip creates a test trip with the given seats in fleet.trip_seat_inventory.
// Returns the created trip ID.
func SeedTrip(t *testing.T, pool *pgxpool.Pool, seed TripSeed) string {
	ctx := context.Background()

	// Create an operator for FK constraint
	var operatorID string
	err := pool.QueryRow(ctx, `
		INSERT INTO identity.users (id, email, phone_number, verified_phone, nic_number, full_name, role, status)
		VALUES (gen_random_uuid(), 'op-'||gen_random_uuid()::text||'@test.local', '+94123456789', TRUE, '123456789', 'Test Operator', 'operator', 'active')
		RETURNING id
	`).Scan(&operatorID)
	if err != nil {
		t.Fatalf("failed to create operator: %v", err)
	}

	// Create an operator record in ops.operators
	err = pool.QueryRow(ctx, `
		INSERT INTO ops.operators (id, user_id, name, status, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, 'Test Operator', 'approved', now(), now())
		RETURNING id
	`, operatorID).Scan(&operatorID)
	if err != nil {
		t.Fatalf("failed to create ops.operator record: %v", err)
	}

	// Create a route
	var routeID string
	err = pool.QueryRow(ctx, `
		INSERT INTO fleet.routes (id, operator_id, name, description)
		VALUES (gen_random_uuid(), $1, 'Test Route', 'Test route for seeding')
		RETURNING id
	`, operatorID).Scan(&routeID)
	if err != nil {
		t.Fatalf("failed to create route: %v", err)
	}

	// Create a bus
	var busID string
	err = pool.QueryRow(ctx, `
		INSERT INTO fleet.buses (id, operator_id, registration, vin, model, total_seats, status)
		VALUES (gen_random_uuid(), $1, 'TEST-BUS-'||substr(gen_random_uuid()::text,1,8), 'TEST-VIN-001', 'Test Model', $2, 'active')
		RETURNING id
	`, operatorID, len(seed.Seats)).Scan(&busID)
	if err != nil {
		t.Fatalf("failed to create bus: %v", err)
	}

	// Create seats in fleet.bus_seat_layout
	for _, seatSeed := range seed.Seats {
		_, err := pool.Exec(ctx, `
			INSERT INTO fleet.bus_seat_layout (bus_id, seat_number, row_number, column_number, is_wheelchair_accessible)
			VALUES ($1, $2, 1, 1, FALSE)
		`, busID, seatSeed.SeatNumber)
		if err != nil {
			t.Fatalf("failed to create bus seat: %v", err)
		}
	}

	// Create a trip
	var tripID string
	err = pool.QueryRow(ctx, `
		INSERT INTO fleet.trips (
			id, route_id, bus_id, departure_time, arrival_time,
			base_fare, status, available_seats, created_at, updated_at
		)
		VALUES (
			gen_random_uuid(), $1, $2, now() + interval '1 day', now() + interval '2 days',
			1000.00, 'scheduled', $3, now(), now()
		)
		RETURNING id
	`, routeID, busID, seed.AvailableSeats).Scan(&tripID)
	if err != nil {
		t.Fatalf("failed to create trip: %v", err)
	}

	// Create seats in fleet.trip_seat_inventory
	for _, seatSeed := range seed.Seats {
		holdExpiresAtVal := interface{}(nil)
		if !seatSeed.HoldExpiresAt.IsZero() {
			holdExpiresAtVal = seatSeed.HoldExpiresAt
		}

		bookingIDVal := interface{}(nil)
		if seatSeed.BookingID != "" {
			bookingIDVal = seatSeed.BookingID
		}

		_, err := pool.Exec(ctx, `
			INSERT INTO fleet.trip_seat_inventory (
				trip_id, seat_number, status, booking_id, hold_expires_at, version
			)
			VALUES ($1, $2, $3, $4, $5, 0)
		`, tripID, seatSeed.SeatNumber, seatSeed.Status, bookingIDVal, holdExpiresAtVal)
		if err != nil {
			t.Fatalf("failed to create trip_seat_inventory: %v", err)
		}
	}

	return tripID
}
