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

// SeatMapService backs FR-FLT-2 — the operator's seat-map canvas editor
// always sends the whole configured layout back, so Replace is the only
// write operation (no incremental add/remove-seat endpoints).
type SeatMapService struct {
	pool *pgxpool.Pool
}

func NewSeatMapService(pool *pgxpool.Pool) *SeatMapService {
	return &SeatMapService{pool: pool}
}

func (s *SeatMapService) Replace(ctx context.Context, claims *auth.Claims, busID string, seats []domain.Seat) (*domain.SeatMap, error) {
	if err := domain.ValidateSeats(seats); err != nil {
		return nil, err
	}
	var seatMap *domain.SeatMap
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		repo := fleetrepo.NewSeatMapRepository(tx)
		var err error
		seatMap, err = repo.Replace(ctx, busID, seats)
		return err
	})
	if err != nil {
		return nil, err
	}
	return seatMap, nil
}

func (s *SeatMapService) Get(ctx context.Context, claims *auth.Claims, busID string) (*domain.SeatMap, error) {
	var seatMap *domain.SeatMap
	err := platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		repo := fleetrepo.NewSeatMapRepository(tx)
		var err error
		seatMap, err = repo.FindByBusID(ctx, busID)
		return err
	})
	return seatMap, err
}
