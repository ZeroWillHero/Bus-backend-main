package domain

import "errors"

var (
	ErrSeatUnavailable         = errors.New("seat(s) unavailable or already held")
	ErrHoldExpired             = errors.New("seat hold has expired")
	ErrBookingNotFound         = errors.New("booking not found")
	ErrInvalidStatusTransition = errors.New("invalid booking status transition")
	ErrAlreadyCheckedIn        = errors.New("ticket already checked in")
	ErrInvalidQRToken          = errors.New("invalid or tampered QR token")
)
