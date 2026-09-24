// Package domain holds the Identity & Access context's pure business
// logic (ARCHITECTURE.md §3.2) — no HTTP or SQL imports, so it's
// unit-testable without a database.
package domain

import (
	"context"
	"time"

	"github.com/sl-bus/backend/internal/platform/auth"
)

// Role re-exports platform/auth's Role — the vocabulary is shared kernel
// across every bounded context that gates on it, not identity-specific.
type Role = auth.Role

const (
	RolePassenger     = auth.RolePassenger
	RoleOperatorStaff = auth.RoleOperatorStaff
	RoleOperatorAdmin = auth.RoleOperatorAdmin
	RoleFinanceAdmin  = auth.RoleFinanceAdmin
	RoleSupport       = auth.RoleSupport
	RolePlatformAdmin = auth.RolePlatformAdmin
)

// RequiresMFA reports whether role must complete TOTP MFA to obtain a
// fully-authenticated session (AUTHENTICATION_DESIGN.md §1 — every role
// except passenger, which is passwordless, and operator_staff, whose
// blast radius doesn't justify the friction).
func RequiresMFA(role Role) bool {
	switch role {
	case RolePassenger, RoleOperatorStaff:
		return false
	default:
		return true
	}
}

// TrustedDeviceEligible reports whether role may skip a satisfied MFA
// challenge on a previously-trusted device (FR-AUTH-4). Platform Super
// Admin is deliberately excluded regardless of device trust.
func TrustedDeviceEligible(role Role) bool {
	return RequiresMFA(role) && role != RolePlatformAdmin
}

// User is the Identity & Access aggregate root.
type User struct {
	ID                  string
	Phone               string
	Email               string
	PasswordHash        string
	Role                Role
	OperatorID          string
	MFATOTPSecret       string
	FailedLoginAttempts int
	LockedUntil         *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// IsLocked reports whether the account is currently within a lockout
// window (AUTHENTICATION_DESIGN.md §6 — 5 consecutive failed attempts).
func (u *User) IsLocked(now time.Time) bool {
	return u.LockedUntil != nil && now.Before(*u.LockedUntil)
}

// MaxFailedLoginAttempts before an account is temporarily locked.
const MaxFailedLoginAttempts = 5

// UserRepository is the port the identity service layer depends on;
// repository/ provides the Postgres adapter.
type UserRepository interface {
	FindByEmail(ctx context.Context, email string) (*User, error)
	FindByPhone(ctx context.Context, phone string) (*User, error)
	FindByID(ctx context.Context, id string) (*User, error)
	Create(ctx context.Context, u *User) (*User, error)
	CreatePassenger(ctx context.Context, phone string) (*User, error)
	IncrementFailedAttempts(ctx context.Context, id string) (*User, error)
	SetLockedUntil(ctx context.Context, id string, until time.Time) error
	ResetLockout(ctx context.Context, id string) error
	SetMFASecret(ctx context.Context, id string, secret string) error
	UpdatePassword(ctx context.Context, id string, passwordHash string) error
}
