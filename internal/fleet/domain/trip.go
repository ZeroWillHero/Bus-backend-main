package domain

import (
	"context"
	"time"
)

type TripStatus string

const (
	TripStatusScheduled TripStatus = "scheduled"
	TripStatusCancelled TripStatus = "cancelled"
	TripStatusCompleted TripStatus = "completed"
)

// Trip is the Fleet & Scheduling aggregate root (DOMAIN_MODEL.md §2.3),
// owning SeatInventory. Phase 2 does not implement HoldSeats/
// ReleaseSeats/ConfirmSeats — those are Booking-triggered (Phase 3);
// adding them now, unused, would be speculative. Phase 2's Trip only
// needs creation/scheduling/cancellation invariants.
type Trip struct {
	ID             string
	OperatorID     string
	RouteID        string
	BusID          string
	DepartureAt    time.Time
	Status         TripStatus
	BaseFare       float64
	AvailableSeats int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CanTransitionStatus encodes the allowed status verb graph (FR-FLT-6:
// cancel or reschedule). Pure and unit-testable without a DB, mirroring
// ops.CanTransition's pattern.
func CanTransitionStatus(from, to TripStatus) bool {
	switch from {
	case TripStatusScheduled:
		return to == TripStatusCancelled || to == TripStatusCompleted
	default:
		return false
	}
}

// SameOperator enforces the invariant that a trip's bus and route must
// belong to the same operator that owns the trip — a business rule
// named explicitly in DOMAIN_MODEL.md's context map, not just an FK
// constraint (RLS scoping means this should already hold in practice,
// but the domain layer states the invariant rather than trusting that).
func SameOperator(operatorID string, bus *Bus, route *Route) bool {
	return bus.OperatorID == operatorID && route.OperatorID == operatorID
}

type TripFare struct {
	SeatType string
	Fare     float64
}

type TripRepository interface {
	Create(ctx context.Context, t *Trip) (*Trip, error)
	FindByID(ctx context.Context, id string) (*Trip, error)
	ListByOperator(ctx context.Context, operatorID string, filter TripListFilter, cursor string, limit int) (trips []*Trip, nextCursor string, err error)
	UpdateStatus(ctx context.Context, id string, status TripStatus) (*Trip, error)
	Reschedule(ctx context.Context, id string, departureAt time.Time) (*Trip, error)
	SeedSeatInventory(ctx context.Context, tripID string, seatNumbers []string) error
}

type TripListFilter struct {
	RouteID string
	Status  string
}

type TripFareRepository interface {
	Create(ctx context.Context, tripID string, fare TripFare) (*TripFare, error)
	ListByTripID(ctx context.Context, tripID string) ([]TripFare, error)
	Update(ctx context.Context, tripID, seatType string, fare float64) (*TripFare, error)
}
