//go:build legacy_otp

// Legacy phone-only OTP sign-in, excluded from the build: it issued full
// tokens with no password or second factor. Superseded by SMS as a second
// factor (docs/SMS_VERIFICATION_DESIGN.md). Safe to delete.

package service

import (
	"context"
	"crypto/subtle"
	"sync"
	"time"

	"github.com/sl-bus/backend/internal/identity/domain"
)

// OTPService implements the passenger phone+OTP flow
// (AUTHENTICATION_DESIGN.md §2).
type OTPService struct {
	otpRepo   domain.OTPRepository
	userRepo  domain.UserRepository
	notifier  domain.Notifier
	tokens    *TokenIssuer
	ipLimiter *ipRateLimiter
}

func NewOTPService(otpRepo domain.OTPRepository, userRepo domain.UserRepository, notifier domain.Notifier, tokens *TokenIssuer) *OTPService {
	return &OTPService{
		otpRepo:   otpRepo,
		userRepo:  userRepo,
		notifier:  notifier,
		tokens:    tokens,
		ipLimiter: newIPRateLimiter(),
	}
}

// RequestOTP sends a fresh OTP to phone, subject to the per-phone
// (NFR-49, 5/hour) and per-IP rate limits.
func (s *OTPService) RequestOTP(ctx context.Context, phone, clientIP string) error {
	since := time.Now().Add(-1 * time.Hour)
	count, err := s.otpRepo.CountSince(ctx, phone, since)
	if err != nil {
		return err
	}
	if count >= domain.OTPRequestLimitPerHour {
		return domain.ErrOTPRateLimited
	}
	if !s.ipLimiter.Allow(clientIP) {
		return domain.ErrOTPRateLimited
	}

	code, err := domain.GenerateOTP()
	if err != nil {
		return err
	}
	if _, err := s.otpRepo.Create(ctx, phone, hashOTP(code), time.Now().Add(domain.OTPTTL)); err != nil {
		return err
	}
	return s.notifier.SendOTP(ctx, phone, code)
}

// VerifyOTP validates code against the latest unconsumed OTP for phone,
// implicitly creating a passenger account on first successful verification
// (AUTHENTICATION_DESIGN.md §2.1).
func (s *OTPService) VerifyOTP(ctx context.Context, phone, code, deviceInfo string) (*TokenPair, error) {
	otp, err := s.otpRepo.FindLatestUnconsumed(ctx, phone)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrInvalidOTP
		}
		return nil, err
	}
	if time.Now().After(otp.ExpiresAt) {
		return nil, domain.ErrOTPExpired
	}
	if subtle.ConstantTimeCompare([]byte(hashOTP(code)), []byte(otp.CodeHash)) != 1 {
		return nil, domain.ErrInvalidOTP
	}
	if err := s.otpRepo.MarkConsumed(ctx, otp.ID); err != nil {
		return nil, err
	}

	user, err := s.userRepo.FindByPhone(ctx, phone)
	if err != nil {
		if err != domain.ErrUserNotFound {
			return nil, err
		}
		user, err = s.userRepo.CreatePassenger(ctx, phone)
		if err != nil {
			return nil, err
		}
	}

	return s.tokens.IssuePair(ctx, user, deviceInfo)
}

// ipRateLimiter is a single-instance, in-memory fixed-window limiter for
// the per-IP OTP request cap — a documented Phase 1 MVP tradeoff; a
// Redis-backed limiter is the natural upgrade once multiple API instances
// run behind a load balancer.
type ipRateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
}

func newIPRateLimiter() *ipRateLimiter {
	return &ipRateLimiter{requests: make(map[string][]time.Time)}
}

const ipRateLimitPerHour = 20

func (l *ipRateLimiter) Allow(ip string) bool {
	if ip == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := time.Now().Add(-1 * time.Hour)
	kept := l.requests[ip][:0]
	for _, t := range l.requests[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= ipRateLimitPerHour {
		l.requests[ip] = kept
		return false
	}
	l.requests[ip] = append(kept, time.Now())
	return true
}
