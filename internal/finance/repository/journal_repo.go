package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/finance/domain"
	"github.com/sl-bus/backend/internal/finance/domain/ports"
)

// Compile-time check
var _ ports.JournalRepository = (*JournalRepository)(nil)

type JournalRepository struct {
	pool *pgxpool.Pool
}

func NewJournalRepository(pool *pgxpool.Pool) *JournalRepository {
	return &JournalRepository{pool: pool}
}

// PostEntry inserts a journal entry with its lines in a single transaction (atomicity).
// The database constraint trigger verifies debits = credits before commit.
func (r *JournalRepository) PostEntry(
	ctx context.Context,
	entry *domain.JournalEntry,
	lines []*domain.JournalLine,
) error {
	return r.post(ctx, entry, lines, "", 0)
}

// PostEntryAdjustingPayable posts the entry and the operator payable delta atomically.
func (r *JournalRepository) PostEntryAdjustingPayable(
	ctx context.Context,
	entry *domain.JournalEntry,
	lines []*domain.JournalLine,
	operatorID string,
	delta float64,
) error {
	return r.post(ctx, entry, lines, operatorID, delta)
}

func (r *JournalRepository) post(
	ctx context.Context,
	entry *domain.JournalEntry,
	lines []*domain.JournalLine,
	operatorID string,
	delta float64,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// System-generated entries have no author: store NULL, not an invalid uuid.
	var createdBy *string
	if entry.CreatedBy != "" {
		createdBy = &entry.CreatedBy
	}

	var entryID string
	err = tx.QueryRow(ctx, `
		INSERT INTO finance.journal_entries (
			period_id, entry_date, source_type, source_id, description, created_by, reversed_entry_id, posted_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		RETURNING id
	`,
		entry.PeriodID, entry.EntryDate, entry.SourceType, entry.SourceID,
		entry.Description, createdBy, entry.ReversedEntryID,
	).Scan(&entryID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "uq_journal_entries_source" {
			return domain.ErrEntryAlreadyPosted
		}
		return fmt.Errorf("insert journal_entry: %w", err)
	}

	for _, line := range lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO finance.journal_lines (journal_entry_id, account_id, operator_id, debit, credit)
			VALUES ($1, $2, $3, $4, $5)
		`, entryID, line.AccountID, line.OperatorID, line.Debit, line.Credit); err != nil {
			return fmt.Errorf("insert journal_line: %w", err)
		}
	}

	if operatorID != "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO finance.operator_ledger_balances (operator_id, payable_balance, updated_at)
			VALUES ($1, $2, now())
			ON CONFLICT (operator_id)
			DO UPDATE SET payable_balance = operator_ledger_balances.payable_balance + $2, updated_at = now()
		`, operatorID, delta); err != nil {
			return fmt.Errorf("adjust operator payable: %w", err)
		}
	}

	// The deferred balance trigger fires here: unbalanced entries fail the commit.
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit journal entry: %w", err)
	}
	return nil
}

// FindBySource retrieves the entry created for a specific booking/refund/etc (idempotency check).
func (r *JournalRepository) FindBySource(
	ctx context.Context,
	sourceType domain.EntrySource,
	sourceID string,
) (*domain.JournalEntry, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, period_id, entry_date, source_type, source_id, description, created_by, reversed_entry_id, posted_at, created_at
		FROM finance.journal_entries
		WHERE source_type = $1 AND source_id = $2
		LIMIT 1
	`, sourceType, sourceID)

	var entry domain.JournalEntry
	var createdBy *string
	err := row.Scan(
		&entry.ID, &entry.PeriodID, &entry.EntryDate, &entry.SourceType, &entry.SourceID,
		&entry.Description, &createdBy, &entry.ReversedEntryID, &entry.PostedAt, &entry.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan journal_entry: %w", err)
	}
	if createdBy != nil {
		entry.CreatedBy = *createdBy
	}

	return &entry, nil
}

// ListByPeriod retrieves entries for a fiscal period (for reporting).
func (r *JournalRepository) ListByPeriod(
	ctx context.Context,
	periodID string,
	limit int,
	offset int,
) ([]*domain.JournalEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, period_id, entry_date, source_type, source_id, description, created_by, reversed_entry_id, posted_at, created_at
		FROM finance.journal_entries
		WHERE period_id = $1
		ORDER BY entry_date DESC, posted_at DESC
		LIMIT $2 OFFSET $3
	`, periodID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query entries: %w", err)
	}
	defer rows.Close()

	var entries []*domain.JournalEntry
	for rows.Next() {
		var entry domain.JournalEntry
		var createdBy *string
		err := rows.Scan(
			&entry.ID, &entry.PeriodID, &entry.EntryDate, &entry.SourceType, &entry.SourceID,
			&entry.Description, &createdBy, &entry.ReversedEntryID, &entry.PostedAt, &entry.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan entry: %w", err)
		}
		if createdBy != nil {
			entry.CreatedBy = *createdBy
		}
		entries = append(entries, &entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return entries, nil
}
