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
