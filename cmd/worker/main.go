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

	// TODO(phase-3): seat-hold cleanup ticker per MITIGATION_PLAN.md §1.1
	//   (sub-minute interval — a plain ticker, not robfig/cron).
	// TODO(phase-4): robfig/cron/v3 schedule for reconciliation (FR-FIN-11)
	//   and payout batches (FR-FIN-10), per ARCHITECTURE.md §6.3.
	// TODO(phase-3): outbox relay goroutine per ARCHITECTURE.md §3.3.

	logger.Info("worker started (no jobs registered yet — Phase 0 scaffold)")

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("worker shutting down")
			return
		case <-ticker.C:
			logger.Debug("worker heartbeat")
		}
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
