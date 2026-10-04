// Command server runs the SL-Bus API — the request-serving half of the Go
// modular monolith described in ARCHITECTURE.md §3. The background worker
// (seat-hold cleanup, reconciliation, payouts) is a separate binary,
// cmd/worker, per ARCHITECTURE.md §6.3.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"

	bookinghttp "github.com/sl-bus/backend/internal/booking/http"
	bookingrepo "github.com/sl-bus/backend/internal/booking/repository"
	bookingservice "github.com/sl-bus/backend/internal/booking/service"
	fleethttp "github.com/sl-bus/backend/internal/fleet/http"
	fleetrepo "github.com/sl-bus/backend/internal/fleet/repository"
	fleetservice "github.com/sl-bus/backend/internal/fleet/service"
	identityhttp "github.com/sl-bus/backend/internal/identity/http"
	identityrepo "github.com/sl-bus/backend/internal/identity/repository"
	identityservice "github.com/sl-bus/backend/internal/identity/service"
	insights "github.com/sl-bus/backend/internal/insights"
	insightshttp "github.com/sl-bus/backend/internal/insights/http"
	opshttp "github.com/sl-bus/backend/internal/ops/http"
	opsservice "github.com/sl-bus/backend/internal/ops/service"
	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/db"
	"github.com/sl-bus/backend/internal/platform/httpx"
	"github.com/sl-bus/backend/internal/platform/wsgw"
)

func main() {
	// Loads .env into the process environment if one exists (local dev —
	// see .env.example); silently a no-op otherwise, since prod/CI inject
	// real environment variables directly rather than via a file.
	_ = godotenv.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Connects as slbus_app, NOT the slbus superuser migrations run as
	// (db/migrations/000005_app_role.up.sql) — Postgres superusers bypass
	// Row-Level Security unconditionally, so the API must run as a
	// non-superuser role for the tenant-isolation policies
	// (DATABASE_DESIGN.md §5) to have any effect at all.
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		logger.Error("DATABASE_URL is required (remote Supabase connection string; see .env.example)")
		os.Exit(1)
	}
	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		// The API cannot serve any bounded context without the database;
		// fail fast rather than starting in a half-working state.
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := bootstrapPlatformAdmin(ctx, pool, logger); err != nil {
		logger.Error("bootstrap platform admin failed", "error", err)
		os.Exit(1)
	}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(httpx.RequestID)
	r.Use(middleware.Logger)

	health := func(w http.ResponseWriter, req *http.Request) {
		if err := pool.Ping(req.Context()); err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.APIError{
				Code:    "PLATFORM_DB_UNAVAILABLE",
				Message: "Database is currently unreachable.",
			}, nil)
			return
		}
		httpx.WriteSuccess(w, http.StatusOK, map[string]string{"status": "ok"}, nil)
	}

	// Phase 1 — Identity & Operator Onboarding (IMPLEMENTATION_ROADMAP.md).
	// Repos are constructed against the shared pool; cross-context/RLS
	// call sites (ops registration, WithTenantContext) bind fresh
	// instances to a specific pgx.Tx instead, per platform/db/txscope.go.
	jwtIssuer := auth.NewJWTIssuer(requireSecret("JWT_SIGNING_KEY", "dev-only-insecure-signing-key-change-me"))
	breachChecker := auth.NoopBreachChecker{}

	userRepo := identityrepo.NewUserRepository(pool)
	otpRepo := identityrepo.NewOTPRepository(pool)
	refreshRepo := identityrepo.NewRefreshTokenRepository(pool)
	trustedDeviceRepo := identityrepo.NewTrustedDeviceRepository(pool)

	tokenIssuer := identityservice.NewTokenIssuer(jwtIssuer, refreshRepo)
	preAuthStore := identityservice.NewPreAuthTokenStore()
	approvalSvc := opsservice.NewApprovalService(pool)

	otpSvc := identityservice.NewOTPService(otpRepo, userRepo, identityservice.ConsoleNotifier{}, tokenIssuer)
	passengerRegistrationSvc := identityservice.NewPassengerRegistrationService(userRepo, breachChecker)
	loginSvc := identityservice.NewLoginService(userRepo, trustedDeviceRepo, breachChecker, approvalSvc, preAuthStore, tokenIssuer)
	mfaSvc := identityservice.NewMFAService(userRepo, trustedDeviceRepo, preAuthStore, tokenIssuer)
	tokenSvc := identityservice.NewTokenService(refreshRepo, userRepo, jwtIssuer, tokenIssuer)

	registrationSvc := opsservice.NewRegistrationService(pool, breachChecker)
	listingSvc := opsservice.NewListingService(pool)
	insightsSvc := insights.NewService(pool)

	// Phase 2 — Fleet & Search (IMPLEMENTATION_ROADMAP.md). approvalSvc
	// (constructed above for Phase 1) doubles as fleet's
	// OperatorApprovalChecker port — same instance, no new wiring needed.
	busSvc := fleetservice.NewBusService(pool, approvalSvc)
	seatMapSvc := fleetservice.NewSeatMapService(pool)
	routeSvc := fleetservice.NewRouteService(pool, approvalSvc)
	tripSvc := fleetservice.NewTripService(pool, approvalSvc)
	searchSvc := fleetservice.NewSearchService(pool)
	fleetReadModel := fleetservice.NewReadModelService(pool)

	// Phase 3 — Booking Core (IMPLEMENTATION_ROADMAP.md).
	// Constructs the seat-hold repository, checkout service, and booking HTTP handlers.
	// Also wires the Redis client for live seat-update pub/sub and WebSocket gateway.
	redisAddr := getenv("REDIS_ADDR", "localhost:6379")
	redisClient := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	defer func() { _ = redisClient.Close() }()

	seatInventoryRepo := fleetrepo.NewSeatInventoryRepository(pool, redisClient)
	bookingRepo := bookingrepo.NewBookingRepository(pool)
	bookingSeatRepo := bookingrepo.NewBookingSeatRepository(pool)
	ticketRepo := bookingrepo.NewTicketRepository(pool)
	outboxRepo := bookingrepo.NewOutboxRepository(pool)
	paymentGateway := bookingrepo.NewMockPaymentGateway()
	qrTokenSecret := []byte(requireSecret("QR_TOKEN_SECRET", "dev-only-insecure-secret-change-me"))

	checkoutSvc := bookingservice.NewCheckoutService(
		bookingRepo,
		bookingSeatRepo,
		ticketRepo,
		seatInventoryRepo,
		seatInventoryRepo, // also the TripPricingPort: server-side fares
		paymentGateway,
		outboxRepo,
		qrTokenSecret,
	)

	// WebSocket gateway for live seat updates
	wsGateway := wsgw.NewGateway(redisClient, jwtIssuer, logger)

	mountRoutes(r, routeDeps{
		Health:           health,
		RegisterOperator: opshttp.NewRegisterHandler(registrationSvc),
		Identity: identityhttp.Deps{
			OTP:                   otpSvc,
			PassengerRegistration: passengerRegistrationSvc,
			Login:                 loginSvc,
			MFA:                   mfaSvc,
			Tokens:                tokenSvc,
			Verifier:              jwtIssuer,
		},
		Ops: opshttp.Deps{
			Registration: registrationSvc,
			Approval:     approvalSvc,
			Listing:      listingSvc,
			Verifier:     jwtIssuer,
		},
		Insights: insightshttp.Deps{Insights: insightsSvc, Verifier: jwtIssuer},
		Fleet: fleethttp.Deps{
			Bus:       busSvc,
			SeatMap:   seatMapSvc,
			Route:     routeSvc,
			Trip:      tripSvc,
			Search:    searchSvc,
			ReadModel: fleetReadModel,
			Verifier:  jwtIssuer,
		},
		Search:    fleethttp.NewSearchHandler(searchSvc),
		TripSeats: fleethttp.NewTripSeatsHandler(searchSvc),
		Booking: bookinghttp.Deps{
			CheckoutService: checkoutSvc,
			Verifier:        jwtIssuer,
		},
		SeatUpdatesWS: wsGateway.HandleSeatsSubscription,
	})

	addr := getenv("HTTP_ADDR", ":8080")
	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("http server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// requireSecret returns the secret from the environment. The insecure dev
// fallback is only honoured when APP_ENV=development; anywhere else a missing
// secret is fatal so a misconfigured deploy fails closed instead of signing
// tokens with a publicly known key.
func requireSecret(key, devFallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if os.Getenv("APP_ENV") == "development" {
		slog.Warn("using insecure dev default for secret", "key", key)
		return devFallback
	}
	slog.Error("required secret not set (set it, or APP_ENV=development for local dev only)", "key", key)
	os.Exit(1)
	return ""
}
