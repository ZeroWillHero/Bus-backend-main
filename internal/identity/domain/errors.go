package domain

import "errors"

var (
	ErrInvalidCredentials = errors.New("identity: invalid credentials")
	ErrAccountLocked      = errors.New("identity: account locked")
	ErrMFARequired        = errors.New("identity: mfa challenge required")
	ErrInvalidMFACode     = errors.New("identity: invalid mfa code")
	ErrOTPExpired         = errors.New("identity: otp expired")
	ErrOTPRateLimited     = errors.New("identity: otp rate limited")
	ErrInvalidOTP         = errors.New("identity: invalid otp")
	ErrTokenInvalid       = errors.New("identity: refresh token invalid")
	ErrTokenExpired       = errors.New("identity: refresh token expired")
	ErrUserNotFound       = errors.New("identity: user not found")
	ErrSessionNotOwned    = errors.New("identity: session does not belong to caller")

	// ErrNotFound is the generic "no row" signal repositories return;
	// callers translate it into a context-specific error
	// (ErrUserNotFound, ErrInvalidOTP, etc.) since the same repository
	// helper (mapNotFound) is shared across entity types.
	ErrNotFound = errors.New("identity: not found")
)
