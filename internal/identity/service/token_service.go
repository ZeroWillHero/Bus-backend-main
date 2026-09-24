package service

import (
	"context"
	"time"

	"github.com/sl-bus/backend/internal/identity/domain"
	"github.com/sl-bus/backend/internal/platform/auth"
)

type TokenService struct {
	refreshRepo domain.RefreshTokenRepository
	userRepo    domain.UserRepository
	jwt         *auth.JWTIssuer
	tokens      *TokenIssuer
}

func NewTokenService(refreshRepo domain.RefreshTokenRepository, userRepo domain.UserRepository, jwt *auth.JWTIssuer, tokens *TokenIssuer) *TokenService {
	return &TokenService{refreshRepo: refreshRepo, userRepo: userRepo, jwt: jwt, tokens: tokens}
}

// RefreshTokens rotates a refresh token: the presented token is revoked
// and a new pair is issued in its place. Presenting a token that's
// already been rotated (or revoked) is treated as a reuse/theft signal
// (AUTHENTICATION_DESIGN.md §4.3) — the caller (http layer) is expected to
// alert on ErrTokenInvalid for this endpoint specifically.
func (s *TokenService) RefreshTokens(ctx context.Context, rawToken, deviceInfo string) (*TokenPair, error) {
	record, err := s.refreshRepo.FindByHash(ctx, hashToken(rawToken))
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrTokenInvalid
		}
		return nil, err
	}
	now := time.Now()
	if !record.Active(now) {
		return nil, domain.ErrTokenInvalid
	}
	if err := s.refreshRepo.Revoke(ctx, record.ID); err != nil {
		return nil, err
	}

	user, err := s.userRepo.FindByID(ctx, record.UserID)
	if err != nil {
		return nil, err
	}
	return s.tokens.IssuePair(ctx, user, deviceInfo)
}

// Logout revokes a single refresh token (this device's session only).
func (s *TokenService) Logout(ctx context.Context, rawToken string) error {
	record, err := s.refreshRepo.FindByHash(ctx, hashToken(rawToken))
	if err != nil {
		if err == domain.ErrNotFound {
			return nil
		}
		return err
	}
	return s.refreshRepo.Revoke(ctx, record.ID)
}

// SessionSummary is what GET /v1/auth/sessions returns per active
// refresh token — never the token value itself (FR-AUTH-5).
type SessionSummary struct {
	ID         string
	DeviceInfo string
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

func (s *TokenService) ListSessions(ctx context.Context, userID string) ([]SessionSummary, error) {
	tokens, err := s.refreshRepo.ListActiveForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]SessionSummary, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, SessionSummary{ID: t.ID, DeviceInfo: t.DeviceInfo, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt})
	}
	return out, nil
}

// RevokeSession revokes one of userID's own sessions. The caller (http
// layer) is responsible for confirming sessionID belongs to userID before
// calling this — see the handler's ownership check, since the repository
// layer has no per-user filter on Revoke by design (it's a small,
// low-value indirection to add given the handler already has both IDs).
func (s *TokenService) RevokeSession(ctx context.Context, userID string, sessionID string) error {
	sessions, err := s.refreshRepo.ListActiveForUser(ctx, userID)
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		if sess.ID == sessionID {
			return s.refreshRepo.Revoke(ctx, sessionID)
		}
	}
	return domain.ErrSessionNotOwned
}
