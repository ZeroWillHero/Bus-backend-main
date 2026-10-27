package service

import (
	"context"
	"time"

	"github.com/sl-bus/backend/internal/identity/domain"
	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/ratelimit"
)

// MaxMFAFailures is how many wrong second-factor codes an account may
// submit within MFAFailureWindow, across all pre-auth tokens, before MFA
// (and therefore login) is refused until the window ends. The per-token cap
// alone isn't enough: a caller who knows the password can mint a fresh
// token for every 5 guesses.
const (
	MaxMFAFailures   = 10
	MFAFailureWindow = 24 * time.Hour
)

func mfaFailureKey(userID string) string { return "mfa_fail:" + userID }

func mfaLocked(ctx context.Context, failures ratelimit.Counter, userID string) (bool, error) {
	n, err := failures.Count(ctx, mfaFailureKey(userID))
	if err != nil {
		return false, err
	}
	return n >= MaxMFAFailures, nil
}

type MFAService struct {
	userRepo    domain.UserRepository
	trustedRepo domain.TrustedDeviceRepository
	preAuth     PreAuthStore
	failures    ratelimit.Counter
	tokens      *TokenIssuer
}

func NewMFAService(userRepo domain.UserRepository, trustedRepo domain.TrustedDeviceRepository, preAuth PreAuthStore, failures ratelimit.Counter, tokens *TokenIssuer) *MFAService {
	return &MFAService{userRepo: userRepo, trustedRepo: trustedRepo, preAuth: preAuth, failures: failures, tokens: tokens}
}

// EnrollMFA generates a TOTP secret for the user identified by
// preAuthToken — called once, right after a mandatory-MFA role's first
// login attempt returns mfaEnrollNeeded=true. It reads (Peek, not
// Consume) the pre-auth token, since the same token is still needed for
// the /mfa/verify call that completes login. The secret is only saved to
// the account once that call verifies a code from it, and an account that
// already has one can't enroll again: otherwise the password alone would
// be enough to replace the authenticator.
func (s *MFAService) EnrollMFA(ctx context.Context, preAuthToken string) (secret, provisioningURI string, err error) {
	userID, ok, err := s.preAuth.Peek(ctx, preAuthToken)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", domain.ErrTokenInvalid
	}
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return "", "", err
	}
	if user.MFATOTPSecret != "" {
		return "", "", domain.ErrMFAAlreadyEnrolled
	}
	accountLabel := user.Email
	if accountLabel == "" {
		accountLabel = user.Phone
	}
	secret, provisioningURI, err = auth.GenerateTOTPSecret(accountLabel)
	if err != nil {
		return "", "", err
	}
	set, err := s.preAuth.SetPendingMFASecret(ctx, preAuthToken, secret)
	if err != nil {
		return "", "", err
	}
	if !set {
		return "", "", domain.ErrTokenInvalid
	}
	return secret, provisioningURI, nil
}

// VerifyMFA completes a login's MFA challenge: validates totpCode against
// the pre-auth token issued by LoginService (finishing enrollment if one
// is pending), marks the device trusted for 30 days (unless the role is
// excluded, FR-AUTH-4), and issues full tokens. A wrong code leaves the
// token usable for up to maxPreAuthAttempts tries in total, and counts
// towards the account's MaxMFAFailures.
func (s *MFAService) VerifyMFA(ctx context.Context, preAuthToken, totpCode string) (*TokenPair, error) {
	attempt, ok, err := s.preAuth.BeginAttempt(ctx, preAuthToken)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, domain.ErrTokenInvalid
	}
	if locked, err := mfaLocked(ctx, s.failures, attempt.UserID); err != nil {
		return nil, err
	} else if locked {
		return nil, domain.ErrAccountLocked
	}

	user, err := s.userRepo.FindByID(ctx, attempt.UserID)
	if err != nil {
		return nil, err
	}
	secret := user.MFATOTPSecret
	enrolling := secret == ""
	if enrolling {
		secret = attempt.PendingMFASecret
	}
	invalid := func() (*TokenPair, error) {
		if _, err := s.failures.Incr(ctx, mfaFailureKey(user.ID), MFAFailureWindow); err != nil {
			return nil, err
		}
		return nil, domain.ErrInvalidMFACode
	}
	if secret == "" {
		return invalid()
	}
	step, ok := auth.MatchTOTPStep(secret, totpCode, time.Now())
	if !ok {
		return invalid()
	}
	// Replay protection: each time step's code is accepted once, and never
	// one older than the last accepted. A replay counts as a failure.
	if claimed, err := s.userRepo.ClaimMFAStep(ctx, user.ID, step); err != nil {
		return nil, err
	} else if !claimed {
		return invalid()
	}
	// Single use: a concurrent request that verified the same code first
	// has already consumed the token.
	if _, ok, err := s.preAuth.Consume(ctx, preAuthToken); err != nil {
		return nil, err
	} else if !ok {
		return nil, domain.ErrTokenInvalid
	}
	if err := s.failures.Reset(ctx, mfaFailureKey(user.ID)); err != nil {
		return nil, err
	}

	if enrolling {
		if err := s.userRepo.SetMFASecret(ctx, user.ID, secret); err != nil {
			return nil, err
		}
	}

	if domain.TrustedDeviceEligible(user.Role) && attempt.DeviceFingerprint != "" {
		if _, err := s.trustedRepo.Upsert(ctx, user.ID, attempt.DeviceFingerprint, time.Now().Add(domain.TrustedDeviceWindow)); err != nil {
			return nil, err
		}
	}

	return s.tokens.IssuePair(ctx, user, attempt.DeviceInfo)
}
