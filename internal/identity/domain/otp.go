package domain

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"
)

// OTPTTL matches the 5-minute window in AUTHENTICATION_DESIGN.md §2.2.
const OTPTTL = 5 * time.Minute

// OTPRequestLimitPerHour is the per-phone-number rate limit (NFR-49).
const OTPRequestLimitPerHour = 5

type OTPCode struct {
	ID         string
	Phone      string
	CodeHash   string
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	CreatedAt  time.Time
}

// GenerateOTP returns a cryptographically random 6-digit code.
func GenerateOTP() (string, error) {
	max := int64(1000000)
	n, err := randInt64(max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n), nil
}

func randInt64(max int64) (int64, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return 0, err
	}
	var n uint64
	for _, b := range buf {
		n = n<<8 | uint64(b)
	}
	return int64(n % uint64(max)), nil
}

type OTPRepository interface {
	Create(ctx context.Context, phone, codeHash string, expiresAt time.Time) (*OTPCode, error)
	FindLatestUnconsumed(ctx context.Context, phone string) (*OTPCode, error)
	MarkConsumed(ctx context.Context, id string) error
	CountSince(ctx context.Context, phone string, since time.Time) (int, error)
}
