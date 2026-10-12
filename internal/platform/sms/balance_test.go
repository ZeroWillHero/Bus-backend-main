package sms

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type fakeFetcher struct {
	calls int
	st    AccountStatus
	err   error
}

func (f *fakeFetcher) AccountStatus(context.Context) (AccountStatus, error) {
	f.calls++
	return f.st, f.err
}

func newBalanceService(t *testing.T, f BalanceFetcher) (*BalanceService, *time.Time) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	s := NewBalanceService(f, rdb)
	clock := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }
	return s, &clock
}

func TestBalanceServiceCachesForTheFreshnessWindow(t *testing.T) {
	ctx := context.Background()
	f := &fakeFetcher{st: AccountStatus{Status: "active", CreditBalance: 924.45}}
	s, clock := newBalanceService(t, f)

	snap, stale, err := s.Get(ctx, false)
	if err != nil || stale || snap.MainBalanceLKR != 924.45 || snap.Status != "active" {
		t.Fatalf("first Get = %+v %v %v", snap, stale, err)
	}
	if _, _, _ = s.Get(ctx, false); f.calls != 1 {
		t.Fatalf("fresh cache still called the provider (%d calls)", f.calls)
	}
	if _, _, _ = s.Get(ctx, true); f.calls != 2 {
		t.Fatalf("forced refresh didn't call the provider (%d calls)", f.calls)
	}
	*clock = clock.Add(BalanceFreshFor + time.Second)
	if _, _, _ = s.Get(ctx, false); f.calls != 3 {
		t.Fatalf("expired cache didn't call the provider (%d calls)", f.calls)
	}
}

func TestBalanceServiceServesStaleOnProviderFailure(t *testing.T) {
	ctx := context.Background()
	f := &fakeFetcher{st: AccountStatus{Status: "active", CreditBalance: 100}}
	s, _ := newBalanceService(t, f)
	if _, _, err := s.Get(ctx, false); err != nil {
		t.Fatal(err)
	}
	f.err = ErrProviderUnavailable
	snap, stale, err := s.Get(ctx, true)
	if err != nil || !stale || snap.MainBalanceLKR != 100 {
		t.Fatalf("Get during outage = %+v stale=%v err=%v, want last known value flagged stale", snap, stale, err)
	}
}

func TestBalanceServiceUnavailableWithNothingCached(t *testing.T) {
	ctx := context.Background()
	s, _ := newBalanceService(t, &fakeFetcher{err: ErrProviderAuth})
	if _, _, err := s.Get(ctx, false); !errors.Is(err, ErrBalanceUnavailable) {
		t.Fatalf("err = %v, want ErrBalanceUnavailable", err)
	}
	var none *SMSlenzClient
	s2, _ := newBalanceService(t, none)
	if _, _, err := s2.Get(ctx, false); !errors.Is(err, ErrBalanceUnavailable) {
		t.Fatalf("unconfigured err = %v, want ErrBalanceUnavailable", err)
	}
}

func TestBalancePolicy(t *testing.T) {
	p := BalancePolicy{UnitPriceLKR: 0.59, LowThresholdLKR: 500}
	if n := p.EstimatedMessages(BalanceSnapshot{MainBalanceLKR: 924.45}); n != 1566 {
		t.Fatalf("EstimatedMessages = %d, want 1566", n)
	}
	cases := []struct {
		snap BalanceSnapshot
		low  bool
	}{
		{BalanceSnapshot{Status: "active", MainBalanceLKR: 924.45}, false},
		{BalanceSnapshot{Status: "active", MainBalanceLKR: 499}, true},
		{BalanceSnapshot{Status: "suspended", MainBalanceLKR: 5000}, true},
	}
	for _, c := range cases {
		if got := p.IsLow(c.snap); got != c.low {
			t.Errorf("IsLow(%+v) = %v, want %v", c.snap, got, c.low)
		}
	}
}
