package insights

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sl-bus/backend/internal/platform/auth"
)

// Revenue definition (documented once, here, and in api/openapi.yaml):
//
//   - grossRevenue = total_amount of bookings that were confirmed in the
//     period, bucketed by confirmed_at (a booking that is later cancelled
//     still counts here; the cancellation shows up as a refund).
//   - refunds      = total_amount of bookings that had been confirmed and
//     were cancelled in the period, bucketed by cancelled_at. Today the
//     booking state machine only lets a *held* booking be cancelled, so this
//     is always 0.00 until a refund flow exists; the query is ready for it.
//   - netRevenue   = grossRevenue − refunds. Platform commission and
//     gateway fees are NOT deducted; those are finance-ledger concerns.
//   - held (unpaid) bookings never count.
//
// Days are cut on midnight in the requested time zone.

type SeriesPoint struct {
	Date          string `json:"date"`
	GrossRevenue  string `json:"grossRevenue"`
	Refunds       string `json:"refunds"`
	NetRevenue    string `json:"netRevenue"`
	Bookings      int    `json:"bookings"`
	Cancellations int    `json:"cancellations"`
	SeatsSold     int    `json:"seatsSold"`
}

type Series struct {
	Currency    string        `json:"currency"`
	Granularity string        `json:"granularity"`
	Points      []SeriesPoint `json:"points"`
}

// Series returns one zero-filled point per local date in [from, to].
func (s *Service) Series(ctx context.Context, claims *auth.Claims, operatorID, from, to, granularity, tz string) (*Series, error) {
	opID, err := resolveOperator(claims, operatorID)
	if err != nil {
		return nil, err
	}
	loc, err := loadZone(tz)
	if err != nil {
		return nil, err
	}
	if granularity == "" {
		granularity = "day"
	}
	if granularity != "day" {
		return nil, invalid("granularity must be \"day\"")
	}
	rng, err := parseRange(from, to, loc)
	if err != nil {
		return nil, err
	}
	zone := loc.String()

	out := &Series{Currency: Currency, Granularity: granularity, Points: make([]SeriesPoint, 0, rng.days())}
	err = s.withTx(ctx, claims, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			WITH days AS (
			  SELECT d::date AS day FROM generate_series($2::date, $3::date, interval '1 day') d
			),
			conf AS (
			  SELECT (b.confirmed_at AT TIME ZONE $4)::date AS day,
			         sum(b.total_amount) AS gross, count(*) AS bookings,
			         sum((SELECT count(*) FROM booking.booking_seats bs WHERE bs.booking_id = b.id)) AS seats
			  FROM booking.bookings b JOIN fleet.trips t ON t.id = b.trip_id
			  WHERE t.operator_id = $1 AND b.confirmed_at >= $5 AND b.confirmed_at < $6
			  GROUP BY 1
			),
			can AS (
			  SELECT (b.cancelled_at AT TIME ZONE $4)::date AS day,
			         sum(b.total_amount) AS refunds, count(*) AS cancellations
			  FROM booking.bookings b JOIN fleet.trips t ON t.id = b.trip_id
			  WHERE t.operator_id = $1 AND b.confirmed_at IS NOT NULL
			    AND b.cancelled_at >= $5 AND b.cancelled_at < $6
			  GROUP BY 1
			)
			SELECT d.day::text,
			       COALESCE(conf.gross, 0)::numeric(14,2)::text,
			       COALESCE(can.refunds, 0)::numeric(14,2)::text,
			       (COALESCE(conf.gross, 0) - COALESCE(can.refunds, 0))::numeric(14,2)::text,
			       COALESCE(conf.bookings, 0), COALESCE(can.cancellations, 0), COALESCE(conf.seats, 0)
			FROM days d
			LEFT JOIN conf ON conf.day = d.day
			LEFT JOIN can ON can.day = d.day
			ORDER BY d.day`,
			opID, rng.From, rng.To, zone, rng.Start, rng.End)
		if err != nil {
			return fmt.Errorf("analytics series: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var p SeriesPoint
			if err := rows.Scan(&p.Date, &p.GrossRevenue, &p.Refunds, &p.NetRevenue, &p.Bookings, &p.Cancellations, &p.SeatsSold); err != nil {
				return err
			}
			out.Points = append(out.Points, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

type Period struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type Comparison struct {
	Period       Period   `json:"period"`
	GrossRevenue string   `json:"grossRevenue"`
	DeltaPercent *float64 `json:"deltaPercent"`
}

type TopRoute struct {
	RouteID      string `json:"routeId"`
	Name         string `json:"name"`
	GrossRevenue string `json:"grossRevenue"`
	Bookings     int    `json:"bookings"`
}

type Summary struct {
	Currency         string      `json:"currency"`
	Period           Period      `json:"period"`
	GrossRevenue     string      `json:"grossRevenue"`
	NetRevenue       string      `json:"netRevenue"`
	Refunds          string      `json:"refunds"`
	Bookings         int         `json:"bookings"`
	Cancellations    int         `json:"cancellations"`
	CancellationRate float64     `json:"cancellationRate"`
	SeatsSold        int         `json:"seatsSold"`
	AverageOccupancy float64     `json:"averageOccupancy"`
	Comparison       *Comparison `json:"comparison"`
	TopRoutes        []TopRoute  `json:"topRoutes"`
}

// SummaryParams are the raw query values of the summary endpoint.
type SummaryParams struct {
	From, To               string
	CompareFrom, CompareTo string
	Period                 string // "" or "month_to_date"
	TZ                     string
}

func (s *Service) Summary(ctx context.Context, claims *auth.Claims, operatorID string, p SummaryParams) (*Summary, error) {
	opID, err := resolveOperator(claims, operatorID)
	if err != nil {
		return nil, err
	}
	loc, err := loadZone(p.TZ)
	if err != nil {
		return nil, err
	}

	var rng dateRange
	var cmp *dateRange
	switch p.Period {
	case "month_to_date":
		if p.From != "" || p.To != "" {
			return nil, invalid("use either period or from/to, not both")
		}
		today := s.now().In(loc)
		first := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
		day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
		rng = newDateRange(first, day, loc)

		prevFirst := first.AddDate(0, -1, 0)
		prevLast := first.AddDate(0, 0, -1)
		prevTo := prevFirst.AddDate(0, 0, today.Day()-1)
		if prevTo.After(prevLast) {
			prevTo = prevLast
		}
		c := newDateRange(prevFirst, prevTo, loc)
		cmp = &c
	case "":
		rng, err = parseRange(p.From, p.To, loc)
		if err != nil {
			return nil, err
		}
		if (p.CompareFrom == "") != (p.CompareTo == "") {
			return nil, invalid("compareFrom and compareTo must be provided together")
		}
		if p.CompareFrom != "" {
			c, err := parseRange(p.CompareFrom, p.CompareTo, loc)
			if err != nil {
				return nil, err
			}
			cmp = &c
		}
	default:
		return nil, invalid("period must be \"month_to_date\"")
	}

	out := &Summary{Currency: Currency, Period: periodOf(rng), TopRoutes: []TopRoute{}}
	err = s.withTx(ctx, claims, func(ctx context.Context, tx pgx.Tx) error {
		t, err := periodTotals(ctx, tx, opID, rng)
		if err != nil {
			return err
		}
		out.GrossRevenue, out.NetRevenue, out.Refunds = t.gross, t.net, t.refunds
		out.Bookings, out.Cancellations, out.SeatsSold = t.bookings, t.cancellations, t.seats
		if t.bookings > 0 {
			out.CancellationRate = round(float64(t.cancellations)/float64(t.bookings), 4)
		}

		var booked, capacity int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE i.status = 'booked'), count(*)
			FROM fleet.trips tr JOIN fleet.trip_seat_inventory i ON i.trip_id = tr.id
			WHERE tr.operator_id = $1 AND tr.status <> 'cancelled'
			  AND tr.departure_at >= $2 AND tr.departure_at < $3`,
			opID, rng.Start, rng.End).Scan(&booked, &capacity); err != nil {
			return fmt.Errorf("analytics occupancy: %w", err)
		}
		if capacity > 0 {
			out.AverageOccupancy = round(float64(booked)/float64(capacity), 4)
		}

		rows, err := tx.Query(ctx, `
			SELECT r.id::text, r.origin, r.destination,
			       sum(b.total_amount)::numeric(14,2)::text, count(*)
			FROM booking.bookings b
			JOIN fleet.trips t ON t.id = b.trip_id
			JOIN fleet.routes r ON r.id = t.route_id
			WHERE t.operator_id = $1 AND b.confirmed_at >= $2 AND b.confirmed_at < $3
			GROUP BY r.id, r.origin, r.destination
			ORDER BY sum(b.total_amount) DESC, r.id
			LIMIT 5`, opID, rng.Start, rng.End)
		if err != nil {
			return fmt.Errorf("analytics top routes: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var tr TopRoute
			var origin, destination string
			if err := rows.Scan(&tr.RouteID, &origin, &destination, &tr.GrossRevenue, &tr.Bookings); err != nil {
				return err
			}
			tr.Name = routeName(origin, destination)
			out.TopRoutes = append(out.TopRoutes, tr)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		if cmp != nil {
			ct, err := periodTotals(ctx, tx, opID, *cmp)
			if err != nil {
				return err
			}
			out.Comparison = &Comparison{Period: periodOf(*cmp), GrossRevenue: ct.gross, DeltaPercent: deltaPercent(t.gross, ct.gross)}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

type totals struct {
	gross, refunds, net            string
	bookings, cancellations, seats int
}

func periodTotals(ctx context.Context, tx pgx.Tx, opID string, rng dateRange) (totals, error) {
	var t totals
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(amount) FILTER (WHERE conf), 0)::numeric(14,2)::text,
		       COALESCE(sum(amount) FILTER (WHERE can), 0)::numeric(14,2)::text,
		       (COALESCE(sum(amount) FILTER (WHERE conf), 0) - COALESCE(sum(amount) FILTER (WHERE can), 0))::numeric(14,2)::text,
		       count(*) FILTER (WHERE conf),
		       count(*) FILTER (WHERE can),
		       COALESCE(sum(seats) FILTER (WHERE conf), 0)
		FROM (
		  SELECT b.total_amount AS amount,
		         (b.confirmed_at >= $2 AND b.confirmed_at < $3) AS conf,
		         (b.confirmed_at IS NOT NULL AND b.cancelled_at >= $2 AND b.cancelled_at < $3) AS can,
		         (SELECT count(*) FROM booking.booking_seats bs WHERE bs.booking_id = b.id) AS seats
		  FROM booking.bookings b JOIN fleet.trips tr ON tr.id = b.trip_id
		  WHERE tr.operator_id = $1
		    AND ((b.confirmed_at >= $2 AND b.confirmed_at < $3)
		      OR (b.cancelled_at >= $2 AND b.cancelled_at < $3))
		) x`, opID, rng.Start, rng.End,
	).Scan(&t.gross, &t.refunds, &t.net, &t.bookings, &t.cancellations, &t.seats)
	if err != nil {
		return t, fmt.Errorf("analytics totals: %w", err)
	}
	return t, nil
}

// deltaPercent is null (not infinity) when there is nothing to compare to.
func deltaPercent(current, previous string) *float64 {
	cur, err1 := strconv.ParseFloat(current, 64)
	prev, err2 := strconv.ParseFloat(previous, 64)
	if err1 != nil || err2 != nil || prev == 0 {
		return nil
	}
	d := round((cur-prev)/prev*100, 2)
	return &d
}

func round(v float64, places int) float64 {
	p := math.Pow10(places)
	return math.Round(v*p) / p
}

func periodOf(r dateRange) Period {
	return Period{From: r.From.Format(dateLayout), To: r.To.Format(dateLayout)}
}

func parseRange(from, to string, loc *time.Location) (dateRange, error) {
	if from == "" || to == "" {
		return dateRange{}, invalid("from and to are required (YYYY-MM-DD)")
	}
	f, err := parseDate("from", from)
	if err != nil {
		return dateRange{}, err
	}
	t, err := parseDate("to", to)
	if err != nil {
		return dateRange{}, err
	}
	if t.Before(f) {
		return dateRange{}, invalid("to must not be before from")
	}
	r := newDateRange(f, t, loc)
	if r.days() > MaxRangeDays {
		return dateRange{}, ErrRangeTooLarge
	}
	return r, nil
}
