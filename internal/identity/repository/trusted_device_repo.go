package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sl-bus/backend/internal/identity/domain"
	"github.com/sl-bus/backend/internal/platform/sqlcgen"
)

type TrustedDeviceRepository struct {
	q *sqlcgen.Queries
}

func NewTrustedDeviceRepository(db sqlcgen.DBTX) *TrustedDeviceRepository {
	return &TrustedDeviceRepository{q: sqlcgen.New(db)}
}

func (r *TrustedDeviceRepository) Find(ctx context.Context, userID, deviceFingerprint string) (*domain.TrustedDevice, error) {
	uid, err := parseUUID(userID)
	if err != nil {
		return nil, err
	}
	row, err := r.q.GetTrustedDevice(ctx, sqlcgen.GetTrustedDeviceParams{
		UserID:            uid,
		DeviceFingerprint: deviceFingerprint,
	})
	if err != nil {
		return nil, mapNotFound(err)
	}
	return toDomainTrustedDevice(row), nil
}

func (r *TrustedDeviceRepository) Upsert(ctx context.Context, userID, deviceFingerprint string, trustedUntil time.Time) (*domain.TrustedDevice, error) {
	uid, err := parseUUID(userID)
	if err != nil {
		return nil, err
	}
	row, err := r.q.UpsertTrustedDevice(ctx, sqlcgen.UpsertTrustedDeviceParams{
		UserID:            uid,
		DeviceFingerprint: deviceFingerprint,
		TrustedUntil:      pgtype.Timestamptz{Time: trustedUntil, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	return toDomainTrustedDevice(row), nil
}

func toDomainTrustedDevice(row sqlcgen.IdentityTrustedDevice) *domain.TrustedDevice {
	return &domain.TrustedDevice{
		ID:                uuidString(row.ID),
		UserID:            uuidString(row.UserID),
		DeviceFingerprint: row.DeviceFingerprint,
		TrustedUntil:      row.TrustedUntil.Time,
	}
}
