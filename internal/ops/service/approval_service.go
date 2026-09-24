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

// platformAdminClaims is the synthetic tenant context these system-level
// operations run under — every method here is either already gated to
// platform_admin at the HTTP layer (auth.RequireRole), or (IsOperatorSuspended)
// is an internal check that legitimately needs to see any operator
// regardless of the caller's own tenant, mirroring what a real
// platform_admin JWT's claims would carry.
var platformAdminClaims = &auth.Claims{Role: auth.RolePlatformAdmin}

// ApprovalService implements the Super Admin's approve/reject/suspend/
// reactivate actions (FR-OPS-3). Callers (http layer) are responsible for
// checking the actor's role is platform_admin via auth.RequireRole before
// reaching these methods — this service additionally establishes that
// role as the DB session's tenant context (platform/db.WithTenantContext),
// since the RLS policy backing ops.operators only grants cross-tenant
// visibility when app.current_role is actually set to platform_admin,
// not merely when the caller's JWT claims say so.
type ApprovalService struct {
	pool *pgxpool.Pool
}

func NewApprovalService(pool *pgxpool.Pool) *ApprovalService {
	return &ApprovalService{pool: pool}
}

func (s *ApprovalService) transition(ctx context.Context, operatorID string, to domain.OperatorStatus) (*domain.Operator, error) {
	var operator *domain.Operator
	err := platformdb.WithTenantContext(ctx, s.pool, platformAdminClaims, func(ctx context.Context, tx pgx.Tx) error {
		repo := opsrepo.NewOperatorRepository(tx)
		current, err := repo.FindByID(ctx, operatorID)
		if err != nil {
			return err
		}
		if !domain.CanTransition(current.Status, to) {
			return domain.ErrInvalidStatusTransition
		}
		operator, err = repo.UpdateStatus(ctx, operatorID, to)
		return err
	})
	if err != nil {
		return nil, err
	}
	return operator, nil
}

func (s *ApprovalService) Approve(ctx context.Context, operatorID string) (*domain.Operator, error) {
	return s.transition(ctx, operatorID, domain.StatusApproved)
}

func (s *ApprovalService) Reject(ctx context.Context, operatorID string) (*domain.Operator, error) {
	return s.transition(ctx, operatorID, domain.StatusRejected)
}

func (s *ApprovalService) Suspend(ctx context.Context, operatorID string) (*domain.Operator, error) {
	return s.transition(ctx, operatorID, domain.StatusSuspended)
}

// Reactivate returns a suspended operator to approved (FR-OPS-3).
func (s *ApprovalService) Reactivate(ctx context.Context, operatorID string) (*domain.Operator, error) {
	return s.transition(ctx, operatorID, domain.StatusApproved)
}

// IsOperatorApproved implements fleet/domain.OperatorApprovalChecker —
// the Phase 2 gate on fleet-mutating endpoints (AUTHENTICATION_DESIGN.md
// §3.1: a pending operator's account can log in immediately, but
// capabilities like adding fleet are gated on approval; login itself
// never blocked on "pending", only IsOperatorSuspended's "suspended").
func (s *ApprovalService) IsOperatorApproved(ctx context.Context, operatorID string) (bool, error) {
	var approved bool
	err := platformdb.WithTenantContext(ctx, s.pool, platformAdminClaims, func(ctx context.Context, tx pgx.Tx) error {
		repo := opsrepo.NewOperatorRepository(tx)
		operator, err := repo.FindByID(ctx, operatorID)
		if err != nil {
			return err
		}
		approved = operator.Status == domain.StatusApproved
		return nil
	})
	return approved, err
}

// IsOperatorSuspended implements identity/service.OperatorStatusChecker,
// letting LoginService reject a login for a suspended operator's staff
// without identity importing internal/ops directly.
func (s *ApprovalService) IsOperatorSuspended(ctx context.Context, operatorID string) (bool, error) {
	var suspended bool
	err := platformdb.WithTenantContext(ctx, s.pool, platformAdminClaims, func(ctx context.Context, tx pgx.Tx) error {
		repo := opsrepo.NewOperatorRepository(tx)
		operator, err := repo.FindByID(ctx, operatorID)
		if err != nil {
			return err
		}
		suspended = operator.Status == domain.StatusSuspended
		return nil
	})
	return suspended, err
}
