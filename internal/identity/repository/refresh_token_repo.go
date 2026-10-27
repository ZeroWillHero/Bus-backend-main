package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sl-bus/backend/internal/identity/domain"
	"github.com/sl-bus/backend/internal/platform/sqlcgen"
)

type RefreshTokenRepository struct {
	q *sqlcgen.Queries
}

func NewRefreshTokenRepository(db sqlcgen.DBTX) *RefreshTokenRepository {
	return &RefreshTokenRepository{q: sqlcgen.New(db)}
}

func (r *RefreshTokenRepository) Create(ctx context.Context, userID, tokenHash, deviceInfo string, expiresAt time.Time) (*domain.RefreshToken, error) {
	uid, err := parseUUID(userID)
	if err != nil {
		return nil, err
	}
	row, err := r.q.CreateRefreshToken(ctx, sqlcgen.CreateRefreshTokenParams{
		UserID:     uid,
		TokenHash:  tokenHash,
		DeviceInfo: text(deviceInfo),
		ExpiresAt:  pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	return toDomainRefreshToken(row), nil
}

func (r *RefreshTokenRepository) FindByHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	row, err := r.q.GetRefreshTokenByHash(ctx, tokenHash)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return toDomainRefreshToken(row), nil
}

// Revoke revokes one token and reports whether this call did it — false
// means it was already revoked (or doesn't exist), which is how
// concurrent rotations of the same token are told apart.
func (r *RefreshTokenRepository) Revoke(ctx context.Context, id string) (bool, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return false, err
	}
	n, err := r.q.RevokeRefreshToken(ctx, uid)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// RevokeAllForOperator revokes every refresh token belonging to any user of
// operatorID — used when the operator is suspended (ops approval service).
func (r *RefreshTokenRepository) RevokeAllForOperator(ctx context.Context, operatorID string) error {
	oid, err := parseUUID(operatorID)
	if err != nil {
		return err
	}
	return r.q.RevokeAllRefreshTokensForOperator(ctx, oid)
}

func (r *RefreshTokenRepository) RevokeAllForUser(ctx context.Context, userID string) error {
	uid, err := parseUUID(userID)
	if err != nil {
		return err
	}
	return r.q.RevokeAllRefreshTokensForUser(ctx, uid)
}

func (r *RefreshTokenRepository) ListActiveForUser(ctx context.Context, userID string) ([]*domain.RefreshToken, error) {
	uid, err := parseUUID(userID)
	if err != nil {
		return nil, err
	}
	rows, err := r.q.ListActiveRefreshTokensForUser(ctx, uid)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.RefreshToken, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomainRefreshToken(row))
	}
	return out, nil
}

func toDomainRefreshToken(row sqlcgen.IdentityRefreshToken) *domain.RefreshToken {
	t := &domain.RefreshToken{
		ID:         uuidString(row.ID),
		UserID:     uuidString(row.UserID),
		TokenHash:  row.TokenHash,
		DeviceInfo: row.DeviceInfo.String,
		ExpiresAt:  row.ExpiresAt.Time,
		CreatedAt:  row.CreatedAt.Time,
	}
	if row.RevokedAt.Valid {
		rt := row.RevokedAt.Time
		t.RevokedAt = &rt
	}
	return t
}
