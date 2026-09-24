// Package service implements the Operator & Multi-Tenant Management
// context's use cases (ARCHITECTURE.md §3.2).
package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	identitydomain "github.com/sl-bus/backend/internal/identity/domain"
	identityrepo "github.com/sl-bus/backend/internal/identity/repository"
	"github.com/sl-bus/backend/internal/ops/domain"
	opsrepo "github.com/sl-bus/backend/internal/ops/repository"
	"github.com/sl-bus/backend/internal/platform/auth"
	platformdb "github.com/sl-bus/backend/internal/platform/db"
)

// RegisterOperatorInput is the operator self-registration request
// (FR-OPS-1) — collects both the business profile and the account
// (email+password) that logs into it, created together in one
// transaction.
type RegisterOperatorInput struct {
	BusinessName       string
	BusinessRegNo      string
	ContactEmail       string
	ContactPhone       string
	RegistrationDocURL string
	Password           string
}

// RegistrationService is the cross-context use case: it owns both
// ops.operators and identity.users creation. It lives here (not in
// identity/service) because ops depends on identity per the
// bounded-context map's dotted "referenced by" edge, not the reverse.
type RegistrationService struct {
	pool          *pgxpool.Pool
	breachChecker auth.PasswordBreachChecker
}

func NewRegistrationService(pool *pgxpool.Pool, breachChecker auth.PasswordBreachChecker) *RegistrationService {
	return &RegistrationService{pool: pool, breachChecker: breachChecker}
}

// RegisterOperator creates the operator (status=pending) and its
// operator_admin user in a single transaction — the two rows must never
// exist independently of each other.
//
// The operator's id is generated here, before the transaction opens, and
// used as this transaction's own app.current_operator_id (via
// platform/db.WithTenantContext) — a self-registering operator's
// transaction scopes itself to the tenant it is in the middle of
// creating. This isn't just tidy: without it, the RLS SELECT policy
// backing this insert's RETURNING clause could never see the row it just
// created (a brand-new row's id can't match a pre-existing session
// context, and there is no pre-existing context here at all).
func (s *RegistrationService) RegisterOperator(ctx context.Context, in RegisterOperatorInput) (*domain.Operator, *identitydomain.User, error) {
	if err := validatePasswordLength(in.Password); err != nil {
		return nil, nil, err
	}
	breached, err := s.breachChecker.IsBreached(ctx, in.Password)
	if err != nil {
		return nil, nil, err
	}
	if breached {
		return nil, nil, ErrPasswordBreached
	}
	passwordHash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, nil, err
	}

	operatorID := uuid.NewString()
	claims := &auth.Claims{Role: identitydomain.RoleOperatorAdmin, OperatorID: operatorID}

	var operator *domain.Operator
	var user *identitydomain.User

	err = platformdb.WithTenantContext(ctx, s.pool, claims, func(ctx context.Context, tx pgx.Tx) error {
		operatorRepo := opsrepo.NewOperatorRepository(tx)
		operator, err = operatorRepo.Create(ctx, &domain.Operator{
			ID:                 operatorID,
			Name:               in.BusinessName,
			BusinessRegNo:      in.BusinessRegNo,
			ContactEmail:       in.ContactEmail,
			ContactPhone:       in.ContactPhone,
			RegistrationDocURL: in.RegistrationDocURL,
		})
		if err != nil {
			return err
		}

		userRepo := identityrepo.NewUserRepository(tx)
		user, err = userRepo.Create(ctx, &identitydomain.User{
			Email:        in.ContactEmail,
			PasswordHash: passwordHash,
			Role:         identitydomain.RoleOperatorAdmin,
			OperatorID:   operator.ID,
		})
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return operator, user, nil
}

// MinPasswordLength follows NIST 800-63B guidance (length over forced
// complexity rules) per AUTHENTICATION_DESIGN.md §3.4.
const MinPasswordLength = 10

var (
	ErrPasswordBreached = fmt.Errorf("ops: password appears in a known breach list")
	ErrPasswordTooShort = fmt.Errorf("ops: password must be at least %d characters", MinPasswordLength)
)

// validatePasswordLength is split out as a pure function so the policy
// boundary is unit-testable without a database.
func validatePasswordLength(password string) error {
	if len(password) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	return nil
}
