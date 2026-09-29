// Package domain defines the Finance bounded context (Phase 4).
// Double-entry ledger with operator sub-ledger and payout settlement.
package domain

import "time"

type AccountType string

const (
	AccountAsset     AccountType = "asset"
	AccountLiability AccountType = "liability"
	AccountEquity    AccountType = "equity"
	AccountRevenue   AccountType = "revenue"
	AccountExpense   AccountType = "expense"
)

type EntrySource string

const (
	EntrySourceBooking    EntrySource = "booking"
	EntrySourceRefund     EntrySource = "refund"
	EntrySourcePayout     EntrySource = "payout"
	EntrySourceAdjustment EntrySource = "adjustment"
)

type PeriodStatus string

const (
	PeriodOpen   PeriodStatus = "open"
	PeriodClosed PeriodStatus = "closed"
	PeriodLocked PeriodStatus = "locked"
)

type PaymentStatus string

const (
	PaymentPending  PaymentStatus = "pending"
	PaymentReceived PaymentStatus = "received"
	PaymentFailed   PaymentStatus = "failed"
	PaymentRefunded PaymentStatus = "refunded"
)

type PayoutStatus string

const (
	PayoutPending    PayoutStatus = "pending"
	PayoutApproved   PayoutStatus = "approved"
	PayoutInitiated  PayoutStatus = "initiated"
	PayoutExecuted   PayoutStatus = "executed"
	PayoutFailed     PayoutStatus = "failed"
	PayoutReconciled PayoutStatus = "reconciled"
)

// Account represents a Chart of Accounts entry
type Account struct {
	ID        string
	Code      string
	Name      string
	Type      AccountType
	ParentID  *string
	IsControl bool
	CreatedAt time.Time
}

// AccountingPeriod represents a fiscal period for period-close reconciliation
type AccountingPeriod struct {
	ID          string
	PeriodStart time.Time
	PeriodEnd   time.Time
	Status      PeriodStatus
	CreatedAt   time.Time
	ClosedAt    *time.Time
}

// JournalEntry is a balanced set of debit/credit lines (immutable)
type JournalEntry struct {
	ID              string
	PeriodID        string
	EntryDate       time.Time
	SourceType      EntrySource
	SourceID        string
	Description     string
	CreatedBy       string
	ReversedEntryID *string
	PostedAt        time.Time
	CreatedAt       time.Time
}

// JournalLine represents a single debit or credit line (XOR: exactly one is non-zero)
type JournalLine struct {
	ID             string
	JournalEntryID string
	AccountID      string
	OperatorID     *string
	Debit          float64 // >= 0; credit == 0 if debit > 0
	Credit         float64 // >= 0; debit == 0 if credit > 0
	CreatedAt      time.Time
}

// OperatorLedgerBalance is the denormalized running payable for an operator
type OperatorLedgerBalance struct {
	OperatorID     string
	PayableBalance float64
	UpdatedAt      time.Time
}

// Payment represents an incoming payment (from DirectPay, etc.)
type Payment struct {
	ID           string
	BookingID    string
	Gateway      string
	GatewayTxnID string
	Amount       float64
	Currency     string
	Status       PaymentStatus
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Payout represents a settlement batch to an operator
type Payout struct {
	ID           string
	OperatorID   string
	PeriodStart  time.Time
	PeriodEnd    time.Time
	Amount       float64
	Status       PayoutStatus
	BankTxnRef   *string
	CreatedAt    time.Time
	InitiatedAt  *time.Time
	ExecutedAt   *time.Time
	ReconciledAt *time.Time
}
