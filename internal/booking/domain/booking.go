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
	BookingStatusHeld           BookingStatus = "held"
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
// No transitions out of terminal states (Confirmed, Cancelled, PaidButExpired).
func CanTransition(from, to BookingStatus) bool {
	switch from {
	case BookingStatusHeld:
		return to == BookingStatusConfirmed || to == BookingStatusCancelled || to == BookingStatusPaidButExpired
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
	UpdateStatus(ctx context.Context, id string, status BookingStatus, confirmedAt, cancelledAt *time.Time) (*Booking, error)
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
}
