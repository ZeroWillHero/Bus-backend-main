package insights

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sl-bus/backend/internal/platform/auth"
)

// lowOccupancyThreshold and lowOccupancyWindow define the "needs attention"
// rule: a scheduled trip departing within the window that is less than this
// full. They live here (not in the app) so they can become operator settings.
const (
	lowOccupancyThreshold = 0.30
	lowOccupancyWindow    = 24 * time.Hour
	maxAlertsPerType      = 10
)

type Dashboard struct {
	Currency    string         `json:"currency"`
	GeneratedAt time.Time      `json:"generatedAt"`
	Fleet       DashboardFleet `json:"fleet"`
	Today       DashboardToday `json:"today"`
	Alerts      []Alert        `json:"alerts"`
}

type DashboardFleet struct {
	ActiveBuses                int `json:"activeBuses"`
	TotalBuses                 int `json:"totalBuses"`
	BusesWithoutSeatMap        int `json:"busesWithoutSeatMap"`
	Routes                     int `json:"routes"`
	RoutesWithoutScheduledTrip int `json:"routesWithoutScheduledTrips"`
	UpcomingTrips              int `json:"upcomingTrips"`
}

type DashboardToday struct {
	Date          string         `json:"date"`
	Trips         int            `json:"trips"`
	CancelledTrip int            `json:"cancelledTrips"`
	SeatsSold     int            `json:"seatsSold"`
	SeatsHeld     int            `json:"seatsHeld"`
	SeatCapacity  int            `json:"seatCapacity"`
	NextDeparture *NextDeparture `json:"nextDeparture"`
}

type NextDeparture struct {
	TripID            string    `json:"tripId"`
	DepartureAt       time.Time `json:"departureAt"`
	RouteName         string    `json:"routeName"`
	BusRegistrationNo string    `json:"busRegistrationNo"`
}

// Alert type values are a stable enum the app switches on.
const (
	AlertLowOccupancy      = "low_occupancy"
	AlertMissingSeatMap    = "missing_seat_map"
	AlertRouteWithoutTrips = "route_without_trips"
)

type Alert struct {
	Type      string   `json:"type"`
	Severity  string   `json:"severity"`
	TripID    *string  `json:"tripId,omitempty"`
	BusID     *string  `json:"busId,omitempty"`
	RouteID   *string  `json:"routeId,omitempty"`
	Message   string   `json:"message"`
	Occupancy *float64 `json:"occupancy,omitempty"`
}

func routeName(origin, destination string) string { return origin + " → " + destination }

// Dashboard builds the R1 snapshot for one operator.
func (s *Service) Dashboard(ctx context.Context, claims *auth.Claims, operatorID, tz string) (*Dashboard, error) {
	opID, err := resolveOperator(claims, operatorID)
	if err != nil {
		return nil, err
	}
	loc, err := loadZone(tz)
	if err != nil {
		return nil, err
	}

	now := s.now().UTC()
	localNow := now.In(loc)
	todayStart := dayStart(localNow, loc)
	todayEnd := dayStart(localNow.AddDate(0, 0, 1), loc)

	out := &Dashboard{
		Currency:    Currency,
		GeneratedAt: now,
		Today:       DashboardToday{Date: localNow.Format(dateLayout)},
		Alerts:      []Alert{},
	}

	err = s.withTx(ctx, claims, func(ctx context.Context, tx pgx.Tx) error {
		f := &out.Fleet
		if err := tx.QueryRow(ctx, `
			SELECT
			  (SELECT count(*) FROM fleet.buses WHERE operator_id = $1),
			  (SELECT count(*) FROM fleet.buses WHERE operator_id = $1 AND active),
			  (SELECT count(*) FROM fleet.buses b WHERE b.operator_id = $1 AND b.active
			     AND NOT EXISTS (SELECT 1 FROM fleet.seat_maps sm WHERE sm.bus_id = b.id AND sm.total_seats > 0)),
			  (SELECT count(*) FROM fleet.routes WHERE operator_id = $1),
			  (SELECT count(*) FROM fleet.routes r WHERE r.operator_id = $1
			     AND NOT EXISTS (SELECT 1 FROM fleet.trips t WHERE t.route_id = r.id
			                     AND t.status = 'scheduled' AND t.departure_at >= $2)),
			  (SELECT count(*) FROM fleet.trips
			     WHERE operator_id = $1 AND status = 'scheduled' AND departure_at >= $2)`,
			opID, now,
		).Scan(&f.TotalBuses, &f.ActiveBuses, &f.BusesWithoutSeatMap, &f.Routes, &f.RoutesWithoutScheduledTrip, &f.UpcomingTrips); err != nil {
			return fmt.Errorf("dashboard fleet counts: %w", err)
		}

		t := &out.Today
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE status <> 'cancelled'),
			       count(*) FILTER (WHERE status = 'cancelled')
			FROM fleet.trips
			WHERE operator_id = $1 AND departure_at >= $2 AND departure_at < $3`,
			opID, todayStart, todayEnd,
		).Scan(&t.Trips, &t.CancelledTrip); err != nil {
			return fmt.Errorf("dashboard today trips: %w", err)
		}

		// Sold = booked seats; held (unpaid, unexpired) seats are reported
		// separately and never counted as sold.
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE i.status = 'booked'),
			       count(*) FILTER (WHERE i.status = 'held' AND (i.hold_expires_at IS NULL OR i.hold_expires_at > $4)),
			       count(*)
			FROM fleet.trips tr
			JOIN fleet.trip_seat_inventory i ON i.trip_id = tr.id
			WHERE tr.operator_id = $1 AND tr.status <> 'cancelled'
			  AND tr.departure_at >= $2 AND tr.departure_at < $3`,
			opID, todayStart, todayEnd, now,
		).Scan(&t.SeatsSold, &t.SeatsHeld, &t.SeatCapacity); err != nil {
			return fmt.Errorf("dashboard today seats: %w", err)
		}

		var nd NextDeparture
		var origin, destination string
		err := tx.QueryRow(ctx, `
			SELECT tr.id::text, tr.departure_at, r.origin, r.destination, b.registration_no
			FROM fleet.trips tr
			JOIN fleet.routes r ON r.id = tr.route_id
			JOIN fleet.buses b ON b.id = tr.bus_id
			WHERE tr.operator_id = $1 AND tr.status = 'scheduled'
			  AND tr.departure_at >= $2 AND tr.departure_at < $3
			ORDER BY tr.departure_at, tr.id
			LIMIT 1`,
			opID, now, todayEnd,
		).Scan(&nd.TripID, &nd.DepartureAt, &origin, &destination, &nd.BusRegistrationNo)
		switch {
		case err == nil:
			nd.RouteName = routeName(origin, destination)
			t.NextDeparture = &nd
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("dashboard next departure: %w", err)
		}

		alerts, err := s.loadAlerts(ctx, tx, opID, now, loc)
		if err != nil {
			return err
		}
		out.Alerts = alerts
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) loadAlerts(ctx context.Context, tx pgx.Tx, opID string, now time.Time, loc *time.Location) ([]Alert, error) {
	alerts := []Alert{}

	rows, err := tx.Query(ctx, `
		SELECT tr.id::text, r.origin, r.destination, tr.departure_at,
		       s.booked::float8 / s.total
		FROM fleet.trips tr
		JOIN fleet.routes r ON r.id = tr.route_id
		JOIN LATERAL (
		    SELECT count(*) FILTER (WHERE status = 'booked') AS booked, count(*) AS total
		    FROM fleet.trip_seat_inventory WHERE trip_id = tr.id
		) s ON s.total > 0
		WHERE tr.operator_id = $1 AND tr.status = 'scheduled'
		  AND tr.departure_at >= $2 AND tr.departure_at < $3
		  AND s.booked::float8 / s.total < $4
		ORDER BY tr.departure_at, tr.id
		LIMIT $5`,
		opID, now, now.Add(lowOccupancyWindow), lowOccupancyThreshold, maxAlertsPerType)
	if err != nil {
		return nil, fmt.Errorf("dashboard low occupancy: %w", err)
	}
	for rows.Next() {
		var id, origin, destination string
		var departure time.Time
		var occupancy float64
		if err := rows.Scan(&id, &origin, &destination, &departure, &occupancy); err != nil {
			rows.Close()
			return nil, err
		}
		alerts = append(alerts, Alert{
			Type:     AlertLowOccupancy,
			Severity: "warning",
			TripID:   &id,
			Message: fmt.Sprintf("%s departs %s and is %d%% full",
				routeName(origin, destination), departure.In(loc).Format("15:04"), int(occupancy*100)),
			Occupancy: &occupancy,
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = tx.Query(ctx, `
		SELECT b.id::text, b.registration_no
		FROM fleet.buses b
		WHERE b.operator_id = $1 AND b.active
		  AND NOT EXISTS (SELECT 1 FROM fleet.seat_maps sm WHERE sm.bus_id = b.id AND sm.total_seats > 0)
		ORDER BY b.registration_no
		LIMIT $2`, opID, maxAlertsPerType)
	if err != nil {
		return nil, fmt.Errorf("dashboard missing seat maps: %w", err)
	}
	for rows.Next() {
		var id, reg string
		if err := rows.Scan(&id, &reg); err != nil {
			rows.Close()
			return nil, err
		}
		alerts = append(alerts, Alert{
			Type: AlertMissingSeatMap, Severity: "warning", BusID: &id,
			Message: fmt.Sprintf("Bus %s has no seat map, so it can't be scheduled", reg),
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = tx.Query(ctx, `
		SELECT r.id::text, r.origin, r.destination
		FROM fleet.routes r
		WHERE r.operator_id = $1
		  AND NOT EXISTS (SELECT 1 FROM fleet.trips t WHERE t.route_id = r.id
		                  AND t.status = 'scheduled' AND t.departure_at >= $2)
		ORDER BY r.origin, r.destination, r.id
		LIMIT $3`, opID, now, maxAlertsPerType)
	if err != nil {
		return nil, fmt.Errorf("dashboard routes without trips: %w", err)
	}
	for rows.Next() {
		var id, origin, destination string
		if err := rows.Scan(&id, &origin, &destination); err != nil {
			rows.Close()
			return nil, err
		}
		alerts = append(alerts, Alert{
			Type: AlertRouteWithoutTrips, Severity: "info", RouteID: &id,
			Message: fmt.Sprintf("%s has no scheduled trips", routeName(origin, destination)),
		})
	}
	rows.Close()
	return alerts, rows.Err()
}
