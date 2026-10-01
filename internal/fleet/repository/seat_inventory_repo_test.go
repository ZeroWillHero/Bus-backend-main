package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sl-bus/backend/internal/platform/testutil"
)

// TestHoldSeatsNoOverselling verifies that exactly one concurrent hold succeeds
// when multiple goroutines compete for the same seat. This is ARCHITECTURE.md
// §3.4's mandated concurrency test — the optimistic-concurrency version check
// in the SQL must prevent overselling via DB-level uniqueness.
func TestHoldSeatsNoOverselling(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewTestPool(t)
	defer pool.Close()

	repo := NewSeatInventoryRepository(pool, nil)

	// Seed a trip with one available seat
	tripID := testutil.SeedTrip(t, pool, testutil.TripSeed{
		AvailableSeats: 1,
		Seats: []testutil.SeatSeed{
			{SeatNumber: "A1", Status: "available"},
		},
	})

	// Fire 20 concurrent hold attempts on the same seat
	const numGoroutines = 20
	results := make([]int, numGoroutines)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			heldCount, err := repo.HoldSeats(ctx, tripID, []string{"A1"}, fmt.Sprintf("00000000-0000-0000-0000-%012d", idx+1), 10*time.Minute)
			if err != nil {
				t.Errorf("goroutine %d: hold failed: %v", idx, err)
				return
			}

			mu.Lock()
			results[idx] = heldCount
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	// Exactly one goroutine should have held the seat (heldCount=1)
	// The other 19 should have gotten 0 (unavailable)
	var successCount int
	var failCount int
	for _, held := range results {
		switch held {
		case 1:
			successCount++
		case 0:
			failCount++
		default:
			t.Errorf("unexpected heldCount: %d", held)
		}
	}

	if successCount != 1 {
		t.Errorf("expected exactly 1 successful hold, got %d", successCount)
	}
	if failCount != numGoroutines-1 {
		t.Errorf("expected %d failures, got %d", numGoroutines-1, failCount)
	}

	// Verify the seat is actually held in the DB
	row := pool.QueryRow(ctx, `
		SELECT status FROM fleet.trip_seat_inventory
		WHERE trip_id = $1 AND seat_number = 'A1'
	`, tripID)
	var status string
	if err := row.Scan(&status); err != nil {
		t.Fatalf("failed to check seat status: %v", err)
	}
	if status != "held" {
		t.Errorf("expected seat status 'held', got '%s'", status)
	}

	// Verify available_seats was decremented by exactly 1
	row = pool.QueryRow(ctx, `SELECT available_seats FROM fleet.trips WHERE id = $1`, tripID)
	var availableSeats int
	if err := row.Scan(&availableSeats); err != nil {
		t.Fatalf("failed to check trip available_seats: %v", err)
	}
	if availableSeats != 0 {
		t.Errorf("expected available_seats=0, got %d", availableSeats)
	}
}

// TestReleaseExpiredHolds verifies that the blanket sweep transitions expired
// held seats back to available and increments trips.available_seats.
func TestReleaseExpiredHolds(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewTestPool(t)
	defer pool.Close()

	repo := NewSeatInventoryRepository(pool, nil)

	// Seed a trip with 2 seats, both held with expired holds
	tripID := testutil.SeedTrip(t, pool, testutil.TripSeed{
		AvailableSeats: 0,
		Seats: []testutil.SeatSeed{
			{
				SeatNumber:    "A1",
				Status:        "held",
				BookingID:     "00000000-0000-0000-0000-000000000001",
				HoldExpiresAt: time.Now().Add(-1 * time.Hour), // expired
			},
			{
				SeatNumber:    "A2",
				Status:        "held",
				BookingID:     "00000000-0000-0000-0000-000000000002",
				HoldExpiresAt: time.Now().Add(-30 * time.Minute), // expired
			},
		},
	})

	// Run the cleanup sweep
	releasedCount, err := repo.ReleaseExpiredHolds(ctx)
	if err != nil {
		t.Fatalf("ReleaseExpiredHolds failed: %v", err)
	}

	if releasedCount != 2 {
		t.Errorf("expected 2 seats released, got %d", releasedCount)
	}

	// Verify both seats are now available
	rows, err := pool.Query(ctx, `
		SELECT seat_number, status FROM fleet.trip_seat_inventory
		WHERE trip_id = $1 ORDER BY seat_number
	`, tripID)
	if err != nil {
		t.Fatalf("failed to query seats: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		count++
		var seatNumber, status string
		if err := rows.Scan(&seatNumber, &status); err != nil {
			t.Errorf("failed to scan seat: %v", err)
		}
		if status != "available" {
			t.Errorf("seat %s: expected status 'available', got '%s'", seatNumber, status)
		}
	}
	if count != 2 {
		t.Errorf("expected 2 seats, got %d", count)
	}

	// Verify available_seats was incremented
	row := pool.QueryRow(ctx, `SELECT available_seats FROM fleet.trips WHERE id = $1`, tripID)
	var availableSeats int
	if err := row.Scan(&availableSeats); err != nil {
		t.Fatalf("failed to check trip available_seats: %v", err)
	}
	if availableSeats != 2 {
		t.Errorf("expected available_seats=2, got %d", availableSeats)
	}
}

// TestConfirmSeatsTransition verifies that held seats transition to booked.
func TestConfirmSeatsTransition(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewTestPool(t)
	defer pool.Close()

	repo := NewSeatInventoryRepository(pool, nil)

	bookingID := "00000000-0000-0000-0000-000000000123"
	tripID := testutil.SeedTrip(t, pool, testutil.TripSeed{
		AvailableSeats: 0,
		Seats: []testutil.SeatSeed{
			{
				SeatNumber:    "A1",
				Status:        "held",
				BookingID:     bookingID,
				HoldExpiresAt: time.Now().Add(10 * time.Minute),
			},
		},
	})

	// Confirm the held seat
	err := repo.ConfirmSeats(ctx, tripID, []string{"A1"}, bookingID)
	if err != nil {
		t.Fatalf("ConfirmSeats failed: %v", err)
	}

	// Verify seat status is now 'booked'
	row := pool.QueryRow(ctx, `
		SELECT status FROM fleet.trip_seat_inventory
		WHERE trip_id = $1 AND seat_number = 'A1'
	`, tripID)
	var status string
	if err := row.Scan(&status); err != nil {
		t.Fatalf("failed to check seat status: %v", err)
	}
	if status != "booked" {
		t.Errorf("expected status 'booked', got '%s'", status)
	}
}

// TestReleaseSeatsTransition verifies that held/booked seats transition to available.
func TestReleaseSeatsTransition(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewTestPool(t)
	defer pool.Close()

	repo := NewSeatInventoryRepository(pool, nil)

	tripID := testutil.SeedTrip(t, pool, testutil.TripSeed{
		AvailableSeats: 0,
		Seats: []testutil.SeatSeed{
			{
				SeatNumber:    "A1",
				Status:        "held",
				BookingID:     "00000000-0000-0000-0000-000000000001",
				HoldExpiresAt: time.Now().Add(10 * time.Minute),
			},
		},
	})

	// Release the held seat
	err := repo.ReleaseSeats(ctx, tripID, []string{"A1"})
	if err != nil {
		t.Fatalf("ReleaseSeats failed: %v", err)
	}

	// Verify seat status is now 'available'
	row := pool.QueryRow(ctx, `
		SELECT status, booking_id, hold_expires_at FROM fleet.trip_seat_inventory
		WHERE trip_id = $1 AND seat_number = 'A1'
	`, tripID)
	var status string
	var bookingID *string
	var holdExpiresAt *time.Time
	if err := row.Scan(&status, &bookingID, &holdExpiresAt); err != nil {
		t.Fatalf("failed to check seat: %v", err)
	}
	if status != "available" {
		t.Errorf("expected status 'available', got '%s'", status)
	}
	if bookingID != nil {
		t.Errorf("expected booking_id to be null, got %v", *bookingID)
	}
	if holdExpiresAt != nil {
		t.Errorf("expected hold_expires_at to be null, got %v", *holdExpiresAt)
	}

	// Verify available_seats was incremented
	row = pool.QueryRow(ctx, `SELECT available_seats FROM fleet.trips WHERE id = $1`, tripID)
	var availableSeats int
	if err := row.Scan(&availableSeats); err != nil {
		t.Fatalf("failed to check available_seats: %v", err)
	}
	if availableSeats != 1 {
		t.Errorf("expected available_seats=1, got %d", availableSeats)
	}
}
