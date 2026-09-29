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
			// TODO: wire fleet.SeatInventoryRepository and call ReleaseExpiredHolds()
			logger.Debug("seat-hold cleanup tick")

		case <-relayTicker.C:
			// TODO: wire booking.OutboxRepository and poll outbox events
			logger.Debug("outbox relay tick")
		}
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
