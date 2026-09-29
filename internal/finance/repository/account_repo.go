package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/finance/domain"
	"github.com/sl-bus/backend/internal/finance/domain/ports"
)

var _ ports.AccountRepository = (*AccountRepository)(nil)

type AccountRepository struct {
	pool *pgxpool.Pool
}

func NewAccountRepository(pool *pgxpool.Pool) *AccountRepository {
	return &AccountRepository{pool: pool}
}

func (r *AccountRepository) FindByCode(ctx context.Context, code string) (*domain.Account, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, code, name, account_type, parent_account_id, is_control, created_at
		FROM finance.accounts
		WHERE code = $1
	`, code)

	var account domain.Account
	err := row.Scan(
		&account.ID, &account.Code, &account.Name, &account.Type, &account.ParentID, &account.IsControl, &account.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan account: %w", err)
	}

	return &account, nil
}

func (r *AccountRepository) FindByType(ctx context.Context, accType domain.AccountType) ([]*domain.Account, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, code, name, account_type, parent_account_id, is_control, created_at
		FROM finance.accounts
		WHERE account_type = $1
		ORDER BY code
	`, accType)
	if err != nil {
		return nil, fmt.Errorf("query accounts: %w", err)
	}
	defer rows.Close()

	var accounts []*domain.Account
	for rows.Next() {
		var account domain.Account
		err := rows.Scan(
			&account.ID, &account.Code, &account.Name, &account.Type, &account.ParentID, &account.IsControl, &account.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		accounts = append(accounts, &account)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return accounts, nil
}

func (r *AccountRepository) ListAll(ctx context.Context) ([]*domain.Account, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, code, name, account_type, parent_account_id, is_control, created_at
		FROM finance.accounts
		ORDER BY code
	`)
	if err != nil {
		return nil, fmt.Errorf("query all accounts: %w", err)
	}
	defer rows.Close()

	var accounts []*domain.Account
	for rows.Next() {
		var account domain.Account
		err := rows.Scan(
			&account.ID, &account.Code, &account.Name, &account.Type, &account.ParentID, &account.IsControl, &account.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		accounts = append(accounts, &account)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return accounts, nil
}

func (r *AccountRepository) Create(ctx context.Context, account *domain.Account) (*domain.Account, error) {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO finance.accounts (code, name, account_type, parent_account_id, is_control)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at
	`, account.Code, account.Name, account.Type, account.ParentID, account.IsControl).
		Scan(&account.ID, &account.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert account: %w", err)
	}

	return account, nil
}
