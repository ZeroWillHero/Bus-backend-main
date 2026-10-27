// Package domain defines the Booking aggregate root (DOMAIN_MODEL.md §2.4),
// the core state machine for the seat-hold → pay → confirm lifecycle.
// Phase 3 does NOT implement hold/release/confirm as in-memory methods on
// Trip — they're repository-level ports (fleet.SeatHoldPort) that the
// booking service calls. This is pure and unit-testable.
package domain

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"time"
)

type BookingStatus string

const (
	BookingStatusHeld BookingStatus = "held"
	// BookingStatusPaymentPending: a confirm has claimed the booking and its
	// charge is in flight. Only the confirm that won held -> payment_pending
	// may charge; it ends in confirmed, or back in held if the charge fails.
	BookingStatusPaymentPending BookingStatus = "payment_pending"
	BookingStatusConfirmed      BookingStatus = "confirmed"
	BookingStatusCancelled      BookingStatus = "cancelled"
	BookingStatusPaidButExpired BookingStatus = "paid_but_expired"
)

type Booking struct {
	ID            string
	TripID        string
	PassengerID   string
	Status        BookingStatus
	TotalAmount   float64
	Currency      string
	HoldExpiresAt *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ConfirmedAt   *time.Time
	CancelledAt   *time.Time
}

type BookingSeat struct {
	ID            string
	BookingID     string
	SeatNumber    string
	PassengerName string
	PassengerNic  string
	FareAmount    float64
}

type Ticket struct {
	ID          string
	BookingID   string
	QRToken     string
	IssuedAt    time.Time
	CheckedInAt *time.Time
}

type SeatHold struct {
	SeatNumbers []string
	ExpiresAt   time.Time
}

// CanTransition guards the allowed status transitions per MITIGATION_PLAN.md §1.3/§2.1.
// Held → Confirmed (normal checkout), Held → Cancelled (passenger cancel before pay),
// Held → PaidButExpired (payment succeeded after hold lapsed).
// Held → PaymentPending (a confirm claimed the booking and is charging);
// PaymentPending → Confirmed (charge + atomic confirm succeeded) or → Held
// (charge declined/failed, or the confirm transaction was rolled back).
// Cancel is only allowed from Held: a booking mid-charge cannot be cancelled.
// No transitions out of terminal states (Confirmed, Cancelled, PaidButExpired).
func CanTransition(from, to BookingStatus) bool {
	switch from {
	case BookingStatusHeld:
		return to == BookingStatusConfirmed || to == BookingStatusCancelled ||
			to == BookingStatusPaidButExpired || to == BookingStatusPaymentPending
	case BookingStatusPaymentPending:
		return to == BookingStatusConfirmed || to == BookingStatusHeld
	default:
		return false
	}
}

// IsExpired checks if the hold's TTL has passed.
func (sh *SeatHold) IsExpired(now time.Time) bool {
	return now.After(sh.ExpiresAt)
}

// GenerateQRToken creates an HMAC-SHA256 token over the booking ID,
// used to verify check-in attempts without trusting a client-supplied ID.
// The secret should be a stable server key (e.g. from config) rotated offline.
func GenerateQRToken(bookingID string, secret []byte) string {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(bookingID))
	return base64.URLEncoding.EncodeToString(h.Sum(nil))
}

// VerifyQRToken checks that a provided token matches the booking ID.
func VerifyQRToken(token, bookingID string, secret []byte) bool {
	expected := GenerateQRToken(bookingID, secret)
	return hmac.Equal([]byte(token), []byte(expected))
}

// Ports: the dependencies booking.service calls to implement its use cases.
// These are boundaries to fleet (SeatHoldPort), payment gateway, and persistence.

type BookingRepository interface {
	Create(ctx context.Context, b *Booking) (*Booking, error)
	FindByID(ctx context.Context, id string) (*Booking, error)
	FindByPassenger(ctx context.Context, passengerID string, cursor string, limit int) ([]*Booking, string, error)
	// TransitionStatus moves the booking from -> to only if it is still in
	// `from` (UPDATE ... WHERE id = $1 AND status = $from). If another request
	// changed it first it returns ErrConcurrentUpdate and changes nothing.
	// confirmedAt/cancelledAt, when non-nil, are stamped; nil keeps the old value.
	TransitionStatus(ctx context.Context, id string, from, to BookingStatus, confirmedAt, cancelledAt *time.Time) (*Booking, error)
	// CountActiveHolds counts the passenger's held/payment_pending bookings
	// whose hold has not expired.
	CountActiveHolds(ctx context.Context, passengerID string) (int, error)
}

// ConfirmCommit is everything ConfirmBooking writes once the charge succeeded.
type ConfirmCommit struct {
	BookingID   string
	TripID      string
	SeatNumbers []string // every seat of the booking; ALL must still be held by it
	ConfirmedAt time.Time
	TicketID    string // pre-generated so the outbox payload can carry it
	QRToken     string
	EventType   string
	Payload     interface{}
}

// BookingConfirmer applies a ConfirmCommit in ONE database transaction:
// seats held -> booked (all of them, or ErrSeatUnavailable and nothing is
// written), booking payment_pending -> confirmed, ticket insert, outbox insert.
type BookingConfirmer interface {
	CommitConfirmation(ctx context.Context, c ConfirmCommit) (*Booking, *Ticket, error)
}

type BookingSeatRepository interface {
	CreateBulk(ctx context.Context, seats []*BookingSeat) error
	FindByBookingID(ctx context.Context, bookingID string) ([]*BookingSeat, error)
}

type TicketRepository interface {
	Create(ctx context.Context, t *Ticket) (*Ticket, error)
	FindByBookingID(ctx context.Context, bookingID string) (*Ticket, error)
	FindByQRToken(ctx context.Context, token string) (*Ticket, error)
	MarkCheckedIn(ctx context.Context, id string) (*Ticket, error)
}

type PaymentResult struct {
	Gateway       string
	TransactionID string
	Approved      bool
	ErrorMessage  string
}

type PaymentGateway interface {
	Charge(ctx context.Context, amount float64, currency, reference string, details map[string]interface{}) (*PaymentResult, error)
	Refund(ctx context.Context, transactionID string, amount float64, currency string) error
}

type WebhookDedupeStore interface {
	AlreadyProcessed(ctx context.Context, eventID string) (bool, error)
	MarkProcessed(ctx context.Context, eventID string) error
}

type OutboxWriter interface {
	Write(ctx context.Context, tx interface{}, eventType string, payload interface{}) error
}

// SeatHoldPort: the cross-context dependency from Booking → Fleet.
// Fleet owns the seat inventory; booking triggers holds/releases via this port.
type SeatHoldPort interface {
	HoldSeats(ctx context.Context, tripID string, seatNumbers []string, bookingID string, ttl time.Duration) (heldCount int, err error)
	ConfirmSeats(ctx context.Context, tripID string, seatNumbers []string, bookingID string) error
	ReleaseSeats(ctx context.Context, tripID string, seatNumbers []string) error
	// ReleaseSeatsForBooking frees only seats currently held/booked FOR THIS booking.
	// Unlike ReleaseSeats (by seat number) it can never free a seat that has since
	// been re-held by somebody else after this booking's hold expired.
	ReleaseSeatsForBooking(ctx context.Context, bookingID string) error
	// ExtendHoldForBooking verifies and protects this booking's seats before a
	// charge: every seat still status='held' for this booking with an unexpired
	// hold gets hold_expires_at pushed to at least now()+d, so the expired-hold
	// sweep cannot free it while the charge is in flight. Returns how many seats
	// matched; fewer than the booking's seat count means a seat was lost.
	ExtendHoldForBooking(ctx context.Context, bookingID string, d time.Duration) (int, error)
}

// TripPricingPort is the server-side source of truth for what a booking costs
// and who it pays. The client's idea of a fare is never trusted.
type TripPricingPort interface {
	// SeatFares returns seat_number -> fare for a bookable (scheduled) trip.
	// Returns ErrUnknownSeat if any requested seat is not on the trip.
	SeatFares(ctx context.Context, tripID string, seatNumbers []string) (map[string]float64, error)
	TripOperatorID(ctx context.Context, tripID string) (string, error)
	// TripSchedule returns the trip's operator, status and departure time
	// (used to authorise and time-box check-in).
	TripSchedule(ctx context.Context, tripID string) (*TripSchedule, error)
}

// TripSchedule is the slice of a fleet trip booking needs for check-in.
type TripSchedule struct {
	OperatorID  string
	Status      string // fleet trip status: scheduled | cancelled | completed
	DepartureAt time.Time
}

// Fleet trip statuses booking cares about.
const (
	TripStatusScheduled = "scheduled"
	TripStatusCancelled = "cancelled"
	TripStatusCompleted = "completed"
)

// Abuse limits for holds.
const (
	// MaxSeatsPerBooking caps the seats one booking may hold.
	MaxSeatsPerBooking = 10
	// MaxActiveHoldsPerPassenger caps a passenger's concurrent unexpired holds
	// (held or payment_pending), so one account can't keep a whole bus held.
	MaxActiveHoldsPerPassenger = 3
)

// Check-in window around departure.
const (
	CheckInOpensBefore = 2 * time.Hour
	CheckInClosesAfter = 6 * time.Hour
)

// CheckInAllowed reports whether a ticket for this trip may be checked in at
// now: ErrTicketTripCancelled for a cancelled trip; ErrTicketNotValidNow
// outside [departure-2h, departure+6h] or for any other non-boardable status.
// 'completed' is accepted inside the window because the worker auto-completes
// trips, possibly before every late passenger has been scanned.
func CheckInAllowed(t *TripSchedule, now time.Time) error {
	switch t.Status {
	case TripStatusCancelled:
		return ErrTicketTripCancelled
	case TripStatusScheduled, TripStatusCompleted:
	default:
		return ErrTicketNotValidNow
	}
	if now.Before(t.DepartureAt.Add(-CheckInOpensBefore)) || now.After(t.DepartureAt.Add(CheckInClosesAfter)) {
		return ErrTicketNotValidNow
	}
	return nil
}
