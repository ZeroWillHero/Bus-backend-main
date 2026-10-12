package service

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/sl-bus/backend/internal/identity/domain"
	"github.com/sl-bus/backend/internal/platform/auth"
)

func newRedisPreAuthStore(t *testing.T) (*RedisPreAuthStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewRedisPreAuthStore(rdb), mr
}

// Both implementations must behave the same: the memory store is what the
// service tests exercise, the Redis one is what production runs.
func TestPreAuthStores(t *testing.T) {
	redisStore, _ := newRedisPreAuthStore(t)
	stores := map[string]PreAuthStore{"memory": NewMemoryPreAuthStore(), "redis": redisStore}
	for name, s := range stores {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			token := issue(t, s, "u1", "device-1", "ua")

			if uid, ok, err := s.Peek(ctx, token); err != nil || !ok || uid != "u1" {
				t.Fatalf("Peek = %q %v %v, want u1", uid, ok, err)
			}
			if set, err := s.SetPendingMFASecret(ctx, token, "SECRET"); err != nil || !set {
				t.Fatalf("SetPendingMFASecret = %v %v", set, err)
			}
			for i := 1; i <= maxPreAuthAttempts; i++ {
				e, ok, err := s.BeginAttempt(ctx, token)
				if err != nil || !ok {
					t.Fatalf("attempt %d = %v %v, want allowed", i, ok, err)
				}
				if e.UserID != "u1" || e.DeviceFingerprint != "device-1" || e.DeviceInfo != "ua" || e.PendingMFASecret != "SECRET" || e.Attempts != i {
					t.Fatalf("attempt %d entry = %+v", i, e)
				}
			}
			if _, ok, _ := s.BeginAttempt(ctx, token); ok {
				t.Fatal("attempt beyond the cap was allowed")
			}
			if _, ok, _ := s.Peek(ctx, token); ok {
				t.Fatal("token still exists after the cap was exceeded")
			}

			token = issue(t, s, "u2", "", "")
			if e, ok, err := s.Consume(ctx, token); err != nil || !ok || e.UserID != "u2" {
				t.Fatalf("Consume = %+v %v %v", e, ok, err)
			}
			if _, ok, _ := s.Consume(ctx, token); ok {
				t.Fatal("token consumed twice")
			}
			if set, _ := s.SetPendingMFASecret(ctx, "unknown", "X"); set {
				t.Fatal("SetPendingMFASecret succeeded on an unknown token")
			}
		})
	}
}

func TestRedisPreAuthTokensExpire(t *testing.T) {
	s, mr := newRedisPreAuthStore(t)
	token := issue(t, s, "u1", "", "")
	mr.FastForward(preAuthTokenTTL + time.Second)
	if _, ok, _ := s.Peek(context.Background(), token); ok {
		t.Fatal("token still valid after its TTL")
	}
}

// Fresh pre-auth tokens must not reset the account's MFA failure budget —
// otherwise a caller who knows the password gets unlimited code guesses.
func TestVerifyMFALocksTheAccountAcrossTokens(t *testing.T) {
	ctx := context.Background()
	secret, _, _ := auth.GenerateTOTPSecret("ann@example.com")
	f := newMFAFixture(&domain.User{ID: "u1", Email: "ann@example.com", Role: domain.RolePassenger, MFATOTPSecret: secret})

	for i := 0; i < MaxMFAFailures; i++ {
		token := issue(t, f.preAuth, "u1", "", "test")
		if _, err := f.svc.VerifyMFA(ctx, token, wrongCode(t, secret)); err != domain.ErrInvalidMFACode {
			t.Fatalf("failure %d error = %v, want ErrInvalidMFACode", i+1, err)
		}
	}
	token := issue(t, f.preAuth, "u1", "", "test")
	if _, err := f.svc.VerifyMFA(ctx, token, currentCode(t, secret)); err != domain.ErrAccountLocked {
		t.Fatalf("right code after %d failures error = %v, want ErrAccountLocked", MaxMFAFailures, err)
	}
}
