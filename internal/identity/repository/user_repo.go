// Package repository adapts internal/identity/domain's ports to Postgres
// via sqlc-generated queries (ARCHITECTURE.md §3.2).
package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sl-bus/backend/internal/identity/domain"
	"github.com/sl-bus/backend/internal/platform/sqlcgen"
)

// usersEmailUniqueConstraint is Postgres's default name for the UNIQUE on
// identity.users.email (000002_identity_schema.up.sql).
const usersEmailUniqueConstraint = "users_email_key"

// UserRepository adapts sqlcgen.Queries to domain.UserRepository. db is a
// DBTX — either the shared pool or a specific pgx.Tx — so the same
// implementation works for both plain and transaction-scoped calls.
type UserRepository struct {
	q *sqlcgen.Queries
}

func NewUserRepository(db sqlcgen.DBTX) *UserRepository {
	return &UserRepository{q: sqlcgen.New(db)}
}

func (r *UserRepository) FindByEmail(ctx context.Context, email string) (*domain.User, error) {
	row, err := r.q.GetUserByEmail(ctx, text(email))
	if err != nil {
		return nil, mapUserNotFound(err)
	}
	return toDomainUser(row), nil
}

func (r *UserRepository) FindByPhone(ctx context.Context, phone string) (*domain.User, error) {
	row, err := r.q.GetUserByPhone(ctx, text(phone))
	if err != nil {
		return nil, mapUserNotFound(err)
	}
	return toDomainUser(row), nil
}

func (r *UserRepository) FindByID(ctx context.Context, id string) (*domain.User, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	row, err := r.q.GetUserByID(ctx, uid)
	if err != nil {
		return nil, mapUserNotFound(err)
	}
	return toDomainUser(row), nil
}

func (r *UserRepository) Create(ctx context.Context, u *domain.User) (*domain.User, error) {
	operatorID := pgtype.UUID{}
	if u.OperatorID != "" {
		uid, err := parseUUID(u.OperatorID)
		if err != nil {
			return nil, err
		}
		operatorID = uid
	}
	row, err := r.q.CreateUser(ctx, sqlcgen.CreateUserParams{
		Phone:         text(u.Phone),
		Email:         text(u.Email),
		PasswordHash:  text(u.PasswordHash),
		Role:          string(u.Role),
		OperatorID:    operatorID,
		MfaTotpSecret: text(u.MFATOTPSecret),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == usersEmailUniqueConstraint {
			return nil, domain.ErrEmailTaken
		}
		return nil, err
	}
	return toDomainUser(row), nil
}

func (r *UserRepository) CreatePassenger(ctx context.Context, phone string) (*domain.User, error) {
	row, err := r.q.CreatePassengerUser(ctx, text(phone))
	if err != nil {
		return nil, err
	}
	return toDomainUser(row), nil
}

func (r *UserRepository) IncrementFailedAttempts(ctx context.Context, id string) (*domain.User, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	row, err := r.q.IncrementFailedLoginAttempts(ctx, uid)
	if err != nil {
		return nil, err
	}
	return toDomainUser(row), nil
}

func (r *UserRepository) SetLockedUntil(ctx context.Context, id string, until time.Time) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	return r.q.SetUserLockedUntil(ctx, sqlcgen.SetUserLockedUntilParams{
		ID:          uid,
		LockedUntil: pgtype.Timestamptz{Time: until, Valid: true},
	})
}

func (r *UserRepository) ResetLockout(ctx context.Context, id string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	return r.q.ResetUserLockout(ctx, uid)
}

func (r *UserRepository) SetMFASecret(ctx context.Context, id string, secret string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	return r.q.SetUserMFASecret(ctx, sqlcgen.SetUserMFASecretParams{ID: uid, MfaTotpSecret: text(secret)})
}

func (r *UserRepository) UpdatePassword(ctx context.Context, id string, passwordHash string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	return r.q.UpdateUserPassword(ctx, sqlcgen.UpdateUserPasswordParams{ID: uid, PasswordHash: text(passwordHash)})
}

func toDomainUser(row sqlcgen.IdentityUser) *domain.User {
	u := &domain.User{
		ID:                  uuidString(row.ID),
		Phone:               row.Phone.String,
		Email:               row.Email.String,
		PasswordHash:        row.PasswordHash.String,
		Role:                domain.Role(row.Role),
		OperatorID:          uuidString(row.OperatorID),
		MFATOTPSecret:       row.MfaTotpSecret.String,
		FailedLoginAttempts: int(row.FailedLoginAttempts),
		CreatedAt:           row.CreatedAt.Time,
		UpdatedAt:           row.UpdatedAt.Time,
	}
	if row.LockedUntil.Valid {
		t := row.LockedUntil.Time
		u.LockedUntil = &t
	}
	return u
}

func mapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

func mapUserNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrUserNotFound
	}
	return err
}
