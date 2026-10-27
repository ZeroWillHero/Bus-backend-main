package domain

import (
	"testing"
	"time"
)

// TestCanTransition verifies the booking status state machine transitions.
// Per MITIGATION_PLAN.md §1.3/§2.1: Held→Confirmed, Held→Cancelled, Held→PaidButExpired.
// No transitions out of terminal states.
func TestCanTransition(t *testing.T) {
	tests := []struct {
		name     string
		from     BookingStatus
		to       BookingStatus
		expected bool
	}{
		// Valid transitions
		{"Held to Confirmed", BookingStatusHeld, BookingStatusConfirmed, true},
		{"Held to Cancelled", BookingStatusHeld, BookingStatusCancelled, true},
		{"Held to PaidButExpired", BookingStatusHeld, BookingStatusPaidButExpired, true},
		{"Held to PaymentPending", BookingStatusHeld, BookingStatusPaymentPending, true},
		{"PaymentPending to Confirmed", BookingStatusPaymentPending, BookingStatusConfirmed, true},
		{"PaymentPending to Held", BookingStatusPaymentPending, BookingStatusHeld, true},

		// A booking mid-charge cannot be cancelled or double-claimed
		{"PaymentPending to Cancelled", BookingStatusPaymentPending, BookingStatusCancelled, false},
		{"PaymentPending to PaymentPending", BookingStatusPaymentPending, BookingStatusPaymentPending, false},
		{"Confirmed to PaymentPending", BookingStatusConfirmed, BookingStatusPaymentPending, false},

		// Invalid transitions from Held
		{"Held to Held", BookingStatusHeld, BookingStatusHeld, false},

		// No transitions out of terminal states
		{"Confirmed to Held", BookingStatusConfirmed, BookingStatusHeld, false},
		{"Confirmed to Confirmed", BookingStatusConfirmed, BookingStatusConfirmed, false},
		{"Confirmed to Cancelled", BookingStatusConfirmed, BookingStatusCancelled, false},
		{"Cancelled to Held", BookingStatusCancelled, BookingStatusHeld, false},
		{"Cancelled to Confirmed", BookingStatusCancelled, BookingStatusConfirmed, false},
		{"PaidButExpired to Held", BookingStatusPaidButExpired, BookingStatusHeld, false},
		{"PaidButExpired to Confirmed", BookingStatusPaidButExpired, BookingStatusConfirmed, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CanTransition(tt.from, tt.to)
			if result != tt.expected {
				t.Errorf("CanTransition(%q, %q) = %v, want %v", tt.from, tt.to, result, tt.expected)
			}
		})
	}
}

// TestSeatHoldIsExpired verifies the hold TTL check.
func TestSeatHoldIsExpired(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		expiresAt time.Time
		expected  bool
	}{
		{"Hold expired", now.Add(-1 * time.Minute), true},
		{"Hold not expired", now.Add(10 * time.Minute), false},
		{"Hold expires at exact time", now, false}, // now.After(now) is false
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hold := &SeatHold{
				SeatNumbers: []string{"A1"},
				ExpiresAt:   tt.expiresAt,
			}
			result := hold.IsExpired(now)
			if result != tt.expected {
				t.Errorf("SeatHold.IsExpired() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestQRTokenRoundTrip verifies that a generated token can be verified.
func TestQRTokenRoundTrip(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-minimum")
	bookingID := "booking-123"

	// Generate a token
	token := GenerateQRToken(bookingID, secret)

	// Verify it
	if !VerifyQRToken(token, bookingID, secret) {
		t.Error("VerifyQRToken failed for valid token")
	}
}

// TestQRTokenTamperingDetection verifies that a tampered token is rejected.
func TestQRTokenTamperingDetection(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-minimum")
	bookingID := "booking-123"

	// Generate a token
	token := GenerateQRToken(bookingID, secret)

	// Tamper with it
	tamperedToken := token[0:len(token)-1] + "X"

	// Verify should fail
	if VerifyQRToken(tamperedToken, bookingID, secret) {
		t.Error("VerifyQRToken accepted tampered token")
	}
}

// TestQRTokenWrongBookingID verifies that a token is bound to its booking ID.
func TestQRTokenWrongBookingID(t *testing.T) {
	secret := []byte("test-secret-key-32-chars-minimum")
	bookingID := "booking-123"

	// Generate a token for booking-123
	token := GenerateQRToken(bookingID, secret)

	// Try to verify it against a different booking ID
	if VerifyQRToken(token, "booking-456", secret) {
		t.Error("VerifyQRToken accepted token for wrong booking ID")
	}
}

// TestQRTokenWrongSecret verifies that a token is only valid with the correct secret.
func TestQRTokenWrongSecret(t *testing.T) {
	secret1 := []byte("secret-1-32-chars-minimum-key")
	secret2 := []byte("secret-2-32-chars-minimum-key")
	bookingID := "booking-123"

	// Generate a token with secret1
	token := GenerateQRToken(bookingID, secret1)

	// Try to verify it with secret2
	if VerifyQRToken(token, bookingID, secret2) {
		t.Error("VerifyQRToken accepted token signed with different secret")
	}
}
