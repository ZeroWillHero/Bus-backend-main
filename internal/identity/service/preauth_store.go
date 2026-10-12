package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// PreAuthStore holds short-lived, single-use tokens that carry a user
// through the gap between "password verified" and "MFA verified".
// RedisPreAuthStore is the production implementation, shared by every API
// instance and surviving restarts; MemoryPreAuthStore is for tests.
type PreAuthStore interface {
	Issue(ctx context.Context, userID, deviceFingerprint, deviceInfo string) (string, error)
	// Peek reads the token without consuming it or counting an attempt —
	// used by MFA enrollment, which must leave the token valid for the
	// /mfa/verify call that follows.
	Peek(ctx context.Context, token string) (userID string, ok bool, err error)
	// SetPendingMFASecret attaches a not-yet-confirmed TOTP secret to token.
	SetPendingMFASecret(ctx context.Context, token, secret string) (bool, error)
	// BeginAttempt counts one MFA code attempt against token and returns its
	// entry, or false once the token is expired or out of attempts. The count
	// is taken before the code is checked, so concurrent requests can't get
	// past the cap. A successful check must then Consume the token.
	BeginAttempt(ctx context.Context, token string) (PreAuthEntry, bool, error)
	// Consume returns and deletes the entry — single use.
	Consume(ctx context.Context, token string) (PreAuthEntry, bool, error)
}

type PreAuthEntry struct {
	UserID            string
	DeviceFingerprint string
	DeviceInfo        string

	// PendingMFASecret is the TOTP secret from /mfa/enroll, held here
	// until a code generated from it verifies — so an enrollment the user
	// abandons before adding it to their authenticator never locks them
	// out on their next login.
	PendingMFASecret string
	Attempts         int
}

const preAuthTokenTTL = 5 * time.Minute

// maxPreAuthAttempts caps MFA codes tried against one pre-auth token, so
// a mistyped code can be retried without letting the token be used to
// brute-force the 6-digit code. Minting fresh tokens doesn't reset the
// cap on the account: MFAService also keeps a per-user failure count.
const maxPreAuthAttempts = 5

func newPreAuthToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// MemoryPreAuthStore is a single-process PreAuthStore for tests.
type MemoryPreAuthStore struct {
	mu      sync.Mutex
	entries map[string]memPreAuth
}

type memPreAuth struct {
	PreAuthEntry
	expiresAt time.Time
}

func NewMemoryPreAuthStore() *MemoryPreAuthStore {
	return &MemoryPreAuthStore{entries: make(map[string]memPreAuth)}
}

func (s *MemoryPreAuthStore) Issue(_ context.Context, userID, deviceFingerprint, deviceInfo string) (string, error) {
	token, err := newPreAuthToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, e := range s.entries {
		if now.After(e.expiresAt) {
			delete(s.entries, k)
		}
	}
	s.entries[token] = memPreAuth{
		PreAuthEntry: PreAuthEntry{UserID: userID, DeviceFingerprint: deviceFingerprint, DeviceInfo: deviceInfo},
		expiresAt:    now.Add(preAuthTokenTTL),
	}
	return token, nil
}

func (s *MemoryPreAuthStore) live(token string) (memPreAuth, bool) {
	e, found := s.entries[token]
	if !found {
		return memPreAuth{}, false
	}
	if time.Now().After(e.expiresAt) {
		delete(s.entries, token)
		return memPreAuth{}, false
	}
	return e, true
}

func (s *MemoryPreAuthStore) Peek(_ context.Context, token string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.live(token)
	return e.UserID, ok, nil
}

func (s *MemoryPreAuthStore) SetPendingMFASecret(_ context.Context, token, secret string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.live(token)
	if !ok {
		return false, nil
	}
	e.PendingMFASecret = secret
	s.entries[token] = e
	return true, nil
}

func (s *MemoryPreAuthStore) BeginAttempt(_ context.Context, token string) (PreAuthEntry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.live(token)
	if !ok {
		return PreAuthEntry{}, false, nil
	}
	if e.Attempts >= maxPreAuthAttempts {
		delete(s.entries, token)
		return PreAuthEntry{}, false, nil
	}
	e.Attempts++
	s.entries[token] = e
	return e.PreAuthEntry, true, nil
}

func (s *MemoryPreAuthStore) Consume(_ context.Context, token string) (PreAuthEntry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.live(token)
	if !ok {
		return PreAuthEntry{}, false, nil
	}
	delete(s.entries, token)
	return e.PreAuthEntry, true, nil
}

// RedisPreAuthStore keeps each token as a hash under preauth:<sha256(token)>
// (the raw token never appears in the keyspace) with a TTL, and uses Lua
// scripts so attempt counting and single-use consumption are atomic across
// API instances.
type RedisPreAuthStore struct {
	rdb redis.UniversalClient
}

func NewRedisPreAuthStore(rdb redis.UniversalClient) *RedisPreAuthStore {
	return &RedisPreAuthStore{rdb: rdb}
}

func preAuthKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "preauth:" + hex.EncodeToString(sum[:])
}

func (s *RedisPreAuthStore) Issue(ctx context.Context, userID, deviceFingerprint, deviceInfo string) (string, error) {
	token, err := newPreAuthToken()
	if err != nil {
		return "", err
	}
	key := preAuthKey(token)
	_, err = s.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, key, "u", userID, "fp", deviceFingerprint, "di", deviceInfo, "n", 0)
		p.PExpire(ctx, key, preAuthTokenTTL)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("preauth: issue: %w", err)
	}
	return token, nil
}

func (s *RedisPreAuthStore) Peek(ctx context.Context, token string) (string, bool, error) {
	userID, err := s.rdb.HGet(ctx, preAuthKey(token), "u").Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("preauth: peek: %w", err)
	}
	return userID, true, nil
}

var setPendingScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then return 0 end
redis.call('HSET', KEYS[1], 'ps', ARGV[1])
return 1`)

func (s *RedisPreAuthStore) SetPendingMFASecret(ctx context.Context, token, secret string) (bool, error) {
	n, err := setPendingScript.Run(ctx, s.rdb, []string{preAuthKey(token)}, secret).Int()
	if err != nil {
		return false, fmt.Errorf("preauth: set pending secret: %w", err)
	}
	return n == 1, nil
}

var beginAttemptScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then return false end
local n = redis.call('HINCRBY', KEYS[1], 'n', 1)
if n > tonumber(ARGV[1]) then
  redis.call('DEL', KEYS[1])
  return false
end
return redis.call('HGETALL', KEYS[1])`)

func (s *RedisPreAuthStore) BeginAttempt(ctx context.Context, token string) (PreAuthEntry, bool, error) {
	return s.runEntryScript(ctx, beginAttemptScript, token, maxPreAuthAttempts)
}

var consumeScript = redis.NewScript(`
local v = redis.call('HGETALL', KEYS[1])
if #v == 0 then return false end
redis.call('DEL', KEYS[1])
return v`)

func (s *RedisPreAuthStore) Consume(ctx context.Context, token string) (PreAuthEntry, bool, error) {
	return s.runEntryScript(ctx, consumeScript, token)
}

func (s *RedisPreAuthStore) runEntryScript(ctx context.Context, script *redis.Script, token string, args ...any) (PreAuthEntry, bool, error) {
	res, err := script.Run(ctx, s.rdb, []string{preAuthKey(token)}, args...).StringSlice()
	if errors.Is(err, redis.Nil) {
		return PreAuthEntry{}, false, nil
	}
	if err != nil {
		return PreAuthEntry{}, false, fmt.Errorf("preauth: %w", err)
	}
	fields := make(map[string]string, len(res)/2)
	for i := 0; i+1 < len(res); i += 2 {
		fields[res[i]] = res[i+1]
	}
	attempts, _ := strconv.Atoi(fields["n"])
	return PreAuthEntry{
		UserID:            fields["u"],
		DeviceFingerprint: fields["fp"],
		DeviceInfo:        fields["di"],
		PendingMFASecret:  fields["ps"],
		Attempts:          attempts,
	}, true, nil
}
