// Command worker runs the SL-Bus background processes — seat-hold cleanup,
// the outbox relay, reconciliation, and payout batches — as a separate
// deployable process from the API, sharing all domain code
// (ARCHITECTURE.md §6.3).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	bookingrepo "github.com/sl-bus/backend/internal/booking/repository"
	fleetrepo "github.com/sl-bus/backend/internal/fleet/repository"
	"github.com/sl-bus/backend/internal/platform/db"
)

func main() {
	_ = godotenv.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Port 5433 matches docker-compose.yml's host mapping for local dev
	// (5432 is often already taken by another project on a dev machine).
	dsn := getenv("DATABASE_URL", "postgres://slbus:slbus@localhost:5433/slbus?sslmode=disable")
	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Wire repositories for Phase 3 background tasks
	seatInventoryRepo := fleetrepo.NewSeatInventoryRepository(pool, nil) // Redis not needed for cleanup
	outboxRepo := bookingrepo.NewOutboxRepository(pool)

	// Phase 3: seat-hold cleanup ticker (MITIGATION_PLAN.md §1.1)
	// Idempotent blanket sweep every 20s, safe to call from multiple replicas
	cleanupTicker := time.NewTicker(20 * time.Second)
	defer cleanupTicker.Stop()

	// Phase 3: outbox relay ticker
	// Poll outbox_events table and relay events to Phase 4/5 handlers (console stub for now)
	relayTicker := time.NewTicker(5 * time.Second)
	defer relayTicker.Stop()

	logger.Info("worker started (Phase 3: seat-hold cleanup + outbox relay)")

	for {
		select {
		case <-ctx.Done():
			logger.Info("worker shutting down")
			return

		case <-cleanupTicker.C:
			releasedCount, err := seatInventoryRepo.ReleaseExpiredHolds(ctx)
			if err != nil {
				logger.Error("seat-hold cleanup failed", "error", err)
			} else if releasedCount > 0 {
				logger.Info("seat-hold cleanup swept", "released_count", releasedCount)
			}

		case <-relayTicker.C:
			// Poll outbox for unprocessed events and relay to Phase 4/5 handlers
			// For now, this is a console stub (Phase 4/5 will integrate real handlers)
			events, err := outboxRepo.PollUnprocessed(ctx, 10)
			if err != nil {
				logger.Error("outbox poll failed", "error", err)
				continue
			}

			for _, event := range events {
				// Log event for Phase 4/5 to pick up
				// In real deployment: this would push to a message queue or call handlers
				logger.InfoContext(ctx, "outbox event relayed",
					"event_type", event["event_type"],
					"payload", event["payload"],
				)

				// Mark as processed
				eventID := event["id"].(string)
				if err := outboxRepo.MarkProcessed(ctx, eventID); err != nil {
					logger.Error("failed to mark event processed", "event_id", eventID, "error", err)
				}
			}
		}
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
