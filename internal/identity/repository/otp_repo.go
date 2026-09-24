package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sl-bus/backend/internal/identity/domain"
	"github.com/sl-bus/backend/internal/platform/sqlcgen"
)

type OTPRepository struct {
	q *sqlcgen.Queries
}

func NewOTPRepository(db sqlcgen.DBTX) *OTPRepository {
	return &OTPRepository{q: sqlcgen.New(db)}
}

func (r *OTPRepository) Create(ctx context.Context, phone, codeHash string, expiresAt time.Time) (*domain.OTPCode, error) {
	row, err := r.q.CreateOTPCode(ctx, sqlcgen.CreateOTPCodeParams{
		Phone:     phone,
		CodeHash:  codeHash,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	return toDomainOTP(row), nil
}

func (r *OTPRepository) FindLatestUnconsumed(ctx context.Context, phone string) (*domain.OTPCode, error) {
	row, err := r.q.GetLatestUnconsumedOTP(ctx, phone)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return toDomainOTP(row), nil
}

func (r *OTPRepository) MarkConsumed(ctx context.Context, id string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	return r.q.ConsumeOTPCode(ctx, uid)
}

func (r *OTPRepository) CountSince(ctx context.Context, phone string, since time.Time) (int, error) {
	n, err := r.q.CountOTPCodesSince(ctx, sqlcgen.CountOTPCodesSinceParams{
		Phone:     phone,
		CreatedAt: pgtype.Timestamptz{Time: since, Valid: true},
	})
	return int(n), err
}

func toDomainOTP(row sqlcgen.IdentityOtpCode) *domain.OTPCode {
	o := &domain.OTPCode{
		ID:        uuidString(row.ID),
		Phone:     row.Phone,
		CodeHash:  row.CodeHash,
		ExpiresAt: row.ExpiresAt.Time,
		CreatedAt: row.CreatedAt.Time,
	}
	if row.ConsumedAt.Valid {
		t := row.ConsumedAt.Time
		o.ConsumedAt = &t
	}
	return o
}
