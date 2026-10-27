package service_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/booking/domain"
	bookingrepo "github.com/sl-bus/backend/internal/booking/repository"
	"github.com/sl-bus/backend/internal/booking/service"
	fleetrepo "github.com/sl-bus/backend/internal/fleet/repository"
	"github.com/sl-bus/backend/internal/platform/testutil"
)

// recordingGateway approves everything (unless decline is set) and remembers
// what it was asked to charge and refund. onCharge, if set, runs inside Charge.
type recordingGateway struct {
	mu       sync.Mutex
	charges  []float64
	refunds  []string // transaction ids
	decline  bool
	onCharge func()
}

func (g *recordingGateway) Charge(_ context.Context, amount float64, _, ref string, _ map[string]interface{}) (*domain.PaymentResult, error) {
	if g.onCharge != nil {
		g.onCharge()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.charges = append(g.charges, amount)
	if g.decline {
		return &domain.PaymentResult{Gateway: "test", Approved: false, ErrorMessage: "card declined"}, nil
	}
	return &domain.PaymentResult{Gateway: "test", TransactionID: "txn-" + ref, Approved: true}, nil
}
func (g *recordingGateway) Refund(_ context.Context, txnID string, _ float64, _ string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refunds = append(g.refunds, txnID)
	return nil
}

func (g *recordingGateway) counts() (charges, refunds int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.charges), len(g.refunds)
}

type env struct {
	ctx   context.Context
	pool  *pgxpool.Pool
	svc   *service.CheckoutService
	gw    *recordingGateway
	seats *fleetrepo.SeatInventoryRepository
}

func newEnv(t *testing.T) *env {
	return newEnvWith(t, nil)
}

// newEnvWith lets a test swap the seat side of the confirm transaction.
func newEnvWith(t *testing.T, seatTx bookingrepo.SeatTxConfirmer) *env {
	t.Helper()
	pool := testutil.NewTestPool(t)
	seats := fleetrepo.NewSeatInventoryRepository(pool, nil)
	if seatTx == nil {
		seatTx = seats
	}
	gw := &recordingGateway{}
	outbox := bookingrepo.NewOutboxRepository(pool)
	svc := service.NewCheckoutService(
		bookingrepo.NewBookingRepository(pool),
		bookingrepo.NewBookingSeatRepository(pool),
		bookingrepo.NewTicketRepository(pool),
		seats, seats, gw,
		outbox,
		bookingrepo.NewConfirmationRepository(pool, seatTx, outbox),
		[]byte("test-qr-secret"),
	)
	return &env{ctx: context.Background(), pool: pool, svc: svc, gw: gw, seats: seats}
}

func sel(seat string, clientFare float64) service.SeatSelection {
	return service.SeatSelection{SeatNumber: seat, PassengerName: "Test Passenger", FareAmount: clientFare}
}

func (e *env) trip(t *testing.T, fares map[string]float64, seats ...testutil.SeatSeed) string {
	t.Helper()
	return testutil.SeedTrip(t, e.pool, testutil.TripSeed{AvailableSeats: len(seats), Seats: seats, Fares: fares})
}

func avail(seats ...string) []testutil.SeatSeed {
	out := make([]testutil.SeatSeed, len(seats))
	for i, s := range seats {
		out[i] = testutil.SeatSeed{SeatNumber: s, Status: "available"}
	}
	return out
}

func (e *env) scan(t *testing.T, dst interface{}, q string, args ...interface{}) {
	t.Helper()
	if err := e.pool.QueryRow(e.ctx, q, args...).Scan(dst); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// TestFareTamperingIsIgnored: whatever fare the client claims (cheap, zero,
// negative, huge) the booking total, the stored per-seat fares and the amount
// charged are the server's.
func TestFareTamperingIsIgnored(t *testing.T) {
	for _, clientFare := range []float64{0.01, 0, -500, 1e9, 999.99} {
		t.Run(fmt.Sprintf("client_%v", clientFare), func(t *testing.T) {
			e := newEnv(t)
			trip := e.trip(t, map[string]float64{"standard": 1000}, avail("A1", "A2")...)
			passenger := testutil.SeedPassenger(t, e.pool)

			b, err := e.svc.CreateHold(e.ctx, service.CreateHoldInput{
				TripID: trip, PassengerID: passenger,
				Seats: []service.SeatSelection{sel("A1", clientFare), sel("A2", clientFare)},
			})
			if err != nil {
				t.Fatalf("CreateHold: %v", err)
			}
			if b.TotalAmount != 2000 {
				t.Errorf("TotalAmount = %v, want 2000 (server fare), client sent %v", b.TotalAmount, clientFare)
			}
			var bad int
			e.scan(t, &bad, `SELECT count(*) FROM booking.booking_seats WHERE booking_id=$1 AND fare_amount <> 1000`, b.ID)
			if bad != 0 {
				t.Errorf("%d booking_seats rows carry a client-controlled fare", bad)
			}

			if _, err := e.svc.ConfirmBooking(e.ctx, passenger, b.ID, "pay-"+b.ID); err != nil {
				t.Fatalf("ConfirmBooking: %v", err)
			}
			if len(e.gw.charges) != 1 || e.gw.charges[0] != 2000 {
				t.Errorf("gateway charged %v, want exactly [2000]", e.gw.charges)
			}
		})
	}
}

func TestFareUsesSeatClassPrice(t *testing.T) {
	e := newEnv(t)
	trip := e.trip(t, map[string]float64{"standard": 1000, "vip": 2500},
		testutil.SeatSeed{SeatNumber: "A1", SeatType: "standard"},
		testutil.SeatSeed{SeatNumber: "V1", SeatType: "vip"},
	)
	b, err := e.svc.CreateHold(e.ctx, service.CreateHoldInput{
		TripID: trip, PassengerID: testutil.SeedPassenger(t, e.pool),
		// client claims both are the cheap class
		Seats: []service.SeatSelection{sel("A1", 1000), sel("V1", 1000)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.TotalAmount != 3500 {
		t.Errorf("TotalAmount = %v, want 3500 (1000 standard + 2500 vip)", b.TotalAmount)
	}
}

func TestConfirmPostsServerAmountAndOperatorToOutbox(t *testing.T) {
	e := newEnv(t)
	trip := e.trip(t, map[string]float64{"standard": 750}, avail("A1")...)
	passenger := testutil.SeedPassenger(t, e.pool)
	b, err := e.svc.CreateHold(e.ctx, service.CreateHoldInput{TripID: trip, PassengerID: passenger, Seats: []service.SeatSelection{sel("A1", 1)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ConfirmBooking(e.ctx, passenger, b.ID, "ref-1"); err != nil {
		t.Fatal(err)
	}

	var amount float64
	var operatorID, wantOperator string
	e.scan(t, &amount, `SELECT (payload->>'amount')::float8 FROM platform.outbox_events WHERE event_type='BookingConfirmed' AND payload->>'booking_id'=$1`, b.ID)
	e.scan(t, &operatorID, `SELECT payload->>'operator_id' FROM platform.outbox_events WHERE event_type='BookingConfirmed' AND payload->>'booking_id'=$1`, b.ID)
	e.scan(t, &wantOperator, `SELECT operator_id::text FROM fleet.trips WHERE id=$1`, trip)
	if amount != 750 {
		t.Errorf("outbox amount = %v, want 750", amount)
	}
	if operatorID != wantOperator {
		t.Errorf("outbox operator_id = %q, want trip operator %q (ledger would panic/misroute)", operatorID, wantOperator)
	}
}

func TestCreateHoldRejectsUnknownSeat(t *testing.T) {
	e := newEnv(t)
	trip := e.trip(t, nil, avail("A1")...)
	_, err := e.svc.CreateHold(e.ctx, service.CreateHoldInput{
		TripID: trip, PassengerID: testutil.SeedPassenger(t, e.pool),
		Seats: []service.SeatSelection{sel("A1", 1000), sel("ZZ9", 1000)},
	})
	if !errors.Is(err, domain.ErrUnknownSeat) {
		t.Fatalf("want ErrUnknownSeat, got %v", err)
	}
	var status string
	e.scan(t, &status, `SELECT status FROM fleet.trip_seat_inventory WHERE trip_id=$1 AND seat_number='A1'`, trip)
	if status != "available" {
		t.Errorf("A1 is %q after a rejected request; nothing should have been held", status)
	}
}

func TestCreateHoldRejectsDuplicateAndEmptySelection(t *testing.T) {
	e := newEnv(t)
	trip := e.trip(t, nil, avail("A1")...)
	p := testutil.SeedPassenger(t, e.pool)
	if _, err := e.svc.CreateHold(e.ctx, service.CreateHoldInput{TripID: trip, PassengerID: p,
		Seats: []service.SeatSelection{sel("A1", 1), sel("A1", 1)}}); !errors.Is(err, domain.ErrDuplicateSeat) {
		t.Errorf("duplicate seat: want ErrDuplicateSeat, got %v", err)
	}
	if _, err := e.svc.CreateHold(e.ctx, service.CreateHoldInput{TripID: trip, PassengerID: p}); !errors.Is(err, domain.ErrNoSeatsSelected) {
		t.Errorf("empty selection: want ErrNoSeatsSelected, got %v", err)
	}
}

// TestPartialHoldIsRolledBack: if one of several seats is taken, none stay held.
func TestPartialHoldIsRolledBack(t *testing.T) {
	e := newEnv(t)
	trip := e.trip(t, nil,
		testutil.SeatSeed{SeatNumber: "A1", Status: "available"},
		testutil.SeatSeed{SeatNumber: "A2", Status: "held", HoldExpiresAt: time.Now().Add(5 * time.Minute)},
	)
	_, err := e.svc.CreateHold(e.ctx, service.CreateHoldInput{
		TripID: trip, PassengerID: testutil.SeedPassenger(t, e.pool),
		Seats: []service.SeatSelection{sel("A1", 1000), sel("A2", 1000)},
	})
	if !errors.Is(err, domain.ErrSeatUnavailable) {
		t.Fatalf("want ErrSeatUnavailable, got %v", err)
	}
	var a1 string
	e.scan(t, &a1, `SELECT status FROM fleet.trip_seat_inventory WHERE trip_id=$1 AND seat_number='A1'`, trip)
	if a1 != "available" {
		t.Errorf("A1 stayed %q after a failed multi-seat hold (seat leaked)", a1)
	}
	var a2 string
	e.scan(t, &a2, `SELECT status FROM fleet.trip_seat_inventory WHERE trip_id=$1 AND seat_number='A2'`, trip)
	if a2 != "held" {
		t.Errorf("the other buyer's A2 hold was disturbed: %q", a2)
	}
	var avail int
	e.scan(t, &avail, `SELECT available_seats FROM fleet.trips WHERE id=$1`, trip)
	if avail != 2 {
		t.Errorf("available_seats = %d, want 2 (counter drifted)", avail)
	}
}

// TestCancelNeverFreesSomeoneElsesSeat: after a hold expires and another buyer
// takes the seat, the first buyer's late cancel must not free the new hold.
func TestCancelNeverFreesSomeoneElsesSeat(t *testing.T) {
	e := newEnv(t)
	trip := e.trip(t, nil, avail("A1")...)
	pX, pY := testutil.SeedPassenger(t, e.pool), testutil.SeedPassenger(t, e.pool)

	x, err := e.svc.CreateHold(e.ctx, service.CreateHoldInput{TripID: trip, PassengerID: pX, Seats: []service.SeatSelection{sel("A1", 1)}})
	if err != nil {
		t.Fatal(err)
	}
	// X's hold lapses and the sweeper frees it; Y grabs the seat.
	if _, err := e.pool.Exec(e.ctx, `UPDATE fleet.trip_seat_inventory SET hold_expires_at = now() - interval '1 minute' WHERE trip_id=$1`, trip); err != nil {
		t.Fatal(err)
	}
	if _, err := e.seats.ReleaseExpiredHolds(e.ctx); err != nil {
		t.Fatal(err)
	}
	y, err := e.svc.CreateHold(e.ctx, service.CreateHoldInput{TripID: trip, PassengerID: pY, Seats: []service.SeatSelection{sel("A1", 1)}})
	if err != nil {
		t.Fatalf("Y should get the freed seat: %v", err)
	}

	if _, err := e.svc.CancelBooking(e.ctx, pX, x.ID); err != nil {
		t.Fatalf("X cancel: %v", err)
	}
	var status, holder string
	e.scan(t, &status, `SELECT status FROM fleet.trip_seat_inventory WHERE trip_id=$1 AND seat_number='A1'`, trip)
	e.scan(t, &holder, `SELECT COALESCE(booking_id::text,'') FROM fleet.trip_seat_inventory WHERE trip_id=$1 AND seat_number='A1'`, trip)
	if status != "held" || holder != y.ID {
		t.Errorf("X's late cancel stole Y's seat: status=%q holder=%q want held by %s", status, holder, y.ID)
	}
}

// TestBookingsAreOwnedByTheirPassenger: another passenger can neither read, pay
// for nor cancel a booking (IDOR); it looks like it doesn't exist.
func TestBookingsAreOwnedByTheirPassenger(t *testing.T) {
	e := newEnv(t)
	trip := e.trip(t, nil, avail("A1")...)
	owner, attacker := testutil.SeedPassenger(t, e.pool), testutil.SeedPassenger(t, e.pool)
	b, err := e.svc.CreateHold(e.ctx, service.CreateHoldInput{TripID: trip, PassengerID: owner, Seats: []service.SeatSelection{sel("A1", 1)}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.GetBooking(e.ctx, attacker, b.ID); !errors.Is(err, domain.ErrBookingNotFound) {
		t.Errorf("attacker GET: want ErrBookingNotFound, got %v", err)
	}
	if _, err := e.svc.ConfirmBooking(e.ctx, attacker, b.ID, "x"); !errors.Is(err, domain.ErrBookingNotFound) {
		t.Errorf("attacker confirm: want ErrBookingNotFound, got %v", err)
	}
	if _, err := e.svc.CancelBooking(e.ctx, attacker, b.ID); !errors.Is(err, domain.ErrBookingNotFound) {
		t.Errorf("attacker cancel: want ErrBookingNotFound, got %v", err)
	}
	var status string
	e.scan(t, &status, `SELECT status FROM booking.bookings WHERE id=$1`, b.ID)
	if status != "held" {
		t.Errorf("booking status = %q after attacker attempts, want held", status)
	}
	if len(e.gw.charges) != 0 {
		t.Errorf("attacker caused a charge: %v", e.gw.charges)
	}
}
