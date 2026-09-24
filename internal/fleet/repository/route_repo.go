package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sl-bus/backend/internal/fleet/domain"
	"github.com/sl-bus/backend/internal/platform/sqlcgen"
)

type RouteRepository struct {
	q *sqlcgen.Queries
}

func NewRouteRepository(db sqlcgen.DBTX) *RouteRepository {
	return &RouteRepository{q: sqlcgen.New(db)}
}

func (r *RouteRepository) Create(ctx context.Context, route *domain.Route) (*domain.Route, error) {
	operatorID, err := parseUUID(route.OperatorID)
	if err != nil {
		return nil, err
	}
	row, err := r.q.CreateRoute(ctx, sqlcgen.CreateRouteParams{
		OperatorID:  operatorID,
		Origin:      route.Origin,
		Destination: route.Destination,
		DistanceKm:  numericPtr(route.DistanceKm),
	})
	if err != nil {
		return nil, err
	}
	return toDomainRoute(row), nil
}

func (r *RouteRepository) FindByID(ctx context.Context, id string) (*domain.Route, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	row, err := r.q.GetRouteByID(ctx, uid)
	if err != nil {
		return nil, mapRouteNotFound(err)
	}
	return toDomainRoute(row), nil
}

func (r *RouteRepository) ListByOperator(ctx context.Context, operatorID, cursor string, limit int) ([]*domain.Route, string, error) {
	opID, err := parseUUID(operatorID)
	if err != nil {
		return nil, "", err
	}
	params := sqlcgen.ListRoutesByOperatorParams{OperatorID: opID, Limit: int32(limit)}
	if cursor != "" {
		createdAt, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		params.CursorCreatedAt = pgtype.Timestamptz{Time: createdAt, Valid: true}
		cid, err := parseUUID(id)
		if err != nil {
			return nil, "", err
		}
		params.CursorID = cid
	}
	rows, err := r.q.ListRoutesByOperator(ctx, params)
	if err != nil {
		return nil, "", err
	}
	out := make([]*domain.Route, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomainRoute(row))
	}
	nextCursor := ""
	if len(out) == limit {
		last := out[len(out)-1]
		nextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	return out, nextCursor, nil
}

func (r *RouteRepository) Update(ctx context.Context, route *domain.Route) (*domain.Route, error) {
	uid, err := parseUUID(route.ID)
	if err != nil {
		return nil, err
	}
	row, err := r.q.UpdateRoute(ctx, sqlcgen.UpdateRouteParams{
		ID:          uid,
		Origin:      route.Origin,
		Destination: route.Destination,
		DistanceKm:  numericPtr(route.DistanceKm),
	})
	if err != nil {
		return nil, mapRouteNotFound(err)
	}
	return toDomainRoute(row), nil
}

func (r *RouteRepository) ReplaceStops(ctx context.Context, routeID string, stops []domain.RouteStop) error {
	uid, err := parseUUID(routeID)
	if err != nil {
		return err
	}
	if err := r.q.DeleteRouteStops(ctx, uid); err != nil {
		return err
	}
	for _, s := range stops {
		if _, err := r.q.CreateRouteStop(ctx, sqlcgen.CreateRouteStopParams{
			RouteID:            uid,
			StopName:           s.StopName,
			Sequence:           int32(s.Sequence),
			ArrivalOffsetMin:   int4Ptr(s.ArrivalOffsetMin),
			DepartureOffsetMin: int4Ptr(s.DepartureOffsetMin),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *RouteRepository) ListStops(ctx context.Context, routeID string) ([]domain.RouteStop, error) {
	uid, err := parseUUID(routeID)
	if err != nil {
		return nil, err
	}
	rows, err := r.q.ListRouteStopsByRouteID(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := make([]domain.RouteStop, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.RouteStop{
			StopName:           row.StopName,
			Sequence:           int(row.Sequence),
			ArrivalOffsetMin:   int4ToIntPtr(row.ArrivalOffsetMin),
			DepartureOffsetMin: int4ToIntPtr(row.DepartureOffsetMin),
		})
	}
	return out, nil
}

func toDomainRoute(row sqlcgen.FleetRoute) *domain.Route {
	return &domain.Route{
		ID:          uuidString(row.ID),
		OperatorID:  uuidString(row.OperatorID),
		Origin:      row.Origin,
		Destination: row.Destination,
		DistanceKm:  numericToFloat64Ptr(row.DistanceKm),
		CreatedAt:   row.CreatedAt.Time,
		UpdatedAt:   row.UpdatedAt.Time,
	}
}

func mapRouteNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrRouteNotFound
	}
	return err
}
