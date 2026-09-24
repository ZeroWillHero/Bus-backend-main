package domain

import (
	"context"
	"time"
)

type RouteStop struct {
	StopName           string
	Sequence           int
	ArrivalOffsetMin   *int
	DepartureOffsetMin *int
}

// Route is an entity referenced by Trip (FK) — its own lifecycle,
// created/edited independently via FR-FLT-3.
type Route struct {
	ID          string
	OperatorID  string
	Origin      string
	Destination string
	DistanceKm  *float64
	Stops       []RouteStop
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type RouteRepository interface {
	Create(ctx context.Context, r *Route) (*Route, error)
	FindByID(ctx context.Context, id string) (*Route, error)
	ListByOperator(ctx context.Context, operatorID, cursor string, limit int) (routes []*Route, nextCursor string, err error)
	Update(ctx context.Context, r *Route) (*Route, error)
	ReplaceStops(ctx context.Context, routeID string, stops []RouteStop) error
	ListStops(ctx context.Context, routeID string) ([]RouteStop, error)
}
