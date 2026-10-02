package service

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/ops/domain"
	opsrepo "github.com/sl-bus/backend/internal/ops/repository"
	"github.com/sl-bus/backend/internal/platform/auth"
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

// GetOwn returns the operator the caller belongs to (GET /v1/operators/me).
// Unlike ListByStatus it runs under the caller's own claims, so RLS keeps
// them to their own tenant; callers with no operator_id (e.g. platform
// admins) get ErrOperatorNotFound.
func (s *ListingService) GetOwn(ctx context.Context, claims *auth.Claims) (*domain.Operator, error) {
	if claims == nil || claims.OperatorID == "" {
		return nil, domain.ErrOperatorNotFound
	}
	var operator *domain.Operator
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		operator, err = opsrepo.NewOperatorRepository(tx).FindByID(ctx, claims.OperatorID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return operator, nil
}
