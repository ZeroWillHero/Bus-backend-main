package domain

import (
	"context"
	"errors"
	"time"
)

// Seat's Row/Col form the logical grid the operator's seat-map canvas
// snaps to. Irregular layouts (a wider back row, aisles in different
// places per row) are expressed by non-contiguous Col values per row,
// not by a separate continuous pixel-coordinate system.
type Seat struct {
	Number string
	Type   string
	Row    int
	Col    int
}

type SeatMap struct {
	BusID      string
	TotalSeats int
	Seats      []Seat
	UpdatedAt  time.Time
}

// ValidateSeats enforces the seat-map invariants a saved layout must
// satisfy — pure, unit-testable without a database.
func ValidateSeats(seats []Seat) error {
	if len(seats) == 0 {
		return ErrEmptySeatMap
	}
	seen := make(map[string]struct{}, len(seats))
	for _, s := range seats {
		if s.Number == "" {
			return ErrInvalidSeatMap
		}
		if _, dup := seen[s.Number]; dup {
			return ErrDuplicateSeatNumber
		}
		seen[s.Number] = struct{}{}
		if s.Row < 0 || s.Col < 0 {
			return ErrInvalidSeatMap
		}
	}
	return nil
}

var (
	ErrEmptySeatMap        = errors.New("fleet: seat map must contain at least one seat")
	ErrDuplicateSeatNumber = errors.New("fleet: duplicate seat number in seat map")
	ErrInvalidSeatMap      = errors.New("fleet: invalid seat map")
)

type SeatMapRepository interface {
	Replace(ctx context.Context, busID string, seats []Seat) (*SeatMap, error)
	FindByBusID(ctx context.Context, busID string) (*SeatMap, error)
}
