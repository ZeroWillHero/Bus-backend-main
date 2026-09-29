package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/finance/domain"
	"github.com/sl-bus/backend/internal/finance/domain/ports"
)

var _ ports.PeriodRepository = (*PeriodRepository)(nil)

type PeriodRepository struct {
	pool *pgxpool.Pool
}

func NewPeriodRepository(pool *pgxpool.Pool) *PeriodRepository {
	return &PeriodRepository{pool: pool}
}

// GetOrCreateCurrentPeriod returns the current open period (month-based for Phase 4).
// Creates one if none exists (first of month to last of month).
func (r *PeriodRepository) GetOrCreateCurrentPeriod(ctx context.Context) (*domain.AccountingPeriod, error) {
	now := time.Now()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	periodEnd := periodStart.AddDate(0, 1, -1)

	// Try to find existing period for this month
	var period domain.AccountingPeriod
	err := r.pool.QueryRow(ctx, `
		SELECT id, period_start, period_end, status, created_at, closed_at
		FROM finance.accounting_periods
		WHERE period_start = $1 AND period_end = $2
		LIMIT 1
	`, periodStart, periodEnd).Scan(
		&period.ID, &period.PeriodStart, &period.PeriodEnd, &period.Status, &period.CreatedAt, &period.ClosedAt,
	)

	if err == nil {
		return &period, nil
	}

	// Create new period
	err = r.pool.QueryRow(ctx, `
		INSERT INTO finance.accounting_periods (period_start, period_end, status, created_at)
		VALUES ($1, $2, $3, now())
		RETURNING id, created_at
	`, periodStart, periodEnd, domain.PeriodOpen).Scan(&period.ID, &period.CreatedAt)

	if err != nil {
		return nil, fmt.Errorf("create period: %w", err)
	}

	period.PeriodStart = periodStart
	period.PeriodEnd = periodEnd
	period.Status = domain.PeriodOpen

	return &period, nil
}

func (r *PeriodRepository) FindByDate(ctx context.Context, dateStr string) (*domain.AccountingPeriod, error) {
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return nil, fmt.Errorf("parse date: %w", err)
	}

	row := r.pool.QueryRow(ctx, `
		SELECT id, period_start, period_end, status, created_at, closed_at
		FROM finance.accounting_periods
		WHERE period_start <= $1 AND period_end >= $1
		LIMIT 1
	`, date)

	var period domain.AccountingPeriod
	err = row.Scan(
		&period.ID, &period.PeriodStart, &period.PeriodEnd, &period.Status, &period.CreatedAt, &period.ClosedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan period: %w", err)
	}

	return &period, nil
}

func (r *PeriodRepository) ListOpen(ctx context.Context) ([]*domain.AccountingPeriod, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, period_start, period_end, status, created_at, closed_at
		FROM finance.accounting_periods
		WHERE status = $1
		ORDER BY period_start DESC
	`, domain.PeriodOpen)
	if err != nil {
		return nil, fmt.Errorf("query periods: %w", err)
	}
	defer rows.Close()

	var periods []*domain.AccountingPeriod
	for rows.Next() {
		var period domain.AccountingPeriod
		err := rows.Scan(
			&period.ID, &period.PeriodStart, &period.PeriodEnd, &period.Status, &period.CreatedAt, &period.ClosedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan period: %w", err)
		}
		periods = append(periods, &period)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return periods, nil
}

func (r *PeriodRepository) ClosePeriod(ctx context.Context, periodID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE finance.accounting_periods
		SET status = $1, closed_at = now()
		WHERE id = $2
	`, domain.PeriodClosed, periodID)
	if err != nil {
		return fmt.Errorf("close period: %w", err)
	}

	return nil
}
