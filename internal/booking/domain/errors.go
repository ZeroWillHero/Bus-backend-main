package domain

import "errors"

var (
	ErrSeatUnavailable         = errors.New("seat(s) unavailable or already held")
	ErrHoldExpired             = errors.New("seat hold has expired")
	ErrBookingNotFound         = errors.New("booking not found")
	ErrInvalidStatusTransition = errors.New("invalid booking status transition")
	ErrAlreadyCheckedIn        = errors.New("ticket already checked in")
	ErrInvalidQRToken          = errors.New("invalid or tampered QR token")
	ErrUnknownSeat             = errors.New("seat does not exist on this trip or trip is not bookable")
	ErrDuplicateSeat           = errors.New("the same seat was selected more than once")
	ErrNoSeatsSelected         = errors.New("no seats selected")
	ErrPaymentDeclined         = errors.New("payment declined")
	ErrTicketNotFound          = errors.New("ticket not found")
)
