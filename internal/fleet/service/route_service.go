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

// RouteService backs FR-FLT-3.
type RouteService struct {
	pool     *pgxpool.Pool
	approval domain.OperatorApprovalChecker
}

func NewRouteService(pool *pgxpool.Pool, approval domain.OperatorApprovalChecker) *RouteService {
	return &RouteService{pool: pool, approval: approval}
}

type CreateRouteInput struct {
	Origin      string
	Destination string
	DistanceKm  *float64
	Stops       []domain.RouteStop
}

func (s *RouteService) Create(ctx context.Context, claims *auth.Claims, in CreateRouteInput) (*domain.Route, error) {
	approved, err := s.approval.IsOperatorApproved(ctx, claims.OperatorID)
	if err != nil {
		return nil, err
	}
	if !approved {
		return nil, domain.ErrOperatorNotApproved
	}

	var route *domain.Route
	err = platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		repo := fleetrepo.NewRouteRepository(tx)
		route, err = repo.Create(ctx, &domain.Route{
			OperatorID:  claims.OperatorID,
			Origin:      in.Origin,
			Destination: in.Destination,
			DistanceKm:  in.DistanceKm,
		})
		if err != nil {
			return err
		}
		if len(in.Stops) > 0 {
			if err := repo.ReplaceStops(ctx, route.ID, in.Stops); err != nil {
				return err
			}
			route.Stops = in.Stops
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return route, nil
}

func (s *RouteService) Get(ctx context.Context, claims *auth.Claims, id string) (*domain.Route, error) {
	var route *domain.Route
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		repo := fleetrepo.NewRouteRepository(tx)
		var err error
		route, err = repo.FindByID(ctx, id)
		if err != nil {
			return err
		}
		route.Stops, err = repo.ListStops(ctx, id)
		return err
	})
	return route, err
}

func (s *RouteService) List(ctx context.Context, claims *auth.Claims, cursor string, limit int) ([]*domain.Route, string, error) {
	var routes []*domain.Route
	var nextCursor string
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		repo := fleetrepo.NewRouteRepository(tx)
		var err error
		routes, nextCursor, err = repo.ListByOperator(ctx, claims.OperatorID, cursor, limit)
		return err
	})
	return routes, nextCursor, err
}

type UpdateRouteInput struct {
	Origin      string
	Destination string
	DistanceKm  *float64
	Stops       []domain.RouteStop
}

func (s *RouteService) Update(ctx context.Context, claims *auth.Claims, id string, in UpdateRouteInput) (*domain.Route, error) {
	var route *domain.Route
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		repo := fleetrepo.NewRouteRepository(tx)
		var err error
		route, err = repo.Update(ctx, &domain.Route{
			ID:          id,
			Origin:      in.Origin,
			Destination: in.Destination,
			DistanceKm:  in.DistanceKm,
		})
		if err != nil {
			return err
		}
		if err := repo.ReplaceStops(ctx, id, in.Stops); err != nil {
			return err
		}
		route.Stops = in.Stops
		return nil
	})
	return route, err
}
