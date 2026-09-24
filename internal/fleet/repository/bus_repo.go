package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sl-bus/backend/internal/fleet/domain"
	"github.com/sl-bus/backend/internal/platform/sqlcgen"
)

const postgresUniqueViolation = "23505"

type BusRepository struct {
	q *sqlcgen.Queries
}

func NewBusRepository(db sqlcgen.DBTX) *BusRepository {
	return &BusRepository{q: sqlcgen.New(db)}
}

func (r *BusRepository) Create(ctx context.Context, b *domain.Bus) (*domain.Bus, error) {
	operatorID, err := parseUUID(b.OperatorID)
	if err != nil {
		return nil, err
	}
	row, err := r.q.CreateBus(ctx, sqlcgen.CreateBusParams{
		OperatorID:     operatorID,
		RegistrationNo: b.RegistrationNo,
		BusClass:       b.BusClass,
		Amenities:      amenitiesToJSON(b.Amenities),
		Active:         b.Active,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == postgresUniqueViolation {
			return nil, domain.ErrDuplicateRegistrationNo
		}
		return nil, err
	}
	return toDomainBus(row), nil
}

func (r *BusRepository) FindByID(ctx context.Context, id string) (*domain.Bus, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	row, err := r.q.GetBusByID(ctx, uid)
	if err != nil {
		return nil, mapBusNotFound(err)
	}
	return toDomainBus(row), nil
}

func (r *BusRepository) ListByOperator(ctx context.Context, operatorID, cursor string, limit int) ([]*domain.Bus, string, error) {
	opID, err := parseUUID(operatorID)
	if err != nil {
		return nil, "", err
	}
	params := sqlcgen.ListBusesByOperatorParams{OperatorID: opID, Limit: int32(limit)}
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
	rows, err := r.q.ListBusesByOperator(ctx, params)
	if err != nil {
		return nil, "", err
	}
	out := make([]*domain.Bus, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomainBus(row))
	}
	nextCursor := ""
	if len(out) == limit {
		last := out[len(out)-1]
		nextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	return out, nextCursor, nil
}

func (r *BusRepository) Update(ctx context.Context, b *domain.Bus) (*domain.Bus, error) {
	uid, err := parseUUID(b.ID)
	if err != nil {
		return nil, err
	}
	row, err := r.q.UpdateBus(ctx, sqlcgen.UpdateBusParams{
		ID:             uid,
		RegistrationNo: b.RegistrationNo,
		BusClass:       b.BusClass,
		Amenities:      amenitiesToJSON(b.Amenities),
		Active:         b.Active,
	})
	if err != nil {
		return nil, mapBusNotFound(err)
	}
	return toDomainBus(row), nil
}

func toDomainBus(row sqlcgen.FleetBus) *domain.Bus {
	return &domain.Bus{
		ID:             uuidString(row.ID),
		OperatorID:     uuidString(row.OperatorID),
		RegistrationNo: row.RegistrationNo,
		BusClass:       row.BusClass,
		Amenities:      amenitiesFromJSON(row.Amenities),
		Active:         row.Active,
		CreatedAt:      row.CreatedAt.Time,
		UpdatedAt:      row.UpdatedAt.Time,
	}
}

func mapBusNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrBusNotFound
	}
	return err
}
