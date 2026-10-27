// Command worker runs the SL-Bus background processes — seat-hold cleanup,
// the outbox relay, reconciliation, and payout batches — as a separate
// deployable process from the API, sharing all domain code
// (ARCHITECTURE.md §6.3).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"

	bookingrepo "github.com/sl-bus/backend/internal/booking/repository"
	financerepository "github.com/sl-bus/backend/internal/finance/repository"
	financeservice "github.com/sl-bus/backend/internal/finance/service"
	fleetrepo "github.com/sl-bus/backend/internal/fleet/repository"
	"github.com/sl-bus/backend/internal/platform/db"
	"github.com/sl-bus/backend/internal/platform/outbox"
	"github.com/sl-bus/backend/internal/platform/sms"
)

func main() {
	_ = godotenv.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The outbox is write-only for the API's slbus_app role (RLS denies SELECT and
	// UPDATE), so the relay needs a privileged connection: WORKER_DATABASE_URL,
	// falling back to DATABASE_URL for local setups that use one superuser.
	dsn := os.Getenv("WORKER_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		logger.Error("WORKER_DATABASE_URL or DATABASE_URL is required (see .env.example)")
		os.Exit(1)
	}
	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Redis carries live seat updates to WS clients (expired-hold sweeps
	// publish 'available') and the SMS balance cache.
	redisClient := redis.NewClient(&redis.Options{Addr: getenv("REDIS_ADDR", "localhost:6379")})
	defer func() { _ = redisClient.Close() }()

	// Wire repositories for Phase 3/4 background tasks
	seatInventoryRepo := fleetrepo.NewSeatInventoryRepository(pool, redisClient)
	tripLifecycleRepo := fleetrepo.NewTripLifecycleRepository(pool)
	outboxRepo := bookingrepo.NewOutboxRepository(pool)

	// Phase 4: Wire finance repositories and journal service
	journalRepo := financerepository.NewJournalRepository(pool)
	accountRepo := financerepository.NewAccountRepository(pool)
	operatorLedgerRepo := financerepository.NewOperatorLedgerRepository(pool)
	periodRepo := financerepository.NewPeriodRepository(pool)
	outboxAdapter := financerepository.NewOutboxAdapter(outboxRepo)

	journalSvc := financeservice.NewJournalService(
		journalRepo, accountRepo, operatorLedgerRepo, periodRepo, outboxAdapter, logger,
	)

	// Phase 3: seat-hold cleanup ticker (MITIGATION_PLAN.md §1.1)
	// Idempotent blanket sweep every 20s, safe to call from multiple replicas
	cleanupTicker := time.NewTicker(20 * time.Second)
	defer cleanupTicker.Stop()

	// Trip completion: scheduled trips whose arrival has passed become
	// 'completed'. Idempotent set-based UPDATE, safe across replicas.
	tripCompletionTicker := time.NewTicker(5 * time.Minute)
	defer tripCompletionTicker.Stop()

	// Phase 3: outbox relay ticker
	// Poll outbox_events table and relay events to Phase 4/5 handlers (console stub for now)
	relayTicker := time.NewTicker(5 * time.Second)
	defer relayTicker.Stop()

	relay := &outbox.Relay{
		Source:    outboxRepo,
		BatchSize: 10,
		Log:       logger,
		Handlers: map[string]outbox.Handler{
			"BookingConfirmed": bookingHandler(journalSvc.ProcessBookingConfirmed),
			"BookingCancelled": bookingHandler(journalSvc.ProcessBookingCancelled),
			"PaidButExpired":   bookingHandler(journalSvc.ProcessPaidButExpired),
		},
	}

	// SMS credit monitor: an exhausted SMSlenz balance silently breaks SMS
	// sign-in codes, so warn well before it runs out.
	// Each check also refreshes the Redis cache the admin dashboard reads.
	_, smsClient, smsErr := sms.FromEnv()
	if smsErr != nil {
		logger.Warn("sms balance monitor disabled", "error", smsErr)
	}
	smsBalance := sms.NewBalanceService(smsClient, redisClient)
	smsPolicy := sms.BalancePolicyFromEnv()
	smsBalanceTicker := time.NewTicker(30 * time.Minute)
	defer smsBalanceTicker.Stop()
	checkSMSBalance := func() {
		if smsClient == nil {
			return
		}
		checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		snap, stale, err := smsBalance.Get(checkCtx, true)
		if err != nil || stale {
			logger.Error("sms balance check failed", "error", err, "stale", stale)
			return
		}
		if smsPolicy.IsLow(snap) {
			logger.Error("ALERT: sms credit low or account inactive", "status", snap.Status, "balance_lkr", snap.MainBalanceLKR, "threshold_lkr", smsPolicy.LowThresholdLKR)
		}
	}
	checkSMSBalance()

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

		case <-tripCompletionTicker.C:
			n, err := tripLifecycleRepo.CompleteArrivedTrips(ctx)
			if err != nil {
				logger.Error("trip completion failed", "error", err)
			} else if n > 0 {
				logger.Info("trips completed", "count", n)
			}

		case <-smsBalanceTicker.C:
			checkSMSBalance()

		case <-relayTicker.C:
			stats, err := relay.RunOnce(ctx)
			if err != nil {
				logger.Error("outbox relay failed", "error", err)
			} else if stats.Processed+stats.Failed+stats.Skipped > 0 {
				logger.Info("outbox relay batch", "processed", stats.Processed, "failed", stats.Failed, "skipped", stats.Skipped)
			}
		}
	}
}

// bookingHandler adapts a ledger method to an outbox handler, extracting the
// booking id with a checked assertion (a bad payload is an error, not a panic).
func bookingHandler(fn func(ctx context.Context, bookingID string, payload map[string]interface{}) error) outbox.Handler {
	return func(ctx context.Context, payload map[string]interface{}) error {
		bookingID, ok := payload["booking_id"].(string)
		if !ok || bookingID == "" {
			return fmt.Errorf("outbox payload has no booking_id")
		}
		return fn(ctx, bookingID, payload)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
