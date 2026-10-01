package repository_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/booking/repository"
	"github.com/sl-bus/backend/internal/platform/testutil"
)

func outboxEnv(t *testing.T) (context.Context, *pgxpool.Pool, *repository.OutboxRepository) {
	t.Helper()
	pool := testutil.NewTestPool(t)
	ctx := context.Background()
	// Throwaway test database only (see testutil.NewTestPool): start every test with an empty outbox.
	if _, err := pool.Exec(ctx, `DELETE FROM platform.outbox_events`); err != nil {
		t.Fatalf("clean outbox: %v", err)
	}
	return ctx, pool, repository.NewOutboxRepository(pool)
}

func writeEvents(t *testing.T, ctx context.Context, repo *repository.OutboxRepository, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := repo.Write(ctx, nil, "TestEvent", map[string]interface{}{"n": i}); err != nil {
			t.Fatalf("write event %d: %v", i, err)
		}
	}
}

// TestOutboxConcurrentPollNeverDuplicates is the multi-worker guarantee: with
// several workers polling at once, every event is handed to exactly one of them.
func TestOutboxConcurrentPollNeverDuplicates(t *testing.T) {
	ctx, _, repo := outboxEnv(t)
	const total = 200
	writeEvents(t, ctx, repo, total)

	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				events, err := repo.PollUnprocessed(ctx, 7)
				if err != nil {
					t.Errorf("poll: %v", err)
					return
				}
				if len(events) == 0 {
					return
				}
				mu.Lock()
				for _, e := range events {
					seen[e["id"].(string)]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(seen) != total {
		t.Errorf("claimed %d distinct events, want %d (events lost)", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("event %s handed out %d times (double processing)", id, n)
		}
	}
}

func TestOutboxClaimedEventsAreInvisibleUntilLeaseExpires(t *testing.T) {
	ctx, pool, repo := outboxEnv(t)
	writeEvents(t, ctx, repo, 1)

	first, err := repo.PollUnprocessed(ctx, 10)
	if err != nil || len(first) != 1 {
		t.Fatalf("first poll: %v, events=%d", err, len(first))
	}
	again, _ := repo.PollUnprocessed(ctx, 10)
	if len(again) != 0 {
		t.Fatalf("a claimed event was handed out again before its lease expired")
	}

	// Simulate a crashed worker: its lease lapses and the event is redelivered.
	if _, err := pool.Exec(ctx, `UPDATE platform.outbox_events SET claimed_until = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	redelivered, err := repo.PollUnprocessed(ctx, 10)
	if err != nil || len(redelivered) != 1 {
		t.Fatalf("event must be redelivered after lease expiry: %v, events=%d", err, len(redelivered))
	}
	if got := redelivered[0]["attempts"].(int); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
	if redelivered[0]["id"] != first[0]["id"] {
		t.Errorf("redelivered a different event")
	}
}

func TestOutboxProcessedEventIsNeverRedelivered(t *testing.T) {
	ctx, pool, repo := outboxEnv(t)
	writeEvents(t, ctx, repo, 1)
	events, _ := repo.PollUnprocessed(ctx, 10)
	if err := repo.MarkProcessed(ctx, events[0]["id"].(string)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE platform.outbox_events SET claimed_until = now() - interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	if again, _ := repo.PollUnprocessed(ctx, 10); len(again) != 0 {
		t.Errorf("processed event was redelivered")
	}
}

func TestOutboxPollReturnsOldestFirst(t *testing.T) {
	ctx, _, repo := outboxEnv(t)
	writeEvents(t, ctx, repo, 5)
	events, _ := repo.PollUnprocessed(ctx, 3)
	if len(events) != 3 {
		t.Fatalf("want 3 events, got %d", len(events))
	}
	for i, e := range events {
		if n := e["payload"].(map[string]interface{})["n"].(float64); int(n) != i {
			t.Errorf("event %d has n=%v: batch is not oldest-first", i, n)
		}
	}
}

// TestOutboxWriteIsAtomicWithBusinessTx: the event exists iff the business
// transaction commits (the point of the outbox pattern).
func TestOutboxWriteIsAtomicWithBusinessTx(t *testing.T) {
	ctx, pool, repo := outboxEnv(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Write(ctx, tx, "Rolled", map[string]interface{}{"x": 1}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM platform.outbox_events WHERE event_type='Rolled'`).Scan(&n)
	if n != 0 {
		t.Errorf("rolled-back transaction leaked %d outbox events", n)
	}

	tx, _ = pool.Begin(ctx)
	if err := repo.Write(ctx, tx, "Committed", map[string]interface{}{"x": 1}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM platform.outbox_events WHERE event_type='Committed'`).Scan(&n)
	if n != 1 {
		t.Errorf("committed event count = %d, want 1", n)
	}
	_ = fmt.Sprint
	_ = time.Now
}
