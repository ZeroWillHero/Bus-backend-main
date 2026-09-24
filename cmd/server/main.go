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

	fleethttp "github.com/sl-bus/backend/internal/fleet/http"
	fleetservice "github.com/sl-bus/backend/internal/fleet/service"
	identityhttp "github.com/sl-bus/backend/internal/identity/http"
	identityrepo "github.com/sl-bus/backend/internal/identity/repository"
	identityservice "github.com/sl-bus/backend/internal/identity/service"
	opshttp "github.com/sl-bus/backend/internal/ops/http"
	opsservice "github.com/sl-bus/backend/internal/ops/service"
	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/db"
	"github.com/sl-bus/backend/internal/platform/httpx"
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

	// Port 5433 matches docker-compose.yml's host mapping for local dev
	// (5432 is often already taken by another project on a dev machine).
	//
	// Connects as slbus_app, NOT the slbus superuser migrations run as
	// (db/migrations/000005_app_role.up.sql) — Postgres superusers bypass
	// Row-Level Security unconditionally, so the API must run as a
	// non-superuser role for the tenant-isolation policies
	// (DATABASE_DESIGN.md §5) to have any effect at all.
	dsn := getenv("DATABASE_URL", "postgres://slbus_app:slbus_app@localhost:5433/slbus?sslmode=disable")
	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		// The API cannot serve any bounded context without the database;
		// fail fast rather than starting in a half-working state.
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(httpx.RequestID)
	r.Use(middleware.Logger)

	r.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
		if err := pool.Ping(req.Context()); err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.APIError{
				Code:    "PLATFORM_DB_UNAVAILABLE",
				Message: "Database is currently unreachable.",
			}, nil)
			return
		}
		httpx.WriteSuccess(w, http.StatusOK, map[string]string{"status": "ok"}, nil)
	})

	// Phase 1 — Identity & Operator Onboarding (IMPLEMENTATION_ROADMAP.md).
	// Repos are constructed against the shared pool; cross-context/RLS
	// call sites (ops registration, WithTenantContext) bind fresh
	// instances to a specific pgx.Tx instead, per platform/db/txscope.go.
	jwtIssuer := auth.NewJWTIssuer(getenv("JWT_SIGNING_KEY", "dev-only-insecure-signing-key-change-me"))
	breachChecker := auth.NoopBreachChecker{}

	userRepo := identityrepo.NewUserRepository(pool)
	otpRepo := identityrepo.NewOTPRepository(pool)
	refreshRepo := identityrepo.NewRefreshTokenRepository(pool)
	trustedDeviceRepo := identityrepo.NewTrustedDeviceRepository(pool)

	tokenIssuer := identityservice.NewTokenIssuer(jwtIssuer, refreshRepo)
	preAuthStore := identityservice.NewPreAuthTokenStore()
	approvalSvc := opsservice.NewApprovalService(pool)

	otpSvc := identityservice.NewOTPService(otpRepo, userRepo, identityservice.ConsoleNotifier{}, tokenIssuer)
	loginSvc := identityservice.NewLoginService(userRepo, trustedDeviceRepo, breachChecker, approvalSvc, preAuthStore, tokenIssuer)
	mfaSvc := identityservice.NewMFAService(userRepo, trustedDeviceRepo, preAuthStore, tokenIssuer)
	tokenSvc := identityservice.NewTokenService(refreshRepo, userRepo, jwtIssuer, tokenIssuer)

	registrationSvc := opsservice.NewRegistrationService(pool, breachChecker)
	listingSvc := opsservice.NewListingService(pool)

	r.Route("/v1/auth", func(r chi.Router) {
		r.Post("/register", opshttp.NewRegisterHandler(registrationSvc))
		r.Mount("/", identityhttp.NewRouter(identityhttp.Deps{
			OTP:      otpSvc,
			Login:    loginSvc,
			MFA:      mfaSvc,
			Tokens:   tokenSvc,
			Verifier: jwtIssuer,
		}))
	})

	r.Mount("/v1/operators", opshttp.NewRouter(opshttp.Deps{
		Registration: registrationSvc,
		Approval:     approvalSvc,
		Listing:      listingSvc,
		Verifier:     jwtIssuer,
	}))

	// Phase 2 — Fleet & Search (IMPLEMENTATION_ROADMAP.md). approvalSvc
	// (constructed above for Phase 1) doubles as fleet's
	// OperatorApprovalChecker port — same instance, no new wiring needed.
	busSvc := fleetservice.NewBusService(pool, approvalSvc)
	seatMapSvc := fleetservice.NewSeatMapService(pool)
	routeSvc := fleetservice.NewRouteService(pool, approvalSvc)
	tripSvc := fleetservice.NewTripService(pool, approvalSvc)
	searchSvc := fleetservice.NewSearchService(pool)

	r.Mount("/v1/fleet", fleethttp.NewRouter(fleethttp.Deps{
		Bus:      busSvc,
		SeatMap:  seatMapSvc,
		Route:    routeSvc,
		Trip:     tripSvc,
		Search:   searchSvc,
		Verifier: jwtIssuer,
	}))
	r.Get("/v1/trips/search", fleethttp.NewSearchHandler(searchSvc))

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
