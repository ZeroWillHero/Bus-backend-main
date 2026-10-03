// Package demoseed fills a database with one realistic demo operator — a
// month of past trips, a week of upcoming ones, bookings, check-ins and
// payouts — so the dashboard, analytics and activity endpoints have
// something to show. Everything it creates hangs off an operator whose
// business_reg_no starts with MarkerPrefix, which is how Reset finds it again.
package demoseed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	mrand "math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/platform/auth"
)

// MarkerPrefix tags every demo operator; Reset deletes only these.
const MarkerPrefix = "DEMO-SEED-"

const (
	AdminEmail = "demo.admin@demo.slbus.lk"
	StaffEmail = "demo.staff@demo.slbus.lk"
	// Demo passengers use phones in this reserved-looking range.
	passengerPhonePrefix = "+9470000"
)

var colombo = mustLoc("Asia/Colombo")

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

type Options struct {
	// Password for the demo admin and staff logins.
	Password string
	// Now is the reference "current time"; zero means time.Now().
	Now time.Time
	// Seed makes the random data reproducible.
	Seed int64
}

// Summary reports what was created.
type Summary struct {
	OperatorID                                    string
	Buses, Routes, Trips, Bookings, Tickets, Paid int
	CheckedIn, Payouts                            int
}

type busSpec struct {
	reg, class string
	rows       int // 4 seats per row; 0 = no seat map
	active     bool
}

var buses = []busSpec{
	{"NB-1001", "luxury", 10, true},
	{"NB-1002", "semi_luxury", 11, true},
	{"NB-1003", "normal", 13, true},
	{"NB-1004", "super_luxury", 8, true},
	{"NB-1005", "luxury", 0, true}, // no seat map: triggers the missing_seat_map alert
	{"NB-1006", "normal", 12, false},
}

var routes = []struct {
	origin, destination string
	km                  float64
}{
	{"Colombo", "Kandy", 115},
	{"Colombo", "Galle", 126},
	{"Colombo", "Jaffna", 396},
	{"Kandy", "Nuwara Eliya", 77}, // no trips: triggers route_without_trips
}

// slot is a daily departure: route index, bus index, local time, base fare.
var slots = []struct {
	route, bus int
	hour, min  int
	fare       float64
}{
	{0, 0, 6, 0, 900},
	{0, 1, 14, 0, 850},
	{1, 2, 7, 30, 750},
	{1, 3, 16, 0, 1500},
	{2, 1, 20, 30, 2400},
}

var firstNames = []string{"Nimal", "Kamal", "Sunil", "Amara", "Dilani", "Ruwan", "Tharindu", "Nadeesha", "Kasun", "Ishara", "Sanduni", "Chamara", "Priya", "Mahesh", "Lakmal", "Anoma", "Rohan", "Sithara", "Gayan", "Malini"}
var lastNames = []string{"Perera", "Fernando", "Silva", "Jayasinghe", "Wijesinghe", "Bandara", "Gunawardena", "Rajapaksa", "Dissanayake", "Herath", "Kumar", "Ratnayake"}

type seatInfo struct {
	number, typ string
	row, col    int
}

func seatLayout(b busSpec) []seatInfo {
	var out []seatInfo
	letters := []string{"A", "B", "C", "D"}
	cols := []int{1, 2, 4, 5} // gap at col 3 = the aisle
	for r := 1; r <= b.rows; r++ {
		for i, l := range letters {
			typ := "normal"
			switch {
			case b.class == "super_luxury":
				typ = "luxury"
			case l == "A" || l == "D":
				typ = "window"
			}
			out = append(out, seatInfo{fmt.Sprintf("%d%s", r, l), typ, r, cols[i]})
		}
	}
	return out
}

func fareFor(base float64, seatType string) float64 {
	switch seatType {
	case "window":
		return base + 50
	case "luxury":
		return base * 1.4
	}
	return base
}

// Reset removes everything a previous Seed created. It needs a role allowed
// to DELETE from booking tables (the migration superuser, not slbus_app).
func Reset(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := reset(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func reset(ctx context.Context, tx pgx.Tx) error {
	const ops = `(SELECT id FROM ops.operators WHERE business_reg_no LIKE '` + MarkerPrefix + `%')`
	const tripsOf = `(SELECT id FROM fleet.trips WHERE operator_id IN ` + ops + `)`
	const bookingsOf = `(SELECT id FROM booking.bookings WHERE trip_id IN ` + tripsOf + `)`
	stmts := []string{
		`DELETE FROM booking.tickets WHERE booking_id IN ` + bookingsOf,
		`DELETE FROM booking.booking_seats WHERE booking_id IN ` + bookingsOf,
		`DELETE FROM booking.bookings WHERE trip_id IN ` + tripsOf,
		`DELETE FROM finance.payouts WHERE operator_id IN ` + ops,
		`DELETE FROM fleet.trips WHERE operator_id IN ` + ops, // cascades fares and seat inventory
		`DELETE FROM fleet.routes WHERE operator_id IN ` + ops,
		`DELETE FROM fleet.buses WHERE operator_id IN ` + ops, // cascades seat maps and seats
		`DELETE FROM identity.users WHERE operator_id IN ` + ops + ` OR email IN ('` + AdminEmail + `','` + StaffEmail + `') OR phone LIKE '` + passengerPhonePrefix + `%'`,
		`DELETE FROM ops.operators WHERE business_reg_no LIKE '` + MarkerPrefix + `%'`,
	}
	for _, s := range stmts {
		if _, err := tx.Exec(ctx, s); err != nil {
			return fmt.Errorf("reset: %w (statement: %.80s...)", err, s)
		}
	}
	return nil
}

// Seed replaces any previous demo data with a fresh set, in one transaction.
func Seed(ctx context.Context, pool *pgxpool.Pool, opt Options) (*Summary, error) {
	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	rng := mrand.New(mrand.NewSource(opt.Seed))
	hash, err := auth.HashPassword(opt.Password)
	if err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := reset(ctx, tx); err != nil {
		return nil, err
	}

	b := &pgx.Batch{}
	q := func(sql string, args ...any) { b.Queue(sql, args...) }
	// Booking-side rows reference trips by foreign key but are generated
	// while a trip is being built, so they queue separately and run after
	// every trip insert.
	bb := &pgx.Batch{}
	bq := func(sql string, args ...any) { bb.Queue(sql, args...) }
	sum := &Summary{OperatorID: uuid.NewString()}
	opID := sum.OperatorID

	// ---- operators and logins
	q(`INSERT INTO ops.operators (id, name, business_reg_no, status, commission_rate, contact_email, contact_phone, created_at, approved_at)
	   VALUES ($1,'Demo Express (Pvt) Ltd',$2,'approved',0.10,$3,'+94112345678',$4,$5)`,
		opID, MarkerPrefix+"0001", AdminEmail, now.AddDate(0, 0, -61), now.AddDate(0, 0, -60))
	for i, name := range []string{"Lanka Travels", "Island Coaches"} { // pending approvals for the admin Home
		q(`INSERT INTO ops.operators (id, name, business_reg_no, status, contact_email, created_at)
		   VALUES ($1,$2,$3,'pending',$4,$5)`,
			uuid.NewString(), name, fmt.Sprintf("%sPENDING-%d", MarkerPrefix, i+1), fmt.Sprintf("pending%d@demo.slbus.lk", i+1), now.AddDate(0, 0, -(i+1)))
	}
	q(`INSERT INTO identity.users (id, email, password_hash, role, operator_id) VALUES ($1,$2,$3,'operator_admin',$4)`,
		uuid.NewString(), AdminEmail, hash, opID)
	q(`INSERT INTO identity.users (id, email, password_hash, role, operator_id) VALUES ($1,$2,$3,'operator_staff',$4)`,
		uuid.NewString(), StaffEmail, hash, opID)
	var passengers []string
	for i := 0; i < 25; i++ {
		id := uuid.NewString()
		passengers = append(passengers, id)
		q(`INSERT INTO identity.users (id, phone, role) VALUES ($1,$2,'passenger')`, id, fmt.Sprintf("%s%03d", passengerPhonePrefix, i))
	}

	// ---- routes, buses, seat maps
	routeIDs := make([]string, len(routes))
	for i, r := range routes {
		routeIDs[i] = uuid.NewString()
		q(`INSERT INTO fleet.routes (id, operator_id, origin, destination, distance_km) VALUES ($1,$2,$3,$4,$5)`,
			routeIDs[i], opID, r.origin, r.destination, r.km)
	}
	busIDs := make([]string, len(buses))
	layouts := make([][]seatInfo, len(buses))
	for i, bs := range buses {
		busIDs[i] = uuid.NewString()
		q(`INSERT INTO fleet.buses (id, operator_id, registration_no, bus_class, amenities, active) VALUES ($1,$2,$3,$4,$5,$6)`,
			busIDs[i], opID, bs.reg, bs.class, `{"ac": true, "wifi": true, "charging": true}`, bs.active)
		layouts[i] = seatLayout(bs)
		if bs.rows == 0 {
			continue
		}
		mapID := uuid.NewString()
		q(`INSERT INTO fleet.seat_maps (id, bus_id, total_seats) VALUES ($1,$2,$3)`, mapID, busIDs[i], len(layouts[i]))
		for _, s := range layouts[i] {
			q(`INSERT INTO fleet.seats (seat_map_id, seat_number, seat_type, row_no, col_no) VALUES ($1,$2,$3,$4,$5)`,
				mapID, s.number, s.typ, s.row, s.col)
		}
	}
	sum.Buses, sum.Routes = len(buses), len(routes)

	// ---- trips, inventory, bookings, tickets
	today := now.In(colombo)
	lowOccupancyDone := false
	heldTrips := 0
	for day := -29; day <= 6; day++ {
		for _, sl := range slots {
			dep := time.Date(today.Year(), today.Month(), today.Day()+day, sl.hour, sl.min, 0, 0, colombo).UTC()
			layout := layouts[sl.bus]
			past := dep.Before(now)

			status := "scheduled"
			if past {
				status = "completed"
				if rng.Float64() < 0.08 {
					status = "cancelled"
				}
			}
			createdAt := dep.AddDate(0, 0, -7)
			if createdAt.After(now) {
				createdAt = now.Add(-time.Duration(1+rng.Intn(240)) * time.Hour)
			}
			updatedAt := createdAt
			if status == "cancelled" {
				updatedAt = dep.Add(-24 * time.Hour)
			}

			// How full: busy in the past, quieter further out, and one
			// deliberately empty trip in the next 24h for the alert.
			fill := 0.55 + rng.Float64()*0.30
			if !past {
				fill = 0.10 + rng.Float64()*0.35
				if dep.Sub(now) < 36*time.Hour {
					fill = 0.30 + rng.Float64()*0.40
				}
				if !lowOccupancyDone && dep.Sub(now) < 24*time.Hour {
					fill, lowOccupancyDone = 0.12, true
				}
			}
			if status == "cancelled" {
				fill = 0
			}
			wantSeats := int(fill * float64(len(layout)))

			type seatState struct {
				seatInfo
				status, bookingID string
				holdExpires       *time.Time
			}
			seats := make([]seatState, len(layout))
			for i, s := range layout {
				seats[i] = seatState{seatInfo: s, status: "available"}
			}
			order := rng.Perm(len(seats))

			tripID := uuid.NewString()
			booked, held := 0, 0

			makeBooking := func(idx []int, bstatus string, confirmedAt, cancelledAt, holdExpires *time.Time, createdAt time.Time) {
				bookingID := uuid.NewString()
				var total float64
				for _, i := range idx {
					total += fareFor(sl.fare, seats[i].typ)
				}
				bq(`INSERT INTO booking.bookings (id, trip_id, passenger_id, status, total_amount, hold_expires_at, created_at, updated_at, confirmed_at, cancelled_at)
				   VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
					bookingID, tripID, passengers[rng.Intn(len(passengers))], bstatus, total, holdExpires, createdAt, createdAt, confirmedAt, cancelledAt)
				for _, i := range idx {
					bq(`INSERT INTO booking.booking_seats (booking_id, seat_number, passenger_name, fare_amount) VALUES ($1,$2,$3,$4)`,
						bookingID, seats[i].number, firstNames[rng.Intn(len(firstNames))]+" "+lastNames[rng.Intn(len(lastNames))], fareFor(sl.fare, seats[i].typ))
					if bstatus == "confirmed" {
						seats[i].status, seats[i].bookingID = "booked", bookingID
					} else if bstatus == "held" {
						seats[i].status, seats[i].bookingID, seats[i].holdExpires = "held", bookingID, holdExpires
					}
				}
				if bstatus == "confirmed" {
					sum.Bookings++
					booked += len(idx)
					tok := make([]byte, 16)
					_, _ = rand.Read(tok)
					var checkedIn *time.Time
					if past && rng.Float64() < 0.85 {
						t := dep.Add(-time.Duration(5+rng.Intn(35)) * time.Minute)
						checkedIn = &t
						sum.CheckedIn++
					}
					bq(`INSERT INTO booking.tickets (booking_id, qr_token, issued_at, checked_in_at) VALUES ($1,$2,$3,$4)`,
						bookingID, hex.EncodeToString(tok), *confirmedAt, checkedIn)
					sum.Tickets++
				}
			}

			cursor := 0
			for booked < wantSeats && cursor < len(order) {
				n := 1 + rng.Intn(3)
				if cursor+n > len(order) {
					n = len(order) - cursor
				}
				idx := order[cursor : cursor+n]
				cursor += n

				var confirmed time.Time
				if past {
					confirmed = dep.Add(-time.Duration(2+rng.Intn(94)) * time.Hour)
				} else {
					confirmed = now.Add(-time.Duration(rng.Intn(30*60)) * time.Minute)
				}
				if confirmed.After(now) {
					confirmed = now.Add(-time.Minute)
				}
				makeBooking(idx, "confirmed", &confirmed, nil, nil, confirmed.Add(-time.Duration(1+rng.Intn(9))*time.Minute))
				sum.Paid++
			}

			// Abandoned checkouts: cancelled holds that never paid.
			if status != "cancelled" && rng.Float64() < 0.5 && cursor < len(order) {
				cancelled := dep.Add(-time.Duration(3+rng.Intn(48)) * time.Hour)
				if cancelled.After(now) {
					cancelled = now.Add(-time.Hour)
				}
				makeBooking([]int{order[len(order)-1]}, "cancelled", nil, &cancelled, nil, cancelled.Add(-12*time.Minute))
				// the seat stays free: cancelled holds release it
				seats[order[len(order)-1]].status, seats[order[len(order)-1]].bookingID = "available", ""
			}
			// A couple of live holds on the nearest upcoming trips.
			if !past && heldTrips < 2 && cursor+2 <= len(order) && status == "scheduled" {
				exp := now.Add(8 * time.Minute)
				makeBooking(order[len(order)-3:len(order)-1], "held", nil, nil, &exp, now.Add(-2*time.Minute))
				heldTrips++
			}

			for _, s := range seats {
				if s.status == "held" {
					held++
				}
			}
			q(`INSERT INTO fleet.trips (id, operator_id, route_id, bus_id, departure_at, status, base_fare, available_seats, created_at, updated_at)
			   VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
				tripID, opID, routeIDs[sl.route], busIDs[sl.bus], dep, status, sl.fare, len(seats)-booked-held, createdAt, updatedAt)
			for _, typ := range []string{"normal", "window", "luxury"} {
				seen := false
				for _, s := range seats {
					if s.typ == typ {
						seen = true
						break
					}
				}
				if seen {
					q(`INSERT INTO fleet.trip_fares (trip_id, seat_type, fare) VALUES ($1,$2,$3)`, tripID, typ, fareFor(sl.fare, typ))
				}
			}
			for _, s := range seats {
				var bid any
				if s.bookingID != "" {
					bid = s.bookingID
				}
				q(`INSERT INTO fleet.trip_seat_inventory (trip_id, seat_number, status, booking_id, hold_expires_at) VALUES ($1,$2,$3,$4,$5)`,
					tripID, s.number, s.status, bid, s.holdExpires)
			}
			sum.Trips++
		}
	}

	// ---- payouts (tickets are already in; these feed the activity feed)
	for _, p := range []struct {
		daysAgoStart, daysAgoEnd int
		status                   string
	}{{58, 31, "executed"}, {30, 17, "executed"}, {16, 9, "initiated"}} {
		amount := 380000 + rng.Intn(220000)
		initiated := now.AddDate(0, 0, -(p.daysAgoEnd - 2))
		var executed any
		var ref any
		if p.status == "executed" {
			executed = initiated.Add(26 * time.Hour)
			ref = fmt.Sprintf("BNK%08d", rng.Intn(100000000))
		}
		q(`INSERT INTO finance.payouts (operator_id, period_start, period_end, amount, status, bank_txn_ref, created_at, initiated_at, executed_at)
		   VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			opID, now.AddDate(0, 0, -p.daysAgoStart).Format("2006-01-02"), now.AddDate(0, 0, -p.daysAgoEnd).Format("2006-01-02"),
			amount, p.status, ref, initiated.Add(-time.Hour), initiated, executed)
		sum.Payouts++
	}

	b.QueuedQueries = append(b.QueuedQueries, bb.QueuedQueries...)
	res := tx.SendBatch(ctx, b)
	for i := 0; i < b.Len(); i++ {
		if _, err := res.Exec(); err != nil {
			_ = res.Close()
			return nil, fmt.Errorf("seed statement %d: %w", i, err)
		}
	}
	if err := res.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return sum, nil
}
