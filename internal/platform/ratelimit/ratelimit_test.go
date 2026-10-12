package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestCounters(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	mem := NewMemoryCounter()
	clock := time.Now()
	mem.now = func() time.Time { return clock }

	cases := map[string]struct {
		c       Counter
		advance func(time.Duration)
	}{
		"memory": {mem, func(d time.Duration) { clock = clock.Add(d) }},
		"redis":  {NewRedisCounter(rdb), mr.FastForward},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			for want := int64(1); want <= 3; want++ {
				if n, err := tc.c.Incr(ctx, "k", time.Minute); err != nil || n != want {
					t.Fatalf("Incr = %d %v, want %d", n, err, want)
				}
			}
			if n, _ := tc.c.Count(ctx, "k"); n != 3 {
				t.Fatalf("Count = %d, want 3", n)
			}
			tc.advance(time.Minute + time.Second)
			if n, _ := tc.c.Count(ctx, "k"); n != 0 {
				t.Fatalf("Count after window = %d, want 0", n)
			}
			_, _ = tc.c.Incr(ctx, "k", time.Minute)
			if err := tc.c.Reset(ctx, "k"); err != nil {
				t.Fatal(err)
			}
			if n, _ := tc.c.Count(ctx, "k"); n != 0 {
				t.Fatalf("Count after Reset = %d, want 0", n)
			}
		})
	}
}
