package service

import (
	"context"
	"strings"

	"github.com/sl-bus/backend/internal/identity/domain"
	"github.com/sl-bus/backend/internal/platform/auth"
)

// MinPasswordLength follows NIST 800-63B guidance (length over forced
// complexity rules) per AUTHENTICATION_DESIGN.md §3.4 — the same bar as
// operator self-registration.
const MinPasswordLength = 10

// PassengerRegistrationService creates email + password passenger
// accounts. Unlike operator registration there is no tenant to create
// alongside the user, so it stays inside the identity context. The new
// account has no TOTP secret yet: its first /auth/login returns
// mfaEnrollNeeded, and enrollment finishes on /auth/mfa/verify.
type PassengerRegistrationService struct {
	userRepo      domain.UserRepository
	breachChecker auth.PasswordBreachChecker
}

func NewPassengerRegistrationService(userRepo domain.UserRepository, breachChecker auth.PasswordBreachChecker) *PassengerRegistrationService {
	return &PassengerRegistrationService{userRepo: userRepo, breachChecker: breachChecker}
}

func (s *PassengerRegistrationService) RegisterPassenger(ctx context.Context, email, password string) (*domain.User, error) {
	if len(password) < MinPasswordLength {
		return nil, domain.ErrPasswordTooShort
	}
	breached, err := s.breachChecker.IsBreached(ctx, password)
	if err != nil {
		return nil, err
	}
	if breached {
		return nil, domain.ErrPasswordBreached
	}
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	return s.userRepo.Create(ctx, &domain.User{
		// Login looks the account up by exact email, so store it in the
		// canonical form clients are expected to send.
		Email:        strings.ToLower(strings.TrimSpace(email)),
		PasswordHash: passwordHash,
		Role:         domain.RolePassenger,
	})
}
