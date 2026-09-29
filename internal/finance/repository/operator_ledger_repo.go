package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/finance/domain"
	"github.com/sl-bus/backend/internal/finance/domain/ports"
)

var _ ports.OperatorLedgerRepository = (*OperatorLedgerRepository)(nil)

type OperatorLedgerRepository struct {
	pool *pgxpool.Pool
}

func NewOperatorLedgerRepository(pool *pgxpool.Pool) *OperatorLedgerRepository {
	return &OperatorLedgerRepository{pool: pool}
}

func (r *OperatorLedgerRepository) GetBalance(ctx context.Context, operatorID string) (*domain.OperatorLedgerBalance, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT operator_id, payable_balance, updated_at
		FROM finance.operator_ledger_balances
		WHERE operator_id = $1
	`, operatorID)

	var balance domain.OperatorLedgerBalance
	err := row.Scan(&balance.OperatorID, &balance.PayableBalance, &balance.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("scan balance: %w", err)
	}

	return &balance, nil
}

func (r *OperatorLedgerRepository) AddPayable(ctx context.Context, operatorID string, amount float64) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO finance.operator_ledger_balances (operator_id, payable_balance, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (operator_id)
		DO UPDATE SET payable_balance = operator_ledger_balances.payable_balance + $2, updated_at = now()
	`, operatorID, amount)
	if err != nil {
		return fmt.Errorf("add payable: %w", err)
	}

	return nil
}

func (r *OperatorLedgerRepository) SubtractPayable(ctx context.Context, operatorID string, amount float64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE finance.operator_ledger_balances
		SET payable_balance = payable_balance - $1, updated_at = now()
		WHERE operator_id = $2
	`, amount, operatorID)
	if err != nil {
		return fmt.Errorf("subtract payable: %w", err)
	}

	return nil
}
