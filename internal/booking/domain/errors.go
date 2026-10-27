package domain

import "errors"

var (
	ErrSeatUnavailable         = errors.New("seat(s) unavailable or already held")
	ErrHoldExpired             = errors.New("seat hold has expired")
	ErrBookingNotFound         = errors.New("booking not found")
	ErrInvalidStatusTransition = errors.New("invalid booking status transition")
	ErrAlreadyCheckedIn        = errors.New("ticket already checked in")
	ErrInvalidQRToken          = errors.New("invalid or tampered QR token")
	ErrUnknownSeat             = errors.New("seat does not exist on this trip")
	ErrDuplicateSeat           = errors.New("the same seat was selected more than once")
	ErrNoSeatsSelected         = errors.New("no seats selected")
	ErrPaymentDeclined         = errors.New("payment declined")
	ErrTicketNotFound          = errors.New("ticket not found")
	// ErrConcurrentUpdate: the booking changed status between read and write
	// (e.g. two confirms raced); this request lost and did nothing.
	ErrConcurrentUpdate = errors.New("booking was modified by a concurrent request")
	// ErrTripNotBookable: the trip exists but can't take holds any more
	// (cancelled, completed, departed or inside the booking cutoff).
	ErrTripNotBookable = errors.New("trip is no longer bookable")
	// ErrTooManySeats: more than MaxSeatsPerBooking seats in one booking.
	ErrTooManySeats = errors.New("too many seats in one booking")
	// ErrTooManyActiveHolds: the passenger already has MaxActiveHoldsPerPassenger live holds.
	ErrTooManyActiveHolds = errors.New("too many active seat holds")
	// ErrTicketNotValidNow: outside the check-in window, or trip not boardable.
	ErrTicketNotValidNow = errors.New("ticket is not valid for check-in now")
	// ErrTicketTripCancelled: the ticket's trip was cancelled.
	ErrTicketTripCancelled = errors.New("ticket's trip was cancelled")
)
