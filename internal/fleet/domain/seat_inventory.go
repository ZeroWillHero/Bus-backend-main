package domain

import (
	"context"
	"time"
)

// SeatInventoryItem is one row of a trip's seat inventory. Phase 2 only
// creates these (status "available", one per seat) at trip creation; the
// hold/held/booked state machine on them is Booking's responsibility
// (Phase 3) — see fleet.trip_seat_inventory's migration comment.
type SeatInventoryItem struct {
	TripID     string
	SeatNumber string
	Status     string
}

// SeatHoldPort: the cross-context dependency from Booking → Fleet.
// Fleet owns the seat inventory; booking triggers holds/releases/confirms via this port.
// Implemented by fleet's SeatInventoryRepository; booking.service depends on it.
// The actual invariant preventing overselling is the DB's UNIQUE(trip_id, seat_number)
// + optimistic-concurrency version check on trip_seat_inventory rows (DATABASE_DESIGN.md §3).
type SeatHoldPort interface {
	// HoldSeats attempts to hold the requested seat numbers for the booking.
	// Returns the actual count of seats successfully held (< len(seatNumbers) signals
	// partial unavailability). A background worker will auto-release these after ttl.
	HoldSeats(ctx context.Context, tripID string, seatNumbers []string, bookingID string, ttl time.Duration) (heldCount int, err error)

	// ConfirmSeats transitions held seats to booked status, called after payment succeeds.
	ConfirmSeats(ctx context.Context, tripID string, seatNumbers []string, bookingID string) error

	// ReleaseSeats transitions held/booked seats back to available (used for cancellations).
	ReleaseSeats(ctx context.Context, tripID string, seatNumbers []string) error

	// ReleaseExpiredHolds is the idempotent mitigation for 1.1 (MITIGATION_PLAN.md §1.1):
	// a blanket sweep that flips all held rows with hold_expires_at < now() back to available,
	// callable by multiple worker replicas simultaneously. Returns the count released.
	ReleaseExpiredHolds(ctx context.Context) (releasedCount int, err error)
}
