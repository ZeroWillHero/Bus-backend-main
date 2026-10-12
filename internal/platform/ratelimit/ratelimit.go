// Package ratelimit provides fixed-window counters shared by every API
// instance (Redis in production), replacing the per-process in-memory
// limiters that reset on restart and diverge across replicas.
package ratelimit

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Counter counts events per key within a fixed window that starts at the
// key's first event.
type Counter interface {
	// Incr records one event and returns the count in the current window.
	Incr(ctx context.Context, key string, window time.Duration) (int64, error)
	// Count returns the count in the current window without recording one.
	Count(ctx context.Context, key string) (int64, error)
	// Reset clears the key's window.
	Reset(ctx context.Context, key string) error
}

// RedisCounter is the production Counter.
type RedisCounter struct {
	rdb    redis.UniversalClient
	prefix string
}

func NewRedisCounter(rdb redis.UniversalClient) *RedisCounter {
	return &RedisCounter{rdb: rdb, prefix: "rl:"}
}

// incrScript sets the expiry on the first increment only, atomically, so a
// crash between INCR and EXPIRE can't leave a counter that never expires.
var incrScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return n`)

func (c *RedisCounter) Incr(ctx context.Context, key string, window time.Duration) (int64, error) {
	return incrScript.Run(ctx, c.rdb, []string{c.prefix + key}, window.Milliseconds()).Int64()
}

func (c *RedisCounter) Count(ctx context.Context, key string) (int64, error) {
	n, err := c.rdb.Get(ctx, c.prefix+key).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return n, err
}

func (c *RedisCounter) Reset(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, c.prefix+key).Err()
}

// MemoryCounter is a single-process Counter for tests and local tools.
type MemoryCounter struct {
	mu      sync.Mutex
	entries map[string]memEntry
	now     func() time.Time
}

type memEntry struct {
	n       int64
	expires time.Time
}

func NewMemoryCounter() *MemoryCounter {
	return &MemoryCounter{entries: make(map[string]memEntry), now: time.Now}
}

func (c *MemoryCounter) Incr(_ context.Context, key string, window time.Duration) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	e, ok := c.entries[key]
	if !ok || !now.Before(e.expires) {
		e = memEntry{expires: now.Add(window)}
	}
	e.n++
	c.entries[key] = e
	return e.n, nil
}

func (c *MemoryCounter) Count(_ context.Context, key string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || !c.now().Before(e.expires) {
		return 0, nil
	}
	return e.n, nil
}

func (c *MemoryCounter) Reset(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
	return nil
}
