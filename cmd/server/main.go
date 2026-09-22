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

	"github.com/sl-bus/backend/internal/platform/db"
	"github.com/sl-bus/backend/internal/platform/httpx"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dsn := getenv("DATABASE_URL", "postgres://slbus:slbus@localhost:5432/slbus?sslmode=disable")
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

	// TODO(phase-1): mount bounded-context routers here, e.g.
	//   r.Mount("/v1/auth", identityhttp.NewRouter(...))
	//   r.Mount("/v1/operators", opshttp.NewRouter(...))
	// per ARCHITECTURE.md §3.1 and ENGINEERING_STANDARDS.md §6 (URL versioning).

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
