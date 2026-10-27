package domain

import (
	"context"
	"time"
)

// SortBy is one of the two sort modes the search repository supports —
// see fleet_search.sql for why this is two dedicated queries rather than
// one dynamic ORDER BY.
type SortBy string

const (
	SortByPrice     SortBy = "price"
	SortByDeparture SortBy = "departure"
)

// BookingCutoff is how close to departure a trip stops being bookable: search
// hides it, the seat map reports it as not bookable, and holds are refused.
// One value for all three so a passenger never sees a trip they can't hold.
const BookingCutoff = 10 * time.Minute

// TripFallbackDuration is how long after departure a trip is treated as
// arrived when its route has no stop arrival offsets to derive it from.
const TripFallbackDuration = 12 * time.Hour

type SearchCriteria struct {
	Origin      string
	Destination string
	TravelDate  time.Time
	SortBy      SortBy
	BusClass    string
	MinPrice    *float64
	MaxPrice    *float64
	Cursor      string
	Limit       int
}

// TripSearchResult is the read-model FR-SRCH-1/2/3 project — a
// denormalized shape combining Trip + Route + Bus + Operator, since
// search is a dedicated read-side query, not a reconstruction of the
// Trip aggregate (ARCHITECTURE.md §3.3's CQRS-lite pattern).
type TripSearchResult struct {
	TripID         string
	OperatorID     string
	OperatorName   string
	Origin         string
	Destination    string
	DepartureAt    time.Time
	BusClass       string
	Amenities      map[string]any
	BaseFare       float64
	AvailableSeats int
}

type SearchRepository interface {
	Search(ctx context.Context, criteria SearchCriteria) (results []TripSearchResult, nextCursor string, err error)
}

// TripSeat is one seat of a trip's seat map as a passenger sees it before
// booking: where it sits on the operator's grid, whether it can still be
// taken, and what it costs. Status is the raw inventory state ("available",
// "held", "booked") — exactly what the hold endpoint will accept.
type TripSeat struct {
	SeatNumber string
	SeatType   string
	Row        int
	Col        int
	Status     string
	Fare       float64
}
