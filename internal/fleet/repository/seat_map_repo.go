package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/sl-bus/backend/internal/fleet/domain"
	"github.com/sl-bus/backend/internal/platform/sqlcgen"
)

type SeatMapRepository struct {
	q *sqlcgen.Queries
}

func NewSeatMapRepository(db sqlcgen.DBTX) *SeatMapRepository {
	return &SeatMapRepository{q: sqlcgen.New(db)}
}

// Replace upserts the seat_maps row and swaps out every fleet.seats row
// for busID — the seat-map canvas editor always sends the whole
// configured layout back, so delete-then-bulk-insert within one
// tenant-scoped transaction is simpler and safer than diffing. Atomicity
// is the caller's responsibility: this must be called with an instance
// constructed against a pgx.Tx from platform/db.WithTenantContext.
func (r *SeatMapRepository) Replace(ctx context.Context, busID string, seats []domain.Seat) (*domain.SeatMap, error) {
	busUUID, err := parseUUID(busID)
	if err != nil {
		return nil, err
	}

	seatMapRow, err := r.q.UpsertSeatMap(ctx, sqlcgen.UpsertSeatMapParams{
		BusID:      busUUID,
		TotalSeats: int32(len(seats)),
	})
	if err != nil {
		return nil, err
	}

	if err := r.q.DeleteSeatsForSeatMap(ctx, seatMapRow.ID); err != nil {
		return nil, err
	}

	out := make([]domain.Seat, 0, len(seats))
	for _, s := range seats {
		row, err := r.q.CreateSeat(ctx, sqlcgen.CreateSeatParams{
			SeatMapID:  seatMapRow.ID,
			SeatNumber: s.Number,
			SeatType:   s.Type,
			RowNo:      int32(s.Row),
			ColNo:      int32(s.Col),
		})
		if err != nil {
			return nil, err
		}
		out = append(out, toDomainSeat(row))
	}

	return &domain.SeatMap{
		BusID:      uuidString(seatMapRow.BusID),
		TotalSeats: int(seatMapRow.TotalSeats),
		Seats:      out,
		UpdatedAt:  seatMapRow.UpdatedAt.Time,
	}, nil
}

func (r *SeatMapRepository) FindByBusID(ctx context.Context, busID string) (*domain.SeatMap, error) {
	busUUID, err := parseUUID(busID)
	if err != nil {
		return nil, err
	}
	seatMapRow, err := r.q.GetSeatMapByBusID(ctx, busUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	seatRows, err := r.q.ListSeatsBySeatMapID(ctx, seatMapRow.ID)
	if err != nil {
		return nil, err
	}
	seats := make([]domain.Seat, 0, len(seatRows))
	for _, row := range seatRows {
		seats = append(seats, toDomainSeat(row))
	}
	return &domain.SeatMap{
		BusID:      uuidString(seatMapRow.BusID),
		TotalSeats: int(seatMapRow.TotalSeats),
		Seats:      seats,
		UpdatedAt:  seatMapRow.UpdatedAt.Time,
	}, nil
}

func toDomainSeat(row sqlcgen.FleetSeat) domain.Seat {
	return domain.Seat{
		Number: row.SeatNumber,
		Type:   row.SeatType,
		Row:    int(row.RowNo),
		Col:    int(row.ColNo),
	}
}
