package service

import (
	"math"
	"testing"
	"time"

	"github.com/sl-bus/backend/internal/identity/domain"
)

func TestLockoutBackoff(t *testing.T) {
	m := domain.MaxFailedLoginAttempts
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{m, 30 * time.Second},
		{m + 1, time.Minute},
		{m + 6, 32 * time.Minute},
		{m + 7, MaxLockoutDelay}, // 64m capped
		{m + 40, MaxLockoutDelay},
		{m + 70, MaxLockoutDelay}, // 2^70 would overflow
		{math.MaxInt, MaxLockoutDelay},
	}
	for _, tc := range cases {
		if got := lockoutBackoff(tc.attempts); got != tc.want {
			t.Errorf("lockoutBackoff(%d) = %v, want %v", tc.attempts, got, tc.want)
		}
	}
}
