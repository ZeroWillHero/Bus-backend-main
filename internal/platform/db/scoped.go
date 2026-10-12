package db

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/platform/auth"
)

// Querier is the subset of *pgxpool.Pool that repositories use. Scoped
// implements it so every statement runs under the Row-Level Security context
// of the request that issued it.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Scope selects whose identity a Scoped querier presents to RLS.
type Scope int

const (
	// ScopeCaller uses the authenticated claims carried by the context (the
	// passenger, staff member, ...). With no claims in the context (the
	// worker, tests) it falls back to the system identity.
	ScopeCaller Scope = iota
	// ScopeSystem always acts as the platform. It is for work that legitimately
	// crosses tenants, e.g. a passenger holding seats that belong to an
	// operator's trip, or the worker sweeping expired holds.
	ScopeSystem
)

var systemClaims = &auth.Claims{Role: auth.RolePlatformAdmin}

// Scoped runs each statement in its own short transaction that first sets the
// app.current_* settings the RLS policies read (SET LOCAL semantics, so they
// can never leak to the next user of a pooled connection).
type Scoped struct {
	pool  *pgxpool.Pool
	scope Scope
}

var _ Querier = (*Scoped)(nil)

func NewScoped(pool *pgxpool.Pool, scope Scope) *Scoped {
	return &Scoped{pool: pool, scope: scope}
}

func (s *Scoped) claims(ctx context.Context) *auth.Claims {
	if s.scope == ScopeCaller {
		if c, ok := auth.FromContext(ctx); ok {
			return c
		}
	}
	return systemClaims
}

func (s *Scoped) begin(ctx context.Context) (pgx.Tx, error) {
	op, user, role, err := scopeValues(s.claims(ctx))
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: begin tx: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.current_operator_id', $1, true),
		        set_config('app.current_user_id', $2, true),
		        set_config('app.current_role', $3, true)`, op, user, role); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("db: set tenant context: %w", err)
	}
	return tx, nil
}

func (s *Scoped) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		_ = tx.Rollback(ctx)
		return tag, err
	}
	return tag, tx.Commit(ctx)
}

func (s *Scoped) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return &scopedRows{Rows: rows, tx: tx, ctx: ctx}, nil
}

func (s *Scoped) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	rows, err := s.Query(ctx, sql, args...)
	if err != nil {
		return errRow{err}
	}
	return &scopedRow{rows: rows.(*scopedRows)}
}

// scopedRows ends its transaction when the result set is exhausted or closed.
type scopedRows struct {
	pgx.Rows
	tx   pgx.Tx
	ctx  context.Context
	once sync.Once
	err  error
}

func (r *scopedRows) finish() error {
	r.once.Do(func() {
		r.Rows.Close()
		if err := r.Rows.Err(); err != nil {
			_ = r.tx.Rollback(r.ctx)
			return
		}
		r.err = r.tx.Commit(r.ctx)
	})
	return r.err
}

func (r *scopedRows) Next() bool {
	if r.Rows.Next() {
		return true
	}
	_ = r.finish()
	return false
}

func (r *scopedRows) Close() { _ = r.finish() }

func (r *scopedRows) Err() error {
	if err := r.Rows.Err(); err != nil {
		return err
	}
	return r.err
}

type scopedRow struct{ rows *scopedRows }

func (r *scopedRow) Scan(dest ...any) error {
	if !r.rows.Next() {
		if err := r.rows.Err(); err != nil {
			return err
		}
		return pgx.ErrNoRows
	}
	err := r.rows.Rows.Scan(dest...)
	if cerr := r.rows.finish(); err == nil {
		err = cerr
	}
	return err
}

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

// scopeValues turns claims into the three RLS settings. Identities that are
// absent or malformed become the zero uuid, which matches no real row.
func scopeValues(c *auth.Claims) (operatorID, userID, role string, err error) {
	if _, ok := knownRoles[c.Role]; !ok {
		return "", "", "", fmt.Errorf("db: unknown role %q", c.Role)
	}
	operatorID, userID = zeroUUID, zeroUUID
	if c.OperatorID != "" {
		if _, perr := uuid.Parse(c.OperatorID); perr != nil {
			return "", "", "", fmt.Errorf("db: invalid operator_id claim: %w", perr)
		}
		operatorID = c.OperatorID
	}
	if _, perr := uuid.Parse(c.Subject); perr == nil {
		userID = c.Subject
	}
	return operatorID, userID, string(c.Role), nil
}
