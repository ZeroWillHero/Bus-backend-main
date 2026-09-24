package service

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/ops/domain"
	opsrepo "github.com/sl-bus/backend/internal/ops/repository"
	platformdb "github.com/sl-bus/backend/internal/platform/db"
)

// ListingService backs the admin approval-review screen (GET
// /v1/operators) — platform_admin only, already enforced by
// auth.RequireRole at the HTTP layer. It runs under the same synthetic
// platform_admin tenant context as ApprovalService, for the same reason:
// the RLS policy on ops.operators only grants cross-tenant visibility
// when app.current_role is actually set.
type ListingService struct {
	pool *pgxpool.Pool
}

func NewListingService(pool *pgxpool.Pool) *ListingService {
	return &ListingService{pool: pool}
}

const DefaultListLimit = 20

func (s *ListingService) ListByStatus(ctx context.Context, status domain.OperatorStatus, cursor string, limit int) ([]*domain.Operator, string, error) {
	if limit <= 0 {
		limit = DefaultListLimit
	}
	var operators []*domain.Operator
	var nextCursor string
	err := platformdb.WithTenantContext(ctx, s.pool, platformAdminClaims, func(ctx context.Context, tx pgx.Tx) error {
		repo := opsrepo.NewOperatorRepository(tx)
		var err error
		operators, nextCursor, err = repo.ListByStatus(ctx, status, cursor, limit)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	return operators, nextCursor, nil
}
