// Package db wires the shared Postgres connection pool used by every
// bounded-context repository adapter.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool opens a pgx connection pool against the given DSN.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse dsn: %w", err)
	}
	cfg.AfterConnect = setRLSDefaults

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}

// setRLSDefaults gives each new connection session-level values for the
// app.current_* settings the RLS policies read. After a transaction's
// SET LOCAL ends, Postgres reverts a custom setting to its session value;
// with no session value that is the empty string, and the policies' ::uuid casts then fail.
// Migration 000010 sets these as role defaults where it has the privilege;
// this covers hosted Postgres, where it doesn't.
func setRLSDefaults(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx,
		`SELECT set_config('app.current_operator_id', $1, false),
		        set_config('app.current_user_id', $1, false),
		        set_config('app.current_role', '', false)`, zeroUUID)
	if err != nil {
		return fmt.Errorf("db: set rls defaults: %w", err)
	}
	return nil
}
