package service_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/sl-bus/backend/internal/booking/domain"
	"github.com/sl-bus/backend/internal/booking/service"
	"github.com/sl-bus/backend/internal/platform/testutil"
)

func (e *env) hold(t *testing.T, trip, passenger string, seats ...string) *domain.Booking {
	t.Helper()
	sels := make([]service.SeatSelection, len(seats))
	for i, s := range seats {
		sels[i] = sel(s, 0)
	}
	b, err := e.svc.CreateHold(e.ctx, service.CreateHoldInput{TripID: trip, PassengerID: passenger, Seats: sels})
	if err != nil {
		t.Fatalf("CreateHold: %v", err)
	}
	return b
}

func (e *env) count(t *testing.T, q string, args ...interface{}) int {
	t.Helper()
	var n int
	e.scan(t, &n, q, args...)
	return n
}

func (e *env) bookingStatus(t *testing.T, id string) string {
	t.Helper()
	var st string
	e.scan(t, &st, `SELECT status FROM booking.bookings WHERE id=$1`, id)
	return st
}

// TestConcurrentConfirmChargesOnce: N simultaneous confirms of one booking
// charge the card exactly once and produce exactly one ticket and one event.
func TestConcurrentConfirmChargesOnce(t *testing.T) {
	e := newEnv(t)
	e.gw.onCharge = func() { time.Sleep(50 * time.Millisecond) } // widen the race window
	trip := e.trip(t, nil, avail("A1", "A2")...)
	p := testutil.SeedPassenger(t, e.pool)
	b := e.hold(t, trip, p, "A1", "A2")

	const n = 10
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = e.svc.ConfirmBooking(e.ctx, p, b.ID, "ref-"+b.ID)
		}(i)
	}
	close(start)
	wg.Wait()

	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, domain.ErrConcurrentUpdate), errors.Is(err, domain.ErrInvalidStatusTransition):
		default:
			t.Errorf("unexpected error from a losing confirm: %v", err)
		}
	}
	if ok != 1 {
		t.Errorf("%d confirms succeeded, want exactly 1", ok)
	}
	if c, _ := e.gw.counts(); c != 1 {
		t.Errorf("gateway charged %d times, want 1 (double charge)", c)
	}
	if got := e.count(t, `SELECT count(*) FROM booking.tickets WHERE booking_id=$1`, b.ID); got != 1 {
		t.Errorf("%d tickets, want 1", got)
	}
	if got := e.count(t, `SELECT count(*) FROM platform.outbox_events WHERE event_type='BookingConfirmed' AND payload->>'booking_id'=$1`, b.ID); got != 1 {
		t.Errorf("%d BookingConfirmed events, want 1", got)
	}
	if st := e.bookingStatus(t, b.ID); st != "confirmed" {
		t.Errorf("status = %q, want confirmed", st)
	}
}

// TestConfirmFailsWhenSeatWasStolen: X's seat hold lapsed and Y now holds the
// seat, while X's booking row still looks unexpired (e.g. app/DB clock skew).
// X's confirm must not charge, confirm, or ticket — and must leave Y's hold alone.
func TestConfirmFailsWhenSeatWasStolen(t *testing.T) {
	e := newEnv(t)
	trip := e.trip(t, nil, avail("A1")...)
	pX, pY := testutil.SeedPassenger(t, e.pool), testutil.SeedPassenger(t, e.pool)
	x := e.hold(t, trip, pX, "A1")

	if _, err := e.pool.Exec(e.ctx, `UPDATE fleet.trip_seat_inventory SET hold_expires_at = now() - interval '1 minute' WHERE trip_id=$1`, trip); err != nil {
		t.Fatal(err)
	}
	if _, err := e.seats.ReleaseExpiredHolds(e.ctx); err != nil {
		t.Fatal(err)
	}
	y := e.hold(t, trip, pY, "A1")

	_, err := e.svc.ConfirmBooking(e.ctx, pX, x.ID, "ref-x")
	if !errors.Is(err, domain.ErrHoldExpired) {
		t.Fatalf("confirm with a stolen seat: want ErrHoldExpired, got %v", err)
	}
	if c, _ := e.gw.counts(); c != 0 {
		t.Errorf("charged %d times for a booking whose seat is gone", c)
	}
	if got := e.count(t, `SELECT count(*) FROM booking.tickets WHERE booking_id=$1`, x.ID); got != 0 {
		t.Errorf("ticket issued for a booking without a seat (oversell)")
	}
	if st := e.bookingStatus(t, x.ID); st == "confirmed" || st == "payment_pending" {
		t.Errorf("X status = %q after failed confirm", st)
	}
	var status, holder string
	e.scan(t, &status, `SELECT status FROM fleet.trip_seat_inventory WHERE trip_id=$1 AND seat_number='A1'`, trip)
	e.scan(t, &holder, `SELECT COALESCE(booking_id::text,'') FROM fleet.trip_seat_inventory WHERE trip_id=$1 AND seat_number='A1'`, trip)
	if status != "held" || holder != y.ID {
		t.Errorf("Y's hold disturbed: status=%q holder=%q", status, holder)
	}
}

// TestConfirmExpiredHoldDoesNotRefund: nothing was charged, so nothing may be
// refunded and no PaidButExpired event may be emitted.
func TestConfirmExpiredHoldDoesNotRefund(t *testing.T) {
	e := newEnv(t)
	trip := e.trip(t, nil, avail("A1")...)
	p := testutil.SeedPassenger(t, e.pool)
	b := e.hold(t, trip, p, "A1")
	if _, err := e.pool.Exec(e.ctx, `UPDATE booking.bookings SET hold_expires_at = now() - interval '1 minute' WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}

	_, err := e.svc.ConfirmBooking(e.ctx, p, b.ID, "ref-exp")
	if !errors.Is(err, domain.ErrHoldExpired) {
		t.Fatalf("want ErrHoldExpired, got %v", err)
	}
	if c, r := e.gw.counts(); c != 0 || r != 0 {
		t.Errorf("charges=%d refunds=%d, want 0/0 (refund of money never taken)", c, r)
	}
	if got := e.count(t, `SELECT count(*) FROM platform.outbox_events WHERE payload->>'booking_id'=$1`, b.ID); got != 0 {
		t.Errorf("%d outbox events for an expired, uncharged booking", got)
	}
	if st := e.bookingStatus(t, b.ID); st != "held" {
		t.Errorf("status = %q, want held", st)
	}
}

// TestConfirmDeclineReturnsToHeld: a declined card leaves the booking payable.
func TestConfirmDeclineReturnsToHeld(t *testing.T) {
	e := newEnv(t)
	trip := e.trip(t, nil, avail("A1")...)
	p := testutil.SeedPassenger(t, e.pool)
	b := e.hold(t, trip, p, "A1")

	e.gw.decline = true
	if _, err := e.svc.ConfirmBooking(e.ctx, p, b.ID, "ref-d"); !errors.Is(err, domain.ErrPaymentDeclined) {
		t.Fatalf("want ErrPaymentDeclined, got %v", err)
	}
	if st := e.bookingStatus(t, b.ID); st != "held" {
		t.Fatalf("status after decline = %q, want held", st)
	}
	e.gw.decline = false
	if _, err := e.svc.ConfirmBooking(e.ctx, p, b.ID, "ref-d2"); err != nil {
		t.Fatalf("retry after decline: %v", err)
	}
	if st := e.bookingStatus(t, b.ID); st != "confirmed" {
		t.Errorf("status after retry = %q, want confirmed", st)
	}
}

// failingSeats makes the confirm transaction fail at the seat step.
type failingSeats struct{}

func (failingSeats) ConfirmSeatsInTx(context.Context, pgx.Tx, string, []string, string) error {
	return domain.ErrSeatUnavailable
}
func (failingSeats) SeatsBooked(context.Context, string, []string) {}

// TestConfirmCommitFailureRefundsAndRollsBack: when the confirm transaction
// fails after a successful charge, nothing is half-written, the charge is
// refunded by its transaction id, and the booking is held again.
func TestConfirmCommitFailureRefundsAndRollsBack(t *testing.T) {
	e := newEnvWith(t, failingSeats{})
	trip := e.trip(t, nil, avail("A1")...)
	p := testutil.SeedPassenger(t, e.pool)
	b := e.hold(t, trip, p, "A1")

	if _, err := e.svc.ConfirmBooking(e.ctx, p, b.ID, "ref-f"); err == nil {
		t.Fatal("confirm succeeded although the transaction failed")
	}
	e.gw.mu.Lock()
	refunds := append([]string(nil), e.gw.refunds...)
	e.gw.mu.Unlock()
	if len(refunds) != 1 || refunds[0] != "txn-ref-f" {
		t.Errorf("refunds = %v, want [txn-ref-f]", refunds)
	}
	if st := e.bookingStatus(t, b.ID); st != "held" {
		t.Errorf("status = %q, want held", st)
	}
	if got := e.count(t, `SELECT count(*) FROM booking.tickets WHERE booking_id=$1`, b.ID); got != 0 {
		t.Errorf("ticket survived a rolled-back confirm")
	}
	if got := e.count(t, `SELECT count(*) FROM platform.outbox_events WHERE payload->>'booking_id'=$1`, b.ID); got != 0 {
		t.Errorf("outbox event survived a rolled-back confirm")
	}
	var seat string
	e.scan(t, &seat, `SELECT status FROM fleet.trip_seat_inventory WHERE trip_id=$1 AND seat_number='A1'`, trip)
	if seat != "held" {
		t.Errorf("seat = %q, want still held", seat)
	}
}

// TestHoldExpiringMidChargeIsNotSwept: a seat hold that lapses while the charge
// is in flight must not be freed by the sweeper (the confirm extends it).
func TestHoldExpiringMidChargeIsNotSwept(t *testing.T) {
	e := newEnv(t)
	trip := e.trip(t, nil, avail("A1")...)
	p := testutil.SeedPassenger(t, e.pool)
	b := e.hold(t, trip, p, "A1")
	if _, err := e.pool.Exec(e.ctx, `UPDATE fleet.trip_seat_inventory SET hold_expires_at = now() + interval '1 second' WHERE trip_id=$1`, trip); err != nil {
		t.Fatal(err)
	}
	e.gw.onCharge = func() {
		time.Sleep(1500 * time.Millisecond)
		if _, err := e.seats.ReleaseExpiredHolds(e.ctx); err != nil {
			t.Errorf("sweep: %v", err)
		}
	}

	if _, err := e.svc.ConfirmBooking(e.ctx, p, b.ID, "ref-s"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	var seat, holder string
	e.scan(t, &seat, `SELECT status FROM fleet.trip_seat_inventory WHERE trip_id=$1 AND seat_number='A1'`, trip)
	e.scan(t, &holder, `SELECT COALESCE(booking_id::text,'') FROM fleet.trip_seat_inventory WHERE trip_id=$1 AND seat_number='A1'`, trip)
	if seat != "booked" || holder != b.ID {
		t.Errorf("seat = %q held by %q, want booked by %s", seat, holder, b.ID)
	}
}

// TestCannotCancelDuringPayment: a booking mid-charge can't be cancelled.
func TestCannotCancelDuringPayment(t *testing.T) {
	e := newEnv(t)
	trip := e.trip(t, nil, avail("A1")...)
	p := testutil.SeedPassenger(t, e.pool)
	b := e.hold(t, trip, p, "A1")
	if _, err := e.pool.Exec(e.ctx, `UPDATE booking.bookings SET status='payment_pending' WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CancelBooking(e.ctx, p, b.ID); !errors.Is(err, domain.ErrInvalidStatusTransition) {
		t.Fatalf("want ErrInvalidStatusTransition, got %v", err)
	}
	var seat string
	e.scan(t, &seat, `SELECT status FROM fleet.trip_seat_inventory WHERE trip_id=$1 AND seat_number='A1'`, trip)
	if seat != "held" {
		t.Errorf("seat = %q, want held (released under an in-flight charge)", seat)
	}
}

// TestCreateHoldSeatCap: more than MaxSeatsPerBooking seats is refused and
// nothing is held.
func TestCreateHoldSeatCap(t *testing.T) {
	e := newEnv(t)
	seats := make([]string, domain.MaxSeatsPerBooking+1)
	for i := range seats {
		seats[i] = fmt.Sprintf("S%02d", i+1)
	}
	trip := e.trip(t, nil, avail(seats...)...)
	p := testutil.SeedPassenger(t, e.pool)
	sels := make([]service.SeatSelection, len(seats))
	for i, s := range seats {
		sels[i] = sel(s, 0)
	}
	if _, err := e.svc.CreateHold(e.ctx, service.CreateHoldInput{TripID: trip, PassengerID: p, Seats: sels}); !errors.Is(err, domain.ErrTooManySeats) {
		t.Fatalf("want ErrTooManySeats, got %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM fleet.trip_seat_inventory WHERE trip_id=$1 AND status <> 'available'`, trip); n != 0 {
		t.Errorf("%d seats held after a refused request", n)
	}
	// Exactly the cap is fine.
	if _, err := e.svc.CreateHold(e.ctx, service.CreateHoldInput{TripID: trip, PassengerID: p, Seats: sels[:domain.MaxSeatsPerBooking]}); err != nil {
		t.Fatalf("hold of %d seats: %v", domain.MaxSeatsPerBooking, err)
	}
}

// TestCreateHoldActiveHoldLimit: a passenger can't keep more than
// MaxActiveHoldsPerPassenger live holds; expired or paid ones don't count.
func TestCreateHoldActiveHoldLimit(t *testing.T) {
	e := newEnv(t)
	trip := e.trip(t, nil, avail("A1", "A2", "A3", "A4", "A5")...)
	p := testutil.SeedPassenger(t, e.pool)
	var first *domain.Booking
	for i, s := range []string{"A1", "A2", "A3"} {
		b := e.hold(t, trip, p, s)
		if i == 0 {
			first = b
		}
	}
	_, err := e.svc.CreateHold(e.ctx, service.CreateHoldInput{TripID: trip, PassengerID: p, Seats: []service.SeatSelection{sel("A4", 0)}})
	if !errors.Is(err, domain.ErrTooManyActiveHolds) {
		t.Fatalf("4th hold: want ErrTooManyActiveHolds, got %v", err)
	}
	// Another passenger is unaffected.
	e.hold(t, trip, testutil.SeedPassenger(t, e.pool), "A5")

	// Paying for one frees a slot.
	if _, err := e.svc.ConfirmBooking(e.ctx, p, first.ID, "ref-lim"); err != nil {
		t.Fatal(err)
	}
	e.hold(t, trip, p, "A4")
}
