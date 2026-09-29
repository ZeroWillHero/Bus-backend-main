package service

import (
	"context"
	"fmt"
	"time"

	"github.com/sl-bus/backend/internal/booking/domain"
)

// CheckoutService implements the core booking lifecycle (Phase 3 core domain).
type CheckoutService struct {
	bookingRepo     domain.BookingRepository
	bookingSeatRepo domain.BookingSeatRepository
	ticketRepo      domain.TicketRepository
	seatHold        domain.SeatHoldPort
	paymentGateway  domain.PaymentGateway
	outboxWriter    domain.OutboxWriter
	qrTokenSecret   []byte
}

func NewCheckoutService(
	bookingRepo domain.BookingRepository,
	bookingSeatRepo domain.BookingSeatRepository,
	ticketRepo domain.TicketRepository,
	seatHold domain.SeatHoldPort,
	paymentGateway domain.PaymentGateway,
	outboxWriter domain.OutboxWriter,
	qrTokenSecret []byte,
) *CheckoutService {
	return &CheckoutService{
		bookingRepo:     bookingRepo,
		bookingSeatRepo: bookingSeatRepo,
		ticketRepo:      ticketRepo,
		seatHold:        seatHold,
		paymentGateway:  paymentGateway,
		outboxWriter:    outboxWriter,
		qrTokenSecret:   qrTokenSecret,
	}
}

type CreateHoldInput struct {
	TripID      string
	Seats       []SeatSelection
	PassengerID string
}

type SeatSelection struct {
	SeatNumber    string
	PassengerName string
	PassengerNic  string
	FareAmount    float64
}

// CreateHold creates a booking in 'held' status with a 10-minute hold on the seats.
// Returns ErrSeatUnavailable if any requested seat is not available.
func (s *CheckoutService) CreateHold(ctx context.Context, input CreateHoldInput) (*domain.Booking, error) {
	// Attempt to hold all seats
	seatNumbers := make([]string, len(input.Seats))
	totalAmount := 0.0
	for i, seat := range input.Seats {
		seatNumbers[i] = seat.SeatNumber
		totalAmount += seat.FareAmount
	}

	heldCount, err := s.seatHold.HoldSeats(ctx, input.TripID, seatNumbers, "", 10*time.Minute)
	if err != nil {
		return nil, err
	}

	// Partial or full unavailability
	if heldCount < len(seatNumbers) {
		return nil, domain.ErrSeatUnavailable
	}

	// All seats held; create the booking
	booking := &domain.Booking{
		TripID:        input.TripID,
		PassengerID:   input.PassengerID,
		Status:        domain.BookingStatusHeld,
		TotalAmount:   totalAmount,
		Currency:      "LKR",
		HoldExpiresAt: pointerTo(time.Now().Add(10 * time.Minute)),
	}

	booking, err = s.bookingRepo.Create(ctx, booking)
	if err != nil {
		return nil, err
	}

	// Now link the held seats to this booking and create booking_seats
	_, err = s.seatHold.HoldSeats(ctx, input.TripID, seatNumbers, booking.ID, 10*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("relink seats to booking: %w", err)
	}

	// Create booking_seats records
	bookingSeats := make([]*domain.BookingSeat, len(input.Seats))
	for i, seat := range input.Seats {
		bookingSeats[i] = &domain.BookingSeat{
			BookingID:     booking.ID,
			SeatNumber:    seat.SeatNumber,
			PassengerName: seat.PassengerName,
			PassengerNic:  seat.PassengerNic,
			FareAmount:    seat.FareAmount,
		}
	}

	if err := s.bookingSeatRepo.CreateBulk(ctx, bookingSeats); err != nil {
		return nil, err
	}

	return booking, nil
}

// ConfirmBooking transitions a held booking to confirmed after successful payment.
// Implements MITIGATION_PLAN.md §1.3 (PaidButExpired auto-refund if hold expired).
func (s *CheckoutService) ConfirmBooking(ctx context.Context, bookingID string, paymentRef string) (*domain.Booking, error) {
	booking, err := s.bookingRepo.FindByID(ctx, bookingID)
	if err != nil {
		return nil, err
	}

	if booking.Status != domain.BookingStatusHeld {
		return nil, domain.ErrInvalidStatusTransition
	}

	// Check if hold has expired (mitigation 1.3)
	if booking.HoldExpiresAt != nil && time.Now().After(*booking.HoldExpiresAt) {
		// Hold expired — transition to PaidButExpired and auto-refund
		now := time.Now()
		booking, err = s.bookingRepo.UpdateStatus(ctx, bookingID, domain.BookingStatusPaidButExpired, nil, &now)
		if err != nil {
			return nil, err
		}

		// Trigger automatic refund (amount to be determined by ledger phase)
		_ = s.paymentGateway.Refund(ctx, paymentRef, booking.TotalAmount, booking.Currency)

		// Write outbox event for Phase 4/5
		if err := s.outboxWriter.Write(ctx, nil, "PaidButExpired", map[string]interface{}{
			"booking_id":       bookingID,
			"amount":           booking.TotalAmount,
			"currency":         booking.Currency,
			"reason":           "hold_expired_before_payment",
			"refund_initiated": true,
		}); err != nil {
			return nil, fmt.Errorf("write outbox: %w", err)
		}

		return booking, nil
	}

	// Hold still valid; attempt charge
	result, err := s.paymentGateway.Charge(ctx, booking.TotalAmount, booking.Currency, paymentRef, nil)
	if err != nil {
		return nil, err
	}

	if !result.Approved {
		// Payment declined; booking stays held for retry
		return booking, fmt.Errorf("payment declined: %s", result.ErrorMessage)
	}

	// Payment succeeded; confirm seats and booking
	seats, err := s.bookingSeatRepo.FindByBookingID(ctx, bookingID)
	if err != nil {
		return nil, err
	}

	seatNumbers := make([]string, len(seats))
	for i, s := range seats {
		seatNumbers[i] = s.SeatNumber
	}

	if err := s.seatHold.ConfirmSeats(ctx, booking.TripID, seatNumbers, bookingID); err != nil {
		return nil, err
	}

	// Update booking to confirmed
	now := time.Now()
	booking, err = s.bookingRepo.UpdateStatus(ctx, bookingID, domain.BookingStatusConfirmed, &now, nil)
	if err != nil {
		return nil, err
	}

	// Issue ticket with QR token
	qrToken := domain.GenerateQRToken(bookingID, s.qrTokenSecret)
	ticket := &domain.Ticket{
		BookingID: bookingID,
		QRToken:   qrToken,
	}
	ticket, err = s.ticketRepo.Create(ctx, ticket)
	if err != nil {
		return nil, err
	}

	// Write outbox event for Phase 4 (ledger posting) and Phase 5 (SMS/email)
	if err := s.outboxWriter.Write(ctx, nil, "BookingConfirmed", map[string]interface{}{
		"booking_id":   bookingID,
		"trip_id":      booking.TripID,
		"passenger_id": booking.PassengerID,
		"amount":       booking.TotalAmount,
		"currency":     booking.Currency,
		"ticket_id":    ticket.ID,
	}); err != nil {
		return nil, fmt.Errorf("write outbox: %w", err)
	}

	return booking, nil
}

// CancelBooking transitions a held/confirmed booking to cancelled and releases its seats.
func (s *CheckoutService) CancelBooking(ctx context.Context, bookingID string) (*domain.Booking, error) {
	booking, err := s.bookingRepo.FindByID(ctx, bookingID)
	if err != nil {
		return nil, err
	}

	if !domain.CanTransition(booking.Status, domain.BookingStatusCancelled) {
		return nil, domain.ErrInvalidStatusTransition
	}

	// Release seats
	seats, err := s.bookingSeatRepo.FindByBookingID(ctx, bookingID)
	if err != nil {
		return nil, err
	}

	seatNumbers := make([]string, len(seats))
	for i, s := range seats {
		seatNumbers[i] = s.SeatNumber
	}

	if err := s.seatHold.ReleaseSeats(ctx, booking.TripID, seatNumbers); err != nil {
		return nil, err
	}

	// Update booking to cancelled
	now := time.Now()
	booking, err = s.bookingRepo.UpdateStatus(ctx, bookingID, domain.BookingStatusCancelled, nil, &now)
	if err != nil {
		return nil, err
	}

	// Write outbox event
	if err := s.outboxWriter.Write(ctx, nil, "BookingCancelled", map[string]interface{}{
		"booking_id":    bookingID,
		"trip_id":       booking.TripID,
		"passenger_id":  booking.PassengerID,
		"was_confirmed": booking.Status == domain.BookingStatusConfirmed,
	}); err != nil {
		return nil, fmt.Errorf("write outbox: %w", err)
	}

	return booking, nil
}

// GetBooking retrieves a booking by ID
func (s *CheckoutService) GetBooking(ctx context.Context, bookingID string) (*domain.Booking, error) {
	return s.bookingRepo.FindByID(ctx, bookingID)
}

// CheckIn marks a ticket as checked in using the QR token
func (s *CheckoutService) CheckIn(ctx context.Context, qrToken string) (*domain.Ticket, error) {
	ticket, err := s.ticketRepo.FindByQRToken(ctx, qrToken)
	if err != nil {
		return nil, domain.ErrInvalidQRToken
	}

	if ticket.CheckedInAt != nil {
		return nil, domain.ErrAlreadyCheckedIn
	}

	return s.ticketRepo.MarkCheckedIn(ctx, ticket.ID)
}

func pointerTo[T any](v T) *T {
	return &v
}
