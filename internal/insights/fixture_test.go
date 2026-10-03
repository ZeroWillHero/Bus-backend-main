package insights_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/platform/testutil"
)

// fixedNow is 11:30 on 2 Oct 2026 in Asia/Colombo (UTC+5:30).
var fixedNow = time.Date(2026, 10, 2, 6, 0, 0, 0, time.UTC)

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

type fixture struct {
	pool                *pgxpool.Pool
	OperatorID          string
	OtherOperatorID     string
	RouteID, EmptyRoute string
	BusID, BareBusID    string
	TripToday           string // 10:00Z 2 Oct, 4 seats: 1 booked, 1 held, 2 free
	TripCancelled       string // 2 Oct, cancelled
	TripTomorrow        string // 3 Oct 05:00Z, no inventory
	BookingConfirmed    string // 3600, 2 seats, confirmed 03:00Z 2 Oct
	BookingNightOwl     string // 1000, 1 seat, confirmed 20:00Z 1 Oct = 01:30 on 2 Oct Colombo
	BookingRefunded     string // 500, 1 seat, confirmed 10:00Z 1 Oct, cancelled 02:00Z 2 Oct
	BookingHeld         string // 999, never confirmed
}

// seedFixture builds one operator with known numbers, plus a second operator
// whose data must never leak into the first's results.
func seedFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testutil.NewTestPool(t)
	ctx := context.Background()
	f := &fixture{pool: pool, OperatorID: testutil.SeedOperator(t, pool), OtherOperatorID: testutil.SeedOperator(t, pool)}

	one := func(what, q string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, q, args...).Scan(&id); err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
		return id
	}
	exec := func(what, q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}

	f.RouteID = one("route", `INSERT INTO fleet.routes (operator_id, origin, destination) VALUES ($1,'Colombo','Kandy') RETURNING id`, f.OperatorID)
	f.EmptyRoute = one("empty route", `INSERT INTO fleet.routes (operator_id, origin, destination) VALUES ($1,'Galle','Matara') RETURNING id`, f.OperatorID)
	f.BusID = one("bus", `INSERT INTO fleet.buses (operator_id, registration_no, bus_class) VALUES ($1,'NB-'||substr(gen_random_uuid()::text,1,6),'luxury') RETURNING id`, f.OperatorID)
	f.BareBusID = one("bare bus", `INSERT INTO fleet.buses (operator_id, registration_no, bus_class) VALUES ($1,'NC-'||substr(gen_random_uuid()::text,1,6),'normal') RETURNING id`, f.OperatorID)
	exec("seat map", `INSERT INTO fleet.seat_maps (bus_id, total_seats) VALUES ($1, 4)`, f.BusID)

	trip := func(what, route, bus, departure, status string) string {
		return one(what, `INSERT INTO fleet.trips (operator_id, route_id, bus_id, departure_at, status, base_fare, available_seats)
			VALUES ($1,$2,$3,$4,$5,1000,0) RETURNING id`, f.OperatorID, route, bus, utc(departure), status)
	}
	f.TripToday = trip("trip today", f.RouteID, f.BusID, "2026-10-02T10:00:00Z", "scheduled")
	f.TripCancelled = trip("trip cancelled", f.RouteID, f.BusID, "2026-10-02T04:00:00Z", "cancelled")
	f.TripTomorrow = trip("trip tomorrow", f.RouteID, f.BusID, "2026-10-03T05:00:00Z", "scheduled")

	inv := func(tripID, seat, status string) {
		exec("inventory", `INSERT INTO fleet.trip_seat_inventory (trip_id, seat_number, status, hold_expires_at)
			VALUES ($1,$2,$3, CASE WHEN $3 = 'held' THEN '2100-01-01'::timestamptz END)`, tripID, seat, status)
	}
	for seat, status := range map[string]string{"A1": "booked", "A2": "held", "A3": "available", "A4": "available"} {
		inv(f.TripToday, seat, status)
	}
	for _, seat := range []string{"A1", "A2", "A3", "A4"} {
		inv(f.TripCancelled, seat, "booked") // must not count: trip is cancelled
	}

	passenger := testutil.SeedPassenger(t, pool)
	booking := func(what, tripID, status string, amount float64, confirmed, cancelled *time.Time, seats ...string) string {
		id := one(what, `INSERT INTO booking.bookings (trip_id, passenger_id, status, total_amount, confirmed_at, cancelled_at)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, tripID, passenger, status, amount, confirmed, cancelled)
		for _, s := range seats {
			exec("booking seat", `INSERT INTO booking.booking_seats (booking_id, seat_number, passenger_name, fare_amount)
				VALUES ($1,$2,'P',$3)`, id, s, amount/float64(len(seats)))
		}
		return id
	}
	at := func(s string) *time.Time { v := utc(s); return &v }
	f.BookingConfirmed = booking("confirmed", f.TripToday, "confirmed", 3600, at("2026-10-02T03:00:00Z"), nil, "A1", "B1")
	f.BookingNightOwl = booking("night owl", f.TripToday, "confirmed", 1000, at("2026-10-01T20:00:00Z"), nil, "C1")
	f.BookingRefunded = booking("refunded", f.TripToday, "cancelled", 500, at("2026-10-01T10:00:00Z"), at("2026-10-02T02:00:00Z"), "D1")
	f.BookingHeld = booking("held", f.TripToday, "held", 999, nil, nil, "E1")
	exec("ticket", `INSERT INTO booking.tickets (booking_id, qr_token, checked_in_at) VALUES ($1, 'qr-'||gen_random_uuid()::text, $2)`,
		f.BookingConfirmed, utc("2026-10-02T05:00:00Z"))

	// Another operator with loud numbers that must never show up above.
	otherRoute := one("other route", `INSERT INTO fleet.routes (operator_id, origin, destination) VALUES ($1,'Jaffna','Vavuniya') RETURNING id`, f.OtherOperatorID)
	otherBus := one("other bus", `INSERT INTO fleet.buses (operator_id, registration_no, bus_class) VALUES ($1,'ZZ-'||substr(gen_random_uuid()::text,1,6),'luxury') RETURNING id`, f.OtherOperatorID)
	otherTrip := one("other trip", `INSERT INTO fleet.trips (operator_id, route_id, bus_id, departure_at, base_fare, available_seats)
		VALUES ($1,$2,$3,$4,1000,0) RETURNING id`, f.OtherOperatorID, otherRoute, otherBus, utc("2026-10-02T09:00:00Z"))
	booking("other booking", otherTrip, "confirmed", 777000, at("2026-10-02T03:00:00Z"), nil, "A1")
	return f
}
