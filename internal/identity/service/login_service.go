package service

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/sl-bus/backend/internal/identity/domain"
	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/ratelimit"
)

// LockoutBaseDelay is the base of the exponential backoff applied after
// MaxFailedLoginAttempts (AUTHENTICATION_DESIGN.md §6).
const LockoutBaseDelay = 30 * time.Second

// LoginResult is either a full TokenPair (MFA not required, or already
// satisfied via a trusted device) or an MFA challenge the caller must
// complete via MFAService.VerifyMFA.
type LoginResult struct {
	Tokens          *TokenPair
	MFAChallenge    bool
	PreAuthToken    string
	MFAEnrollNeeded bool
}

// OperatorStatusChecker lets LoginService reject a login for an
// operator_admin/operator_staff user whose operator has been suspended
// (OPS_OPERATOR_SUSPENDED) — identity depends on this narrow port rather
// than importing internal/ops directly, keeping the dependency direction
// the context map specifies (ops depends on identity, not the reverse).
type OperatorStatusChecker interface {
	IsOperatorSuspended(ctx context.Context, operatorID string) (bool, error)
}

type LoginService struct {
	userRepo       domain.UserRepository
	trustedRepo    domain.TrustedDeviceRepository
	breachChecker  auth.PasswordBreachChecker
	operatorStatus OperatorStatusChecker
	preAuth        PreAuthStore
	mfaFailures    ratelimit.Counter
	tokens         *TokenIssuer
}

func NewLoginService(
	userRepo domain.UserRepository,
	trustedRepo domain.TrustedDeviceRepository,
	breachChecker auth.PasswordBreachChecker,
	operatorStatus OperatorStatusChecker,
	preAuth PreAuthStore,
	mfaFailures ratelimit.Counter,
	tokens *TokenIssuer,
) *LoginService {
	return &LoginService{
		userRepo:       userRepo,
		trustedRepo:    trustedRepo,
		breachChecker:  breachChecker,
		operatorStatus: operatorStatus,
		preAuth:        preAuth,
		mfaFailures:    mfaFailures,
		tokens:         tokens,
	}
}

func (s *LoginService) Login(ctx context.Context, email, password, deviceFingerprint, deviceInfo string) (*LoginResult, error) {
	user, err := s.userRepo.FindByEmail(ctx, email)
	if err != nil {
		if err == domain.ErrUserNotFound {
			return nil, domain.ErrInvalidCredentials
		}
		return nil, err
	}

	now := time.Now()
	if user.IsLocked(now) {
		return nil, domain.ErrAccountLocked
	}

	if !auth.VerifyPassword(user.PasswordHash, password) {
		updated, incErr := s.userRepo.IncrementFailedAttempts(ctx, user.ID)
		if incErr != nil {
			return nil, incErr
		}
		if updated.FailedLoginAttempts >= domain.MaxFailedLoginAttempts {
			backoff := time.Duration(math.Pow(2, float64(updated.FailedLoginAttempts-domain.MaxFailedLoginAttempts))) * LockoutBaseDelay
			if err := s.userRepo.SetLockedUntil(ctx, user.ID, now.Add(backoff)); err != nil {
				return nil, err
			}
		}
		return nil, domain.ErrInvalidCredentials
	}

	if user.OperatorID != "" && s.operatorStatus != nil {
		suspended, err := s.operatorStatus.IsOperatorSuspended(ctx, user.OperatorID)
		if err != nil {
			return nil, err
		}
		if suspended {
			return nil, ErrOperatorSuspended
		}
	}

	if err := s.userRepo.ResetLockout(ctx, user.ID); err != nil {
		return nil, err
	}

	if !domain.RequiresMFA(user.Role) {
		tokens, err := s.tokens.IssuePair(ctx, user, deviceInfo)
		if err != nil {
			return nil, err
		}
		return &LoginResult{Tokens: tokens}, nil
	}

	// A correct password must not reopen the second factor to guessing once
	// the account has burned through its MFA failure budget.
	if locked, err := mfaLocked(ctx, s.mfaFailures, user.ID); err != nil {
		return nil, err
	} else if locked {
		return nil, domain.ErrAccountLocked
	}

	if user.MFATOTPSecret == "" {
		// Mandatory-MFA role that hasn't enrolled yet (e.g. right after
		// self-registration) — caller must hit /auth/mfa/enroll next.
		preAuth, err := s.preAuth.Issue(ctx, user.ID, deviceFingerprint, deviceInfo)
		if err != nil {
			return nil, err
		}
		return &LoginResult{MFAChallenge: true, PreAuthToken: preAuth, MFAEnrollNeeded: true}, nil
	}

	if domain.TrustedDeviceEligible(user.Role) && deviceFingerprint != "" {
		device, err := s.trustedRepo.Find(ctx, user.ID, deviceFingerprint)
		if err != nil && err != domain.ErrNotFound {
			return nil, err
		}
		if device != nil && device.Trusted(now) {
			tokens, err := s.tokens.IssuePair(ctx, user, deviceInfo)
			if err != nil {
				return nil, err
			}
			return &LoginResult{Tokens: tokens}, nil
		}
	}

	preAuth, err := s.preAuth.Issue(ctx, user.ID, deviceFingerprint, deviceInfo)
	if err != nil {
		return nil, err
	}
	return &LoginResult{MFAChallenge: true, PreAuthToken: preAuth}, nil
}

// ErrOperatorSuspended surfaces as OPS_OPERATOR_SUSPENDED at the HTTP layer.
var ErrOperatorSuspended = errors.New("identity: operator suspended")
