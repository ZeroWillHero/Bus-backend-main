// Package db additions for the tenant-isolation backstop described in
// DATABASE_DESIGN.md §5 and AUTHENTICATION_DESIGN.md §4.2: every
// tenant-scoped query runs inside a transaction that has first set the
// Postgres session variables the RLS policies check.
package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/platform/auth"
)

// zeroUUID is used for app.current_operator_id when the authenticated
// principal has no operator context (passenger, platform-level roles) —
// it can never match a real operator row, so it's a safe "no tenant" value.
const zeroUUID = "00000000-0000-0000-0000-000000000000"

// knownRoles guards against interpolating anything but one of the fixed
// role strings into SET LOCAL, which pgx cannot parameterize.
var knownRoles = map[auth.Role]struct{}{
	auth.RolePassenger:     {},
	auth.RoleOperatorStaff: {},
	auth.RoleOperatorAdmin: {},
	auth.RoleFinanceAdmin:  {},
	auth.RoleSupport:       {},
	auth.RolePlatformAdmin: {},
}

// WithTenantContext runs fn inside a transaction that has set
// app.current_operator_id and app.current_role from claims, so any
// RLS-protected table fn queries is scoped exactly like the request's
// authenticated principal. claims always come from a verified JWT
// (never raw user input), which is what makes it safe to interpolate them
// directly — SET LOCAL doesn't support bind parameters.
func WithTenantContext(ctx context.Context, pool *pgxpool.Pool, claims *auth.Claims, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if _, ok := knownRoles[claims.Role]; !ok {
		return fmt.Errorf("db: unknown role %q", claims.Role)
	}
	operatorID := claims.OperatorID
	if operatorID == "" {
		operatorID = zeroUUID
	}
	if _, err := uuid.Parse(operatorID); err != nil {
		return fmt.Errorf("db: invalid operator_id claim: %w", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The GUC names are double-quoted because "current_role" is itself a
	// reserved SQL keyword (a niladic function in the grammar) — unquoted,
	// `SET LOCAL app.current_role = ...` fails to parse even though the
	// GUC is really just a custom, dot-namespaced setting.
	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL "app.current_operator_id" = '%s'`, operatorID)); err != nil {
		return fmt.Errorf("db: set current_operator_id: %w", err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL "app.current_role" = '%s'`, claims.Role)); err != nil {
		return fmt.Errorf("db: set current_role: %w", err)
	}

	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit tx: %w", err)
	}
	return nil
}
