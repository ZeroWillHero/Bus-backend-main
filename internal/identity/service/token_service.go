package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/sl-bus/backend/internal/identity/domain"
	"github.com/sl-bus/backend/internal/platform/auth"
)

type TokenService struct {
	refreshRepo    domain.RefreshTokenRepository
	userRepo       domain.UserRepository
	operatorStatus OperatorStatusChecker
	jwt            *auth.JWTIssuer
	tokens         *TokenIssuer
}

func NewTokenService(refreshRepo domain.RefreshTokenRepository, userRepo domain.UserRepository, operatorStatus OperatorStatusChecker, jwt *auth.JWTIssuer, tokens *TokenIssuer) *TokenService {
	return &TokenService{refreshRepo: refreshRepo, userRepo: userRepo, operatorStatus: operatorStatus, jwt: jwt, tokens: tokens}
}

// RefreshTokens rotates a refresh token: the presented token is revoked
// and a new pair is issued in its place. Presenting a token that's
// already been rotated (or revoked, e.g. by logout) but hasn't expired is
// treated as reuse/theft (AUTHENTICATION_DESIGN.md §4.3): every session
// the user has is revoked and ErrTokenInvalid returned. The revoke is
// conditional, so of two concurrent refreshes with one token exactly one
// wins; the loser is handled as reuse too.
//
// The account is re-checked on every refresh: a deleted user's tokens
// are gone (ON DELETE CASCADE), a locked account gets ErrAccountLocked and
// a suspended operator's staff get ErrOperatorSuspended — in both cases
// the token is left as it was.
func (s *TokenService) RefreshTokens(ctx context.Context, rawToken, deviceInfo string) (*TokenPair, error) {
	record, err := s.refreshRepo.FindByHash(ctx, hashToken(rawToken))
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrTokenInvalid
		}
		return nil, err
	}
	now := time.Now()
	if !now.Before(record.ExpiresAt) {
		return nil, domain.ErrTokenInvalid
	}
	if record.RevokedAt != nil {
		return nil, s.handleReuse(ctx, record)
	}

	user, err := s.userRepo.FindByID(ctx, record.UserID)
	if err != nil {
		if err == domain.ErrUserNotFound {
			return nil, domain.ErrTokenInvalid
		}
		return nil, err
	}
	if user.IsLocked(now) {
		return nil, domain.ErrAccountLocked
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

	revoked, err := s.refreshRepo.Revoke(ctx, record.ID)
	if err != nil {
		return nil, err
	}
	if !revoked {
		// Someone else rotated (or revoked) it between our read and now.
		return nil, s.handleReuse(ctx, record)
	}
	return s.tokens.IssuePair(ctx, user, deviceInfo)
}

// handleReuse revokes every refresh token of the reused token's owner and
// returns the error the caller should surface.
func (s *TokenService) handleReuse(ctx context.Context, record *domain.RefreshToken) error {
	slog.WarnContext(ctx, "refresh token reuse detected; revoking all sessions", "user_id", record.UserID, "token_id", record.ID)
	if err := s.refreshRepo.RevokeAllForUser(ctx, record.UserID); err != nil {
		return err
	}
	return domain.ErrTokenInvalid
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
	_, err = s.refreshRepo.Revoke(ctx, record.ID)
	return err
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
			_, err := s.refreshRepo.Revoke(ctx, sessionID)
			return err
		}
	}
	return domain.ErrSessionNotOwned
}
