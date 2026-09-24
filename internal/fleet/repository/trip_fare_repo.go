package repository

import (
	"context"

	"github.com/sl-bus/backend/internal/fleet/domain"
	"github.com/sl-bus/backend/internal/platform/sqlcgen"
)

type TripFareRepository struct {
	q *sqlcgen.Queries
}

func NewTripFareRepository(db sqlcgen.DBTX) *TripFareRepository {
	return &TripFareRepository{q: sqlcgen.New(db)}
}

func (r *TripFareRepository) Create(ctx context.Context, tripID string, fare domain.TripFare) (*domain.TripFare, error) {
	uid, err := parseUUID(tripID)
	if err != nil {
		return nil, err
	}
	row, err := r.q.CreateTripFare(ctx, sqlcgen.CreateTripFareParams{
		TripID:   uid,
		SeatType: fare.SeatType,
		Fare:     numeric(fare.Fare),
	})
	if err != nil {
		return nil, err
	}
	f := toDomainTripFare(row)
	return &f, nil
}

func (r *TripFareRepository) ListByTripID(ctx context.Context, tripID string) ([]domain.TripFare, error) {
	uid, err := parseUUID(tripID)
	if err != nil {
		return nil, err
	}
	rows, err := r.q.ListTripFaresByTripID(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := make([]domain.TripFare, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomainTripFare(row))
	}
	return out, nil
}

func (r *TripFareRepository) Update(ctx context.Context, tripID, seatType string, fare float64) (*domain.TripFare, error) {
	uid, err := parseUUID(tripID)
	if err != nil {
		return nil, err
	}
	row, err := r.q.UpdateTripFare(ctx, sqlcgen.UpdateTripFareParams{
		TripID:   uid,
		SeatType: seatType,
		Fare:     numeric(fare),
	})
	if err != nil {
		return nil, err
	}
	f := toDomainTripFare(row)
	return &f, nil
}

func toDomainTripFare(row sqlcgen.FleetTripFare) domain.TripFare {
	return domain.TripFare{
		SeatType: row.SeatType,
		Fare:     numericToFloat64(row.Fare),
	}
}
