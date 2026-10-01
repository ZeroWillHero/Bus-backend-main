package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/mail"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	opsservice "github.com/sl-bus/backend/internal/ops/service"
	"github.com/sl-bus/backend/internal/platform/auth"
)

// bootstrapPlatformAdmin creates the first platform_admin on first run, so
// a fresh deployment doesn't need hand-written SQL. It is a no-op unless
// BOOTSTRAP_ADMIN_EMAIL and BOOTSTRAP_ADMIN_PASSWORD are both set, and it
// never creates a second admin: once any platform_admin exists it does
// nothing, so leaving the variables set across restarts is harmless (and
// changing them later will not add or alter an admin). MFA enrollment
// happens on the admin's first login, as for any platform_admin.
func bootstrapPlatformAdmin(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	email := strings.TrimSpace(os.Getenv("BOOTSTRAP_ADMIN_EMAIL"))
	password := os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")
	if email == "" || password == "" {
		return nil
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return fmt.Errorf("BOOTSTRAP_ADMIN_EMAIL is not a valid email: %w", err)
	}
	if len(password) < opsservice.MinPasswordLength {
		return fmt.Errorf("BOOTSTRAP_ADMIN_PASSWORD must be at least %d characters", opsservice.MinPasswordLength)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash bootstrap admin password: %w", err)
	}

	// Single statement so the existence check and insert are atomic; the
	// UNIQUE(email) constraint covers a concurrent second instance racing
	// with a different admin row.
	tag, err := pool.Exec(ctx, `
		INSERT INTO identity.users (email, password_hash, role)
		SELECT $1, $2, 'platform_admin'
		WHERE NOT EXISTS (SELECT 1 FROM identity.users WHERE role = 'platform_admin')
		ON CONFLICT DO NOTHING`, email, hash)
	if err != nil {
		return fmt.Errorf("insert bootstrap admin: %w", err)
	}
	if tag.RowsAffected() == 1 {
		logger.Info("bootstrap platform admin created", "email", email)
	}
	return nil
}
