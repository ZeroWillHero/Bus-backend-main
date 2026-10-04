package service

import (
	"context"
	"time"

	"github.com/sl-bus/backend/internal/identity/domain"
	"github.com/sl-bus/backend/internal/platform/auth"
)

type MFAService struct {
	userRepo    domain.UserRepository
	trustedRepo domain.TrustedDeviceRepository
	preAuth     *PreAuthTokenStore
	tokens      *TokenIssuer
}

func NewMFAService(userRepo domain.UserRepository, trustedRepo domain.TrustedDeviceRepository, preAuth *PreAuthTokenStore, tokens *TokenIssuer) *MFAService {
	return &MFAService{userRepo: userRepo, trustedRepo: trustedRepo, preAuth: preAuth, tokens: tokens}
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
	userID, ok := s.preAuth.Peek(preAuthToken)
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
	if !s.preAuth.SetPendingMFASecret(preAuthToken, secret) {
		return "", "", domain.ErrTokenInvalid
	}
	return secret, provisioningURI, nil
}

// VerifyMFA completes a login's MFA challenge: validates totpCode against
// the pre-auth token issued by LoginService (finishing enrollment if one
// is pending), marks the device trusted for 30 days (unless the role is
// excluded, FR-AUTH-4), and issues full tokens. A wrong code leaves the
// token usable for up to maxPreAuthAttempts tries in total.
func (s *MFAService) VerifyMFA(ctx context.Context, preAuthToken, totpCode string) (*TokenPair, error) {
	attempt, ok := s.preAuth.BeginAttempt(preAuthToken)
	if !ok {
		return nil, domain.ErrTokenInvalid
	}

	user, err := s.userRepo.FindByID(ctx, attempt.userID)
	if err != nil {
		return nil, err
	}
	secret := user.MFATOTPSecret
	enrolling := secret == ""
	if enrolling {
		secret = attempt.pendingMFASecret
	}
	if secret == "" || !auth.VerifyTOTPCode(secret, totpCode) {
		return nil, domain.ErrInvalidMFACode
	}
	// Single use: a concurrent request that verified the same code first
	// has already consumed the token.
	if _, _, _, ok := s.preAuth.Consume(preAuthToken); !ok {
		return nil, domain.ErrTokenInvalid
	}

	if enrolling {
		if err := s.userRepo.SetMFASecret(ctx, user.ID, secret); err != nil {
			return nil, err
		}
	}

	if domain.TrustedDeviceEligible(user.Role) && attempt.deviceFingerprint != "" {
		if _, err := s.trustedRepo.Upsert(ctx, user.ID, attempt.deviceFingerprint, time.Now().Add(domain.TrustedDeviceWindow)); err != nil {
			return nil, err
		}
	}

	return s.tokens.IssuePair(ctx, user, attempt.deviceInfo)
}
