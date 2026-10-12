// Package service implements the Identity & Access context's use cases
// (ARCHITECTURE.md §3.2) — application logic that orchestrates domain
// objects and ports, with no HTTP or SQL details of its own.
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/sl-bus/backend/internal/identity/domain"
	"github.com/sl-bus/backend/internal/platform/auth"
)

// TokenPair is what every successful auth flow (OTP verify, login, MFA
// verify, refresh) returns to the client.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	Role         domain.Role
	OperatorID   string
}

// TokenIssuer bundles the pieces needed to mint a full TokenPair: the JWT
// signer plus the refresh-token repository (refresh tokens are stored
// hashed so they can be revoked, AUTHENTICATION_DESIGN.md §4.3).
type TokenIssuer struct {
	jwt         *auth.JWTIssuer
	refreshRepo domain.RefreshTokenRepository
}

func NewTokenIssuer(jwt *auth.JWTIssuer, refreshRepo domain.RefreshTokenRepository) *TokenIssuer {
	return &TokenIssuer{jwt: jwt, refreshRepo: refreshRepo}
}

func (t *TokenIssuer) IssuePair(ctx context.Context, u *domain.User, deviceInfo string) (*TokenPair, error) {
	access, err := t.jwt.Issue(u.ID, u.Role, u.OperatorID, true)
	if err != nil {
		return nil, err
	}
	refresh, refreshHash, err := generateRefreshToken()
	if err != nil {
		return nil, err
	}
	if _, err := t.refreshRepo.Create(ctx, u.ID, refreshHash, deviceInfo, time.Now().Add(auth.RefreshTokenTTL)); err != nil {
		return nil, err
	}
	return &TokenPair{AccessToken: access, RefreshToken: refresh, Role: u.Role, OperatorID: u.OperatorID}, nil
}

func generateRefreshToken() (raw string, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, hashToken(raw), nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
