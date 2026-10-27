package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/sl-bus/backend/internal/booking/domain"
)

// CheckoutService implements the core booking lifecycle (Phase 3 core domain).
type CheckoutService struct {
	bookingRepo     domain.BookingRepository
	bookingSeatRepo domain.BookingSeatRepository
	ticketRepo      domain.TicketRepository
	seatHold        domain.SeatHoldPort
	pricing         domain.TripPricingPort
	paymentGateway  domain.PaymentGateway
	outboxWriter    domain.OutboxWriter
	confirmer       domain.BookingConfirmer
	qrTokenSecret   []byte
}

func NewCheckoutService(
	bookingRepo domain.BookingRepository,
	bookingSeatRepo domain.BookingSeatRepository,
	ticketRepo domain.TicketRepository,
	seatHold domain.SeatHoldPort,
	pricing domain.TripPricingPort,
	paymentGateway domain.PaymentGateway,
	outboxWriter domain.OutboxWriter,
	confirmer domain.BookingConfirmer,
	qrTokenSecret []byte,
) *CheckoutService {
	return &CheckoutService{
		bookingRepo:     bookingRepo,
		bookingSeatRepo: bookingSeatRepo,
		ticketRepo:      ticketRepo,
		seatHold:        seatHold,
		pricing:         pricing,
		paymentGateway:  paymentGateway,
		outboxWriter:    outboxWriter,
		confirmer:       confirmer,
		qrTokenSecret:   qrTokenSecret,
	}
}

type CreateHoldInput struct {
	TripID      string
	Seats       []SeatSelection
	PassengerID string
}

// SeatSelection is what the client asks for. FareAmount is accepted only so the
// API stays compatible; it is NEVER used for pricing (see CreateHold).
type SeatSelection struct {
	SeatNumber    string
	PassengerName string
	PassengerNic  string
	FareAmount    float64
}

// holdTTL is how long a seat is held before the worker sweeps it back.
const holdTTL = 10 * time.Minute

// CreateHold creates a booking in 'held' status with a 10-minute hold on the seats.
//
// Pricing is entirely server-side: every seat's fare comes from the trip's fare
// table, so a tampered client FareAmount (0.01, negative, huge) cannot change
// what is charged or what the operator is credited.
// Returns ErrSeatUnavailable if any requested seat is not available.
func (s *CheckoutService) CreateHold(ctx context.Context, input CreateHoldInput) (*domain.Booking, error) {
	if len(input.Seats) == 0 {
		return nil, domain.ErrNoSeatsSelected
	}
	if len(input.Seats) > domain.MaxSeatsPerBooking {
		return nil, domain.ErrTooManySeats
	}
	seatNumbers := make([]string, len(input.Seats))
	seen := make(map[string]struct{}, len(input.Seats))
	for i, seat := range input.Seats {
		if _, dup := seen[seat.SeatNumber]; dup {
			return nil, domain.ErrDuplicateSeat
		}
		seen[seat.SeatNumber] = struct{}{}
		seatNumbers[i] = seat.SeatNumber
	}

	// Count-then-insert is not atomic: simultaneous requests can each see
	// MaxActiveHoldsPerPassenger-1 and all succeed. Overshooting by a few
	// concurrent requests is acceptable for an abuse limit; holds still expire.
	active, err := s.bookingRepo.CountActiveHolds(ctx, input.PassengerID)
	if err != nil {
		return nil, err
	}
	if active >= domain.MaxActiveHoldsPerPassenger {
		return nil, domain.ErrTooManyActiveHolds
	}

	fares, err := s.pricing.SeatFares(ctx, input.TripID, seatNumbers)
	if err != nil {
		return nil, err
	}
	totalCents := int64(0)
	for _, seat := range input.Seats {
		serverFare := fares[seat.SeatNumber]
		if seat.FareAmount != 0 && math.Abs(seat.FareAmount-serverFare) > 0.005 {
			slog.WarnContext(ctx, "client fare differs from server fare; ignoring client value",
				"trip_id", input.TripID, "seat", seat.SeatNumber,
				"client_fare", seat.FareAmount, "server_fare", serverFare, "passenger_id", input.PassengerID)
		}
		totalCents += int64(math.Round(serverFare * 100))
	}

	// Create the booking first: seats are linked to a real booking id as they are held.
	booking, err := s.bookingRepo.Create(ctx, &domain.Booking{
		TripID:        input.TripID,
		PassengerID:   input.PassengerID,
		Status:        domain.BookingStatusHeld,
		TotalAmount:   float64(totalCents) / 100,
		Currency:      "LKR",
		HoldExpiresAt: pointerTo(time.Now().Add(holdTTL)),
	})
	if err != nil {
		return nil, err
	}

	// abort undoes a partial hold: frees only THIS booking's seats and cancels it.
	abort := func(cause error) (*domain.Booking, error) {
		_ = s.seatHold.ReleaseSeatsForBooking(ctx, booking.ID)
		now := time.Now()
		_, _ = s.bookingRepo.TransitionStatus(ctx, booking.ID, domain.BookingStatusHeld, domain.BookingStatusCancelled, nil, &now)
		return nil, cause
	}

	heldCount, err := s.seatHold.HoldSeats(ctx, input.TripID, seatNumbers, booking.ID, holdTTL)
	if err != nil {
		return abort(err)
	}
	if heldCount < len(seatNumbers) {
		return abort(domain.ErrSeatUnavailable)
	}

	bookingSeats := make([]*domain.BookingSeat, len(input.Seats))
	for i, seat := range input.Seats {
		bookingSeats[i] = &domain.BookingSeat{
			BookingID:     booking.ID,
			SeatNumber:    seat.SeatNumber,
			PassengerName: seat.PassengerName,
			PassengerNic:  seat.PassengerNic,
			FareAmount:    fares[seat.SeatNumber],
		}
	}
	if err := s.bookingSeatRepo.CreateBulk(ctx, bookingSeats); err != nil {
		return abort(err)
	}
	return booking, nil
}

// loadOwned fetches a booking and hides it (ErrBookingNotFound) from anyone but
// its passenger, so one passenger can't read, pay for or cancel another's booking.
// An empty actorID skips the check (internal callers).
func (s *CheckoutService) loadOwned(ctx context.Context, bookingID, actorID string) (*domain.Booking, error) {
	booking, err := s.bookingRepo.FindByID(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	if actorID != "" && booking.PassengerID != actorID {
		return nil, domain.ErrBookingNotFound
	}
	return booking, nil
}

// paymentWindow is how long a confirm protects the seats while it charges:
// their hold is extended to at least now()+paymentWindow so the expired-hold
// sweep cannot free a seat mid-charge.
const paymentWindow = 2 * time.Minute

// ConfirmBooking charges a held booking and confirms it.
//
//  1. Only a 'held' booking with an unexpired hold can be paid. An expired hold
//     is ErrHoldExpired: nothing has been charged, so nothing is refunded.
//  2. held -> payment_pending is a compare-and-set, so of N concurrent confirms
//     exactly one proceeds; the rest get ErrConcurrentUpdate and never charge.
//  3. Before charging, every seat must still be held for this booking (and the
//     holds are extended for the payment window). Otherwise: no charge.
//  4. After a successful charge, seats/booking/ticket/outbox commit in ONE
//     transaction. If that fails, the charge is refunded and the booking goes
//     back to held.
func (s *CheckoutService) ConfirmBooking(ctx context.Context, actorID, bookingID, paymentRef string) (*domain.Booking, error) {
	booking, err := s.loadOwned(ctx, bookingID, actorID)
	if err != nil {
		return nil, err
	}
	if booking.Status != domain.BookingStatusHeld {
		return nil, domain.ErrInvalidStatusTransition
	}
	if booking.HoldExpiresAt != nil && time.Now().After(*booking.HoldExpiresAt) {
		return nil, domain.ErrHoldExpired
	}

	// Resolve the payee and seats before claiming the booking, so these
	// failures leave it untouched.
	operatorID, err := s.pricing.TripOperatorID(ctx, booking.TripID)
	if err != nil {
		return nil, fmt.Errorf("resolve trip operator: %w", err)
	}
	seats, err := s.bookingSeatRepo.FindByBookingID(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	seatNumbers := make([]string, len(seats))
	for i, seat := range seats {
		seatNumbers[i] = seat.SeatNumber
	}

	if _, err := s.bookingRepo.TransitionStatus(ctx, bookingID,
		domain.BookingStatusHeld, domain.BookingStatusPaymentPending, nil, nil); err != nil {
		return nil, err // ErrConcurrentUpdate: another request got here first
	}
	// From here on this request owns the booking; undo the claim even if the
	// client goes away, and never let a cancelled ctx skip a refund.
	bg := context.WithoutCancel(ctx)
	backToHeld := func() {
		if _, err := s.bookingRepo.TransitionStatus(bg, bookingID,
			domain.BookingStatusPaymentPending, domain.BookingStatusHeld, nil, nil); err != nil {
			slog.ErrorContext(bg, "confirm: could not return booking to held", "booking_id", bookingID, "error", err)
		}
	}

	held, err := s.seatHold.ExtendHoldForBooking(ctx, bookingID, paymentWindow)
	if err != nil {
		backToHeld()
		return nil, err
	}
	if held != len(seatNumbers) || len(seatNumbers) == 0 {
		// A seat's hold lapsed (and may already belong to someone else): this
		// booking can never be fulfilled. Cancel it via held (cancel is only
		// allowed from held) and free whatever seats it still has.
		backToHeld()
		now := time.Now()
		if _, err := s.bookingRepo.TransitionStatus(bg, bookingID,
			domain.BookingStatusHeld, domain.BookingStatusCancelled, nil, &now); err == nil {
			_ = s.seatHold.ReleaseSeatsForBooking(bg, bookingID)
		}
		return nil, domain.ErrHoldExpired
	}

	// The amount is the server-computed booking total.
	result, err := s.paymentGateway.Charge(ctx, booking.TotalAmount, booking.Currency, paymentRef, nil)
	if err != nil {
		backToHeld()
		return nil, err
	}
	if !result.Approved {
		backToHeld()
		booking.Status = domain.BookingStatusHeld
		return booking, fmt.Errorf("%w: %s", domain.ErrPaymentDeclined, result.ErrorMessage)
	}

	ticketID := uuid.NewString()
	confirmed, _, err := s.confirmer.CommitConfirmation(bg, domain.ConfirmCommit{
		BookingID:   bookingID,
		TripID:      booking.TripID,
		SeatNumbers: seatNumbers,
		ConfirmedAt: time.Now(),
		TicketID:    ticketID,
		QRToken:     domain.GenerateQRToken(bookingID, s.qrTokenSecret),
		EventType:   "BookingConfirmed", // Phase 4 ledger posting, Phase 5 SMS/email
		Payload: map[string]interface{}{
			"booking_id":   bookingID,
			"trip_id":      booking.TripID,
			"operator_id":  operatorID,
			"passenger_id": booking.PassengerID,
			"amount":       booking.TotalAmount,
			"currency":     booking.Currency,
			"ticket_id":    ticketID,
		},
	})
	if err != nil {
		// Charged but not confirmed. Nothing was written (single tx), so give
		// the money back. TODO: durable recovery (a refund outbox/reconciler)
		// for when this refund itself fails.
		slog.ErrorContext(bg, "CHARGED BUT NOT CONFIRMED: confirm transaction failed after successful charge; refunding",
			"booking_id", bookingID, "payment_ref", paymentRef, "transaction_id", result.TransactionID,
			"amount", booking.TotalAmount, "error", err)
		if rerr := s.paymentGateway.Refund(bg, result.TransactionID, booking.TotalAmount, booking.Currency); rerr != nil {
			slog.ErrorContext(bg, "REFUND FAILED after confirm rollback: manual refund required",
				"booking_id", bookingID, "payment_ref", paymentRef, "transaction_id", result.TransactionID,
				"amount", booking.TotalAmount, "error", rerr)
		}
		backToHeld()
		return nil, fmt.Errorf("confirm booking (payment refunded): %w", err)
	}
	return confirmed, nil
}

// CancelBooking transitions a held booking to cancelled and releases its seats.
// The cancel is a compare-and-set from the status that was read, so it cannot
// cancel a booking a concurrent confirm has just claimed (payment_pending).
func (s *CheckoutService) CancelBooking(ctx context.Context, actorID, bookingID string) (*domain.Booking, error) {
	booking, err := s.loadOwned(ctx, bookingID, actorID)
	if err != nil {
		return nil, err
	}

	if !domain.CanTransition(booking.Status, domain.BookingStatusCancelled) {
		return nil, domain.ErrInvalidStatusTransition
	}
	// Captured BEFORE the update: after it, booking.Status is already 'cancelled'.
	wasConfirmed := booking.Status == domain.BookingStatusConfirmed

	now := time.Now()
	booking, err = s.bookingRepo.TransitionStatus(ctx, bookingID, booking.Status, domain.BookingStatusCancelled, nil, &now)
	if err != nil {
		return nil, err
	}

	// Free only this booking's seats (never a seat someone else re-held after
	// expiry). If this fails the seats stay held for a cancelled booking until
	// their hold expires and the sweep frees them.
	if err := s.seatHold.ReleaseSeatsForBooking(ctx, bookingID); err != nil {
		return nil, err
	}

	payload := map[string]interface{}{
		"booking_id":    bookingID,
		"trip_id":       booking.TripID,
		"passenger_id":  booking.PassengerID,
		"amount":        booking.TotalAmount,
		"was_confirmed": wasConfirmed,
	}
	if wasConfirmed {
		operatorID, err := s.pricing.TripOperatorID(ctx, booking.TripID)
		if err != nil {
			return nil, fmt.Errorf("resolve trip operator: %w", err)
		}
		payload["operator_id"] = operatorID
	}
	if err := s.outboxWriter.Write(ctx, nil, "BookingCancelled", payload); err != nil {
		return nil, fmt.Errorf("write outbox: %w", err)
	}
	return booking, nil
}

// GetBooking retrieves a booking by ID
func (s *CheckoutService) GetBooking(ctx context.Context, actorID, bookingID string) (*domain.Booking, error) {
	return s.loadOwned(ctx, bookingID, actorID)
}

// CheckIn marks a ticket as checked in using the QR token. operatorID is the
// caller's operator (from the verified JWT): a ticket is only visible to staff
// of the operator that runs its trip, so another operator's ticket reads as
// "not found" rather than being checked in or confirmed to exist.
func (s *CheckoutService) CheckIn(ctx context.Context, operatorID, qrToken string) (*domain.Ticket, error) {
	ticket, err := s.ticketRepo.FindByQRToken(ctx, qrToken)
	if err != nil {
		return nil, domain.ErrInvalidQRToken
	}
	if !domain.VerifyQRToken(qrToken, ticket.BookingID, s.qrTokenSecret) {
		return nil, domain.ErrInvalidQRToken
	}

	// Authorise before revealing anything else about the ticket (e.g. whether
	// it was already used). Under RLS another operator's booking is invisible
	// (not found); with a role that bypasses RLS the explicit comparison below
	// is what stops them. Either way the answer is the same.
	booking, err := s.bookingRepo.FindByID(ctx, ticket.BookingID)
	if err != nil {
		return nil, domain.ErrTicketNotFound
	}
	trip, err := s.pricing.TripSchedule(ctx, booking.TripID)
	if err != nil || operatorID == "" || trip.OperatorID != operatorID {
		return nil, domain.ErrTicketNotFound
	}

	// Only now (caller is the trip's operator) reveal trip state and timing.
	if err := domain.CheckInAllowed(trip, time.Now()); err != nil {
		return nil, err
	}
	if ticket.CheckedInAt != nil {
		return nil, domain.ErrAlreadyCheckedIn
	}
	return s.ticketRepo.MarkCheckedIn(ctx, ticket.ID)
}

// GetTicket returns the passenger's own ticket (with the QR token to show at
// boarding). A booking that is not theirs, or has no ticket yet, is not found.
func (s *CheckoutService) GetTicket(ctx context.Context, actorID, bookingID string) (*domain.Ticket, error) {
	if _, err := s.loadOwned(ctx, bookingID, actorID); err != nil {
		return nil, err
	}
	ticket, err := s.ticketRepo.FindByBookingID(ctx, bookingID)
	if err != nil {
		return nil, domain.ErrTicketNotFound
	}
	return ticket, nil
}

// ListBookings returns the passenger's own bookings, newest first.
func (s *CheckoutService) ListBookings(ctx context.Context, actorID, cursor string, limit int) ([]*domain.Booking, string, error) {
	return s.bookingRepo.FindByPassenger(ctx, actorID, cursor, limit)
}

func pointerTo[T any](v T) *T {
	return &v
}
