// Package repository adapts internal/ops/domain's OperatorRepository port
// to Postgres via sqlc-generated queries (ARCHITECTURE.md §3.2).
package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sl-bus/backend/internal/ops/domain"
	"github.com/sl-bus/backend/internal/platform/sqlcgen"
)

// postgresUniqueViolation is the SQLSTATE for a unique-constraint
// violation (business_reg_no's UNIQUE constraint).
const postgresUniqueViolation = "23505"

// OperatorRepository's db is a DBTX — the shared pool for read-only
// listing/lookup, or a specific pgx.Tx when the caller needs RLS session
// variables and/or cross-context atomicity (e.g. registration_service's
// operator+user transaction).
type OperatorRepository struct {
	q *sqlcgen.Queries
}

func NewOperatorRepository(db sqlcgen.DBTX) *OperatorRepository {
	return &OperatorRepository{q: sqlcgen.New(db)}
}

// Create requires o.ID to already be set (RegistrationService generates
// it before opening the tenant-scoped transaction — see its comment for
// why: a self-registration insert must run with app.current_operator_id
// already equal to the new row's id for the RLS SELECT policy backing
// this query's RETURNING clause to see the row it just created).
func (r *OperatorRepository) Create(ctx context.Context, o *domain.Operator) (*domain.Operator, error) {
	if o.ID == "" {
		o.ID = uuid.NewString()
	}
	id, err := parseUUID(o.ID)
	if err != nil {
		return nil, err
	}
	row, err := r.q.CreateOperator(ctx, sqlcgen.CreateOperatorParams{
		ID:                 id,
		Name:               o.Name,
		BusinessRegNo:      o.BusinessRegNo,
		ContactEmail:       text(o.ContactEmail),
		ContactPhone:       text(o.ContactPhone),
		RegistrationDocUrl: text(o.RegistrationDocURL),
	})
	if err != nil {
		// RLS means a pre-check SELECT by business_reg_no can't see
		// another tenant's row to catch this ahead of time (see
		// RegistrationService) — the UNIQUE constraint is the real
		// enforcement point, mapped here to the domain error.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == postgresUniqueViolation {
			return nil, domain.ErrDuplicateBusinessRegNo
		}
		return nil, err
	}
	return toDomainOperator(row), nil
}

func (r *OperatorRepository) FindByID(ctx context.Context, id string) (*domain.Operator, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	row, err := r.q.GetOperatorByID(ctx, uid)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return toDomainOperator(row), nil
}

func (r *OperatorRepository) FindByBusinessRegNo(ctx context.Context, regNo string) (*domain.Operator, error) {
	row, err := r.q.GetOperatorByBusinessRegNo(ctx, regNo)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return toDomainOperator(row), nil
}

func (r *OperatorRepository) ListByStatus(ctx context.Context, status domain.OperatorStatus, cursor string, limit int) ([]*domain.Operator, string, error) {
	params := sqlcgen.ListOperatorsByStatusParams{
		Status: string(status),
		Limit:  int32(limit),
	}
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

	rows, err := r.q.ListOperatorsByStatus(ctx, params)
	if err != nil {
		return nil, "", err
	}
	out := make([]*domain.Operator, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomainOperator(row))
	}
	nextCursor := ""
	if len(out) == limit {
		last := out[len(out)-1]
		nextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	return out, nextCursor, nil
}

func (r *OperatorRepository) UpdateStatus(ctx context.Context, id string, status domain.OperatorStatus) (*domain.Operator, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	row, err := r.q.UpdateOperatorStatus(ctx, sqlcgen.UpdateOperatorStatusParams{ID: uid, Status: string(status)})
	if err != nil {
		return nil, mapNotFound(err)
	}
	return toDomainOperator(row), nil
}

func toDomainOperator(row sqlcgen.OpsOperator) *domain.Operator {
	o := &domain.Operator{
		ID:                 uuidString(row.ID),
		Name:               row.Name,
		BusinessRegNo:      row.BusinessRegNo,
		Status:             domain.OperatorStatus(row.Status),
		ContactEmail:       row.ContactEmail.String,
		ContactPhone:       row.ContactPhone.String,
		RegistrationDocURL: row.RegistrationDocUrl.String,
		CreatedAt:          row.CreatedAt.Time,
	}
	if row.CommissionRate.Valid {
		if f, err := row.CommissionRate.Float64Value(); err == nil && f.Valid {
			o.CommissionRate = &f.Float64
		}
	}
	if row.ApprovedAt.Valid {
		t := row.ApprovedAt.Time
		o.ApprovedAt = &t
	}
	return o
}

func mapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrOperatorNotFound
	}
	return err
}
