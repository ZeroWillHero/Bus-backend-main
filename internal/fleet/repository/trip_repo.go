package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sl-bus/backend/internal/fleet/domain"
	"github.com/sl-bus/backend/internal/platform/sqlcgen"
)

type TripRepository struct {
	q *sqlcgen.Queries
}

func NewTripRepository(db sqlcgen.DBTX) *TripRepository {
	return &TripRepository{q: sqlcgen.New(db)}
}

func (r *TripRepository) Create(ctx context.Context, t *domain.Trip) (*domain.Trip, error) {
	operatorID, err := parseUUID(t.OperatorID)
	if err != nil {
		return nil, err
	}
	routeID, err := parseUUID(t.RouteID)
	if err != nil {
		return nil, err
	}
	busID, err := parseUUID(t.BusID)
	if err != nil {
		return nil, err
	}
	row, err := r.q.CreateTrip(ctx, sqlcgen.CreateTripParams{
		OperatorID:     operatorID,
		RouteID:        routeID,
		BusID:          busID,
		DepartureAt:    pgtype.Timestamptz{Time: t.DepartureAt, Valid: true},
		BaseFare:       numeric(t.BaseFare),
		AvailableSeats: int32(t.AvailableSeats),
	})
	if err != nil {
		return nil, err
	}
	return toDomainTrip(row), nil
}

func (r *TripRepository) FindByID(ctx context.Context, id string) (*domain.Trip, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	row, err := r.q.GetTripByID(ctx, uid)
	if err != nil {
		return nil, mapTripNotFound(err)
	}
	return toDomainTrip(row), nil
}

func (r *TripRepository) ListByOperator(ctx context.Context, operatorID string, filter domain.TripListFilter, cursor string, limit int) ([]*domain.Trip, string, error) {
	opID, err := parseUUID(operatorID)
	if err != nil {
		return nil, "", err
	}
	params := sqlcgen.ListTripsByOperatorParams{OperatorID: opID, Limit: int32(limit)}
	if filter.RouteID != "" {
		rid, err := parseUUID(filter.RouteID)
		if err != nil {
			return nil, "", err
		}
		params.RouteID = rid
	}
	if filter.Status != "" {
		params.Status = text(filter.Status)
	}
	if cursor != "" {
		departureAt, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		params.CursorDepartureAt = pgtype.Timestamptz{Time: departureAt, Valid: true}
		cid, err := parseUUID(id)
		if err != nil {
			return nil, "", err
		}
		params.CursorID = cid
	}
	rows, err := r.q.ListTripsByOperator(ctx, params)
	if err != nil {
		return nil, "", err
	}
	out := make([]*domain.Trip, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomainTrip(row))
	}
	nextCursor := ""
	if len(out) == limit {
		last := out[len(out)-1]
		nextCursor = encodeCursor(last.DepartureAt, last.ID)
	}
	return out, nextCursor, nil
}

func (r *TripRepository) UpdateStatus(ctx context.Context, id string, status domain.TripStatus) (*domain.Trip, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	row, err := r.q.UpdateTripStatus(ctx, sqlcgen.UpdateTripStatusParams{ID: uid, Status: string(status)})
	if err != nil {
		return nil, mapTripNotFound(err)
	}
	return toDomainTrip(row), nil
}

func (r *TripRepository) Reschedule(ctx context.Context, id string, departureAt time.Time) (*domain.Trip, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	row, err := r.q.RescheduleTrip(ctx, sqlcgen.RescheduleTripParams{
		ID:          uid,
		DepartureAt: pgtype.Timestamptz{Time: departureAt, Valid: true},
	})
	if err != nil {
		return nil, mapTripNotFound(err)
	}
	return toDomainTrip(row), nil
}

func (r *TripRepository) SeedSeatInventory(ctx context.Context, tripID string, seatNumbers []string) error {
	uid, err := parseUUID(tripID)
	if err != nil {
		return err
	}
	for _, seatNumber := range seatNumbers {
		if err := r.q.CreateTripSeatInventory(ctx, sqlcgen.CreateTripSeatInventoryParams{
			TripID:     uid,
			SeatNumber: seatNumber,
		}); err != nil {
			return err
		}
	}
	return nil
}

func toDomainTrip(row sqlcgen.FleetTrip) *domain.Trip {
	return &domain.Trip{
		ID:             uuidString(row.ID),
		OperatorID:     uuidString(row.OperatorID),
		RouteID:        uuidString(row.RouteID),
		BusID:          uuidString(row.BusID),
		DepartureAt:    row.DepartureAt.Time,
		Status:         domain.TripStatus(row.Status),
		BaseFare:       numericToFloat64(row.BaseFare),
		AvailableSeats: int(row.AvailableSeats),
		CreatedAt:      row.CreatedAt.Time,
		UpdatedAt:      row.UpdatedAt.Time,
	}
}

func mapTripNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrTripNotFound
	}
	return err
}
