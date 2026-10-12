package sms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// BalanceFetcher is the slice of SMSlenzClient the balance service needs.
type BalanceFetcher interface {
	AccountStatus(ctx context.Context) (AccountStatus, error)
}

// BalanceSnapshot is the last balance read from the provider. It holds
// figures only — never credentials — so it is safe to cache and serve.
type BalanceSnapshot struct {
	Status         string             `json:"status"`
	MainBalanceLKR float64            `json:"mainBalanceLkr"`
	SenderBalances map[string]float64 `json:"senderBalances"`
	CheckedAt      time.Time          `json:"checkedAt"`
}

// ErrBalanceUnavailable means there is no balance to show: the provider
// isn't configured or couldn't be reached, and nothing is cached.
var ErrBalanceUnavailable = errors.New("sms: balance unavailable")

// BalanceFreshFor is how long a cached balance is served without asking
// the provider again, so opening the admin dashboard doesn't hit SMSlenz
// on every load.
const BalanceFreshFor = 60 * time.Second

const balanceCacheKey = "sms:balance"

// BalanceService serves the SMS credit balance from a Redis cache shared by
// the API and the worker (whose periodic check writes through it). The
// last snapshot is kept indefinitely so an outage can still show the last
// known figure, flagged stale.
type BalanceService struct {
	fetcher BalanceFetcher // nil when SMSlenz isn't configured
	rdb     redis.UniversalClient
	now     func() time.Time
}

func NewBalanceService(fetcher BalanceFetcher, rdb redis.UniversalClient) *BalanceService {
	// A typed-nil *SMSlenzClient must count as "not configured".
	if c, ok := fetcher.(*SMSlenzClient); ok && c == nil {
		fetcher = nil
	}
	return &BalanceService{fetcher: fetcher, rdb: rdb, now: time.Now}
}

// Get returns the balance and whether it is stale (the provider couldn't
// be reached, so this is the last known value). forceRefresh skips the
// freshness window.
func (s *BalanceService) Get(ctx context.Context, forceRefresh bool) (BalanceSnapshot, bool, error) {
	cached, hasCached, err := s.cached(ctx)
	if err != nil {
		return BalanceSnapshot{}, false, err
	}
	if hasCached && !forceRefresh && s.now().Sub(cached.CheckedAt) < BalanceFreshFor {
		return cached, false, nil
	}
	if s.fetcher == nil {
		if hasCached {
			return cached, true, nil
		}
		return BalanceSnapshot{}, false, fmt.Errorf("%w: SMS provider not configured", ErrBalanceUnavailable)
	}

	st, err := s.fetcher.AccountStatus(ctx)
	if err != nil {
		if hasCached {
			return cached, true, nil
		}
		return BalanceSnapshot{}, false, fmt.Errorf("%w: %v", ErrBalanceUnavailable, err)
	}
	snap := BalanceSnapshot{
		Status:         st.Status,
		MainBalanceLKR: st.CreditBalance,
		SenderBalances: st.SenderBalances,
		CheckedAt:      s.now().UTC(),
	}
	if snap.SenderBalances == nil {
		snap.SenderBalances = map[string]float64{}
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return BalanceSnapshot{}, false, err
	}
	if err := s.rdb.Set(ctx, balanceCacheKey, raw, 0).Err(); err != nil {
		return BalanceSnapshot{}, false, fmt.Errorf("sms: cache balance: %w", err)
	}
	return snap, false, nil
}

func (s *BalanceService) cached(ctx context.Context) (BalanceSnapshot, bool, error) {
	raw, err := s.rdb.Get(ctx, balanceCacheKey).Bytes()
	if errors.Is(err, redis.Nil) {
		return BalanceSnapshot{}, false, nil
	}
	if err != nil {
		return BalanceSnapshot{}, false, fmt.Errorf("sms: read cached balance: %w", err)
	}
	var snap BalanceSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		// A corrupt entry is treated as missing; the next fetch overwrites it.
		return BalanceSnapshot{}, false, nil
	}
	return snap, true, nil
}
