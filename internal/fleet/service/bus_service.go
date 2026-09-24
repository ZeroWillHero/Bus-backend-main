// Package service implements the Fleet & Scheduling context's use cases
// (ARCHITECTURE.md §3.2).
package service

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/fleet/domain"
	fleetrepo "github.com/sl-bus/backend/internal/fleet/repository"
	"github.com/sl-bus/backend/internal/platform/auth"
	platformdb "github.com/sl-bus/backend/internal/platform/db"
)

// BusService backs FR-FLT-1. Every method runs under the caller's own
// (real, JWT-derived) tenant context via platform/db.WithTenantContext —
// unlike ops's admin-only services, these act on behalf of the
// authenticated operator_admin's own operator, not a synthetic
// cross-tenant context.
type BusService struct {
	pool     *pgxpool.Pool
	approval domain.OperatorApprovalChecker
}

func NewBusService(pool *pgxpool.Pool, approval domain.OperatorApprovalChecker) *BusService {
	return &BusService{pool: pool, approval: approval}
}

type CreateBusInput struct {
	RegistrationNo string
	BusClass       string
	Amenities      map[string]any
}

// Create checks the operator is approved first (AUTHENTICATION_DESIGN.md
// §3.1 — login works while pending, but capabilities like adding fleet do
// not) before writing anything.
func (s *BusService) Create(ctx context.Context, claims *auth.Claims, in CreateBusInput) (*domain.Bus, error) {
	approved, err := s.approval.IsOperatorApproved(ctx, claims.OperatorID)
	if err != nil {
		return nil, err
	}
	if !approved {
		return nil, domain.ErrOperatorNotApproved
	}

	var bus *domain.Bus
	err = platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		repo := fleetrepo.NewBusRepository(tx)
		bus, err = repo.Create(ctx, &domain.Bus{
			OperatorID:     claims.OperatorID,
			RegistrationNo: in.RegistrationNo,
			BusClass:       in.BusClass,
			Amenities:      in.Amenities,
			Active:         true,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return bus, nil
}

func (s *BusService) Get(ctx context.Context, claims *auth.Claims, id string) (*domain.Bus, error) {
	var bus *domain.Bus
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		repo := fleetrepo.NewBusRepository(tx)
		var err error
		bus, err = repo.FindByID(ctx, id)
		return err
	})
	return bus, err
}

func (s *BusService) List(ctx context.Context, claims *auth.Claims, cursor string, limit int) ([]*domain.Bus, string, error) {
	var buses []*domain.Bus
	var nextCursor string
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		repo := fleetrepo.NewBusRepository(tx)
		var err error
		buses, nextCursor, err = repo.ListByOperator(ctx, claims.OperatorID, cursor, limit)
		return err
	})
	return buses, nextCursor, err
}

type UpdateBusInput struct {
	RegistrationNo string
	BusClass       string
	Amenities      map[string]any
	Active         bool
}

func (s *BusService) Update(ctx context.Context, claims *auth.Claims, id string, in UpdateBusInput) (*domain.Bus, error) {
	var bus *domain.Bus
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		repo := fleetrepo.NewBusRepository(tx)
		var err error
		bus, err = repo.Update(ctx, &domain.Bus{
			ID:             id,
			RegistrationNo: in.RegistrationNo,
			BusClass:       in.BusClass,
			Amenities:      in.Amenities,
			Active:         in.Active,
		})
		return err
	})
	return bus, err
}

const DefaultListLimit = 20
