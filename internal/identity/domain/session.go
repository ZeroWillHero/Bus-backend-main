package domain

import (
	"context"
	"time"
)

type RefreshToken struct {
	ID         string
	UserID     string
	TokenHash  string
	DeviceInfo string
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

func (t *RefreshToken) Active(now time.Time) bool {
	return t.RevokedAt == nil && now.Before(t.ExpiresAt)
}

type RefreshTokenRepository interface {
	Create(ctx context.Context, userID, tokenHash, deviceInfo string, expiresAt time.Time) (*RefreshToken, error)
	FindByHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	Revoke(ctx context.Context, id string) error
	RevokeAllForUser(ctx context.Context, userID string) error
	ListActiveForUser(ctx context.Context, userID string) ([]*RefreshToken, error)
}

type TrustedDevice struct {
	ID                string
	UserID            string
	DeviceFingerprint string
	TrustedUntil      time.Time
}

func (d *TrustedDevice) Trusted(now time.Time) bool {
	return now.Before(d.TrustedUntil)
}

// TrustedDeviceWindow is the 30-day MFA-skip window (NFR-50).
const TrustedDeviceWindow = 30 * 24 * time.Hour

type TrustedDeviceRepository interface {
	Find(ctx context.Context, userID, deviceFingerprint string) (*TrustedDevice, error)
	Upsert(ctx context.Context, userID, deviceFingerprint string, trustedUntil time.Time) (*TrustedDevice, error)
}
