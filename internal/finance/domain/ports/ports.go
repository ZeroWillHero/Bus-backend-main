package ports

import (
	"context"

	"github.com/sl-bus/backend/internal/finance/domain"
)

// JournalRepository handles journal entry persistence (immutable append-only).
type JournalRepository interface {
	PostEntry(ctx context.Context, entry *domain.JournalEntry, lines []*domain.JournalLine) error
	FindBySource(ctx context.Context, sourceType domain.EntrySource, sourceID string) (*domain.JournalEntry, error)
	ListByPeriod(ctx context.Context, periodID string, limit int, offset int) ([]*domain.JournalEntry, error)
}

// AccountRepository handles Chart of Accounts queries.
type AccountRepository interface {
	FindByCode(ctx context.Context, code string) (*domain.Account, error)
	FindByType(ctx context.Context, accType domain.AccountType) ([]*domain.Account, error)
	ListAll(ctx context.Context) ([]*domain.Account, error)
	Create(ctx context.Context, account *domain.Account) (*domain.Account, error)
}

// OperatorLedgerRepository handles the denormalized operator payable balance.
type OperatorLedgerRepository interface {
	GetBalance(ctx context.Context, operatorID string) (*domain.OperatorLedgerBalance, error)
	AddPayable(ctx context.Context, operatorID string, amount float64) error
	SubtractPayable(ctx context.Context, operatorID string, amount float64) error
}

// PeriodRepository handles accounting period lifecycle (open/close/lock).
type PeriodRepository interface {
	GetOrCreateCurrentPeriod(ctx context.Context) (*domain.AccountingPeriod, error)
	FindByDate(ctx context.Context, date string) (*domain.AccountingPeriod, error)
	ListOpen(ctx context.Context) ([]*domain.AccountingPeriod, error)
	ClosePeriod(ctx context.Context, periodID string) error
}

// OutboxRepository polls unprocessed booking events (cross-schema interface).
type OutboxRepository interface {
	PollUnprocessed(ctx context.Context, limit int) ([]map[string]interface{}, error)
	MarkProcessed(ctx context.Context, eventID string) error
}
