package service

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/fleet/domain"
	"github.com/sl-bus/backend/internal/platform/auth"
	platformdb "github.com/sl-bus/backend/internal/platform/db"
)

// Currency is the platform's single settlement currency.
const Currency = "LKR"

// ReadModelService computes the derived fields the operator app shows next
// to fleet entities (seat counts, embedded route/bus names, upcoming-trip
// counts) in one query per page, instead of one request per row on the
// phone. It only reads; everything runs under the caller's tenant context.
type ReadModelService struct {
	pool *pgxpool.Pool
}

func NewReadModelService(pool *pgxpool.Pool) *ReadModelService {
	return &ReadModelService{pool: pool}
}

type RouteRef struct {
	ID          string
	Origin      string
	Destination string
}

type BusRef struct {
	ID             string
	RegistrationNo string
	BusClass       string
}

// TripExtras: AvailableSeats on the trip itself is a denormalised counter
// maintained at hold/confirm time; these three come straight from the seat
// inventory (held excludes expired holds still awaiting the sweeper).
type TripExtras struct {
	TotalSeats  int
	BookedSeats int
	HeldSeats   int
	Route       RouteRef
	Bus         BusRef
}

func ids[T any](items []T, id func(T) string) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, id(it))
	}
	return out
}

func TripIDs(trips []*domain.Trip) []string {
	return ids(trips, func(t *domain.Trip) string { return t.ID })
}
func BusIDs(buses []*domain.Bus) []string {
	return ids(buses, func(b *domain.Bus) string { return b.ID })
}
func RouteIDs(routes []*domain.Route) []string {
	return ids(routes, func(r *domain.Route) string { return r.ID })
}

// Trips returns extras keyed by trip id for the given trips.
func (s *ReadModelService) Trips(ctx context.Context, claims *auth.Claims, tripIDs []string) (map[string]TripExtras, error) {
	out := make(map[string]TripExtras, len(tripIDs))
	if len(tripIDs) == 0 {
		return out, nil
	}
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT t.id::text, r.id::text, r.origin, r.destination,
			       b.id::text, b.registration_no, b.bus_class,
			       COALESCE(s.total, 0), COALESCE(s.booked, 0), COALESCE(s.held, 0)
			FROM fleet.trips t
			JOIN fleet.routes r ON r.id = t.route_id
			JOIN fleet.buses b ON b.id = t.bus_id
			LEFT JOIN LATERAL (
			    SELECT count(*)::int AS total,
			           (count(*) FILTER (WHERE status = 'booked'))::int AS booked,
			           (count(*) FILTER (WHERE status = 'held'
			                             AND (hold_expires_at IS NULL OR hold_expires_at > now())))::int AS held
			    FROM fleet.trip_seat_inventory WHERE trip_id = t.id
			) s ON true
			WHERE t.id IN (SELECT unnest($1::text[])::uuid)`, tripIDs)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var e TripExtras
			if err := rows.Scan(&id, &e.Route.ID, &e.Route.Origin, &e.Route.Destination,
				&e.Bus.ID, &e.Bus.RegistrationNo, &e.Bus.BusClass,
				&e.TotalSeats, &e.BookedSeats, &e.HeldSeats); err != nil {
				return err
			}
			out[id] = e
		}
		return rows.Err()
	})
	return out, err
}

type BusExtras struct {
	TotalSeats int
	HasSeatMap bool
}

// Buses returns extras keyed by bus id. A bus with no seat map (or an empty
// one) reports 0 seats and hasSeatMap=false.
func (s *ReadModelService) Buses(ctx context.Context, claims *auth.Claims, busIDs []string) (map[string]BusExtras, error) {
	out := make(map[string]BusExtras, len(busIDs))
	if len(busIDs) == 0 {
		return out, nil
	}
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT b.id::text, COALESCE(sm.total_seats, 0)
			FROM fleet.buses b
			LEFT JOIN fleet.seat_maps sm ON sm.bus_id = b.id
			WHERE b.id IN (SELECT unnest($1::text[])::uuid)`, busIDs)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var total int
			if err := rows.Scan(&id, &total); err != nil {
				return err
			}
			out[id] = BusExtras{TotalSeats: total, HasSeatMap: total > 0}
		}
		return rows.Err()
	})
	return out, err
}

type RouteExtras struct {
	ScheduledTripCount int
	NextDepartureAt    *time.Time
}

// Routes returns extras keyed by route id. "Scheduled" means status
// scheduled and departing in the future.
func (s *ReadModelService) Routes(ctx context.Context, claims *auth.Claims, routeIDs []string) (map[string]RouteExtras, error) {
	out := make(map[string]RouteExtras, len(routeIDs))
	if len(routeIDs) == 0 {
		return out, nil
	}
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT r.id::text, count(t.id)::int, min(t.departure_at)
			FROM fleet.routes r
			LEFT JOIN fleet.trips t ON t.route_id = r.id
			     AND t.status = 'scheduled' AND t.departure_at >= now()
			WHERE r.id IN (SELECT unnest($1::text[])::uuid)
			GROUP BY r.id`, routeIDs)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var e RouteExtras
			if err := rows.Scan(&id, &e.ScheduledTripCount, &e.NextDepartureAt); err != nil {
				return err
			}
			out[id] = e
		}
		return rows.Err()
	})
	return out, err
}

type RouteBus struct {
	BusID           string
	RegistrationNo  string
	UpcomingTrips   int
	NextDepartureAt time.Time
}

// BusesOnRoute lists the buses that have upcoming scheduled trips on a
// route, soonest departure first.
func (s *ReadModelService) BusesOnRoute(ctx context.Context, claims *auth.Claims, routeID string) ([]RouteBus, error) {
	var out []RouteBus
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM fleet.routes WHERE id = $1::text::uuid)`, routeID).Scan(&exists); err != nil {
			return domain.ErrRouteNotFound
		}
		if !exists {
			return domain.ErrRouteNotFound
		}
		rows, err := tx.Query(ctx, `
			SELECT b.id::text, b.registration_no, count(*)::int, min(t.departure_at)
			FROM fleet.trips t JOIN fleet.buses b ON b.id = t.bus_id
			WHERE t.route_id = $1::text::uuid AND t.status = 'scheduled' AND t.departure_at >= now()
			GROUP BY b.id, b.registration_no
			ORDER BY min(t.departure_at), b.id`, routeID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var rb RouteBus
			if err := rows.Scan(&rb.BusID, &rb.RegistrationNo, &rb.UpcomingTrips, &rb.NextDepartureAt); err != nil {
				return err
			}
			out = append(out, rb)
		}
		return rows.Err()
	})
	return out, err
}
