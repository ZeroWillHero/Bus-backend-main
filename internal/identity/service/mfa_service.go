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

// EnrollMFA generates a TOTP secret and stores it for the user identified
// by preAuthToken — called once, right after a mandatory-MFA role's first
// login attempt returns mfaEnrollNeeded=true. It reads (Peek, not
// Consume) the pre-auth token, since the same token is still needed for
// the /mfa/verify call that completes login.
func (s *MFAService) EnrollMFA(ctx context.Context, preAuthToken string) (secret, provisioningURI string, err error) {
	userID, ok := s.preAuth.Peek(preAuthToken)
	if !ok {
		return "", "", domain.ErrTokenInvalid
	}
	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return "", "", err
	}
	accountLabel := user.Email
	if accountLabel == "" {
		accountLabel = user.Phone
	}
	secret, provisioningURI, err = auth.GenerateTOTPSecret(accountLabel)
	if err != nil {
		return "", "", err
	}
	if err := s.userRepo.SetMFASecret(ctx, userID, secret); err != nil {
		return "", "", err
	}
	return secret, provisioningURI, nil
}

// VerifyMFA completes a login's MFA challenge: validates totpCode against
// the pre-auth token issued by LoginService, marks the device trusted for
// 30 days (unless the role is excluded, FR-AUTH-4), and issues full
// tokens.
func (s *MFAService) VerifyMFA(ctx context.Context, preAuthToken, totpCode string) (*TokenPair, error) {
	userID, deviceFingerprint, deviceInfo, ok := s.preAuth.Consume(preAuthToken)
	if !ok {
		return nil, domain.ErrTokenInvalid
	}

	user, err := s.userRepo.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user.MFATOTPSecret == "" || !auth.VerifyTOTPCode(user.MFATOTPSecret, totpCode) {
		return nil, domain.ErrInvalidMFACode
	}

	if domain.TrustedDeviceEligible(user.Role) && deviceFingerprint != "" {
		if _, err := s.trustedRepo.Upsert(ctx, user.ID, deviceFingerprint, time.Now().Add(domain.TrustedDeviceWindow)); err != nil {
			return nil, err
		}
	}

	return s.tokens.IssuePair(ctx, user, deviceInfo)
}
