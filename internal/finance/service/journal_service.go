package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/sl-bus/backend/internal/finance/domain"
	"github.com/sl-bus/backend/internal/finance/domain/ports"
)

// JournalService implements the outbox-driven journal entry engine.
// Subscribes to BookingConfirmed/BookingCancelled events and posts ledger entries
// per the double-entry accounting model (DOMAIN_MODEL.md §4).
type JournalService struct {
	journalRepo        ports.JournalRepository
	accountRepo        ports.AccountRepository
	operatorLedgerRepo ports.OperatorLedgerRepository
	periodRepo         ports.PeriodRepository
	outboxRepo         ports.OutboxRepository
	log                *slog.Logger
}

func NewJournalService(
	journalRepo ports.JournalRepository,
	accountRepo ports.AccountRepository,
	operatorLedgerRepo ports.OperatorLedgerRepository,
	periodRepo ports.PeriodRepository,
	outboxRepo ports.OutboxRepository,
	log *slog.Logger,
) *JournalService {
	return &JournalService{
		journalRepo:        journalRepo,
		accountRepo:        accountRepo,
		operatorLedgerRepo: operatorLedgerRepo,
		periodRepo:         periodRepo,
		outboxRepo:         outboxRepo,
		log:                log,
	}
}

// ProcessBookingConfirmed posts a double-entry for a confirmed booking.
// Creates: Debit Accounts Receivable | Credit Revenue (passenger responsibility to pay)
// and:     Debit Accounts Receivable | Credit Operator Payable (operator settlement)
// Idempotent: calling twice with same booking_id is safe (checked via source_id uniqueness).
func (s *JournalService) ProcessBookingConfirmed(ctx context.Context, bookingID string, payload map[string]interface{}) error {
	operatorID := payload["operator_id"].(string)
	amount := payload["amount"].(float64)
	// currency := payload["currency"].(string) // Use LKR for now (single-currency phase)

	// Find or create the current open accounting period
	period, err := s.periodRepo.GetOrCreateCurrentPeriod(ctx)
	if err != nil {
		return fmt.Errorf("get current period: %w", err)
	}

	// Find Chart of Accounts entries
	arAccount, err := s.accountRepo.FindByCode(ctx, "1200-AR-BOOKINGS") // Accounts Receivable
	if err != nil {
		return fmt.Errorf("find AR account: %w", err)
	}

	revenueAccount, err := s.accountRepo.FindByCode(ctx, "5100-REV-BOOKINGS") // Revenue
	if err != nil {
		return fmt.Errorf("find revenue account: %w", err)
	}

	operatorPayableAccount, err := s.accountRepo.FindByCode(ctx, "2300-OP-PAYABLE") // Operator Payable
	if err != nil {
		return fmt.Errorf("find operator payable account: %w", err)
	}

	// Create the journal entry (immutable once posted)
	entry := &domain.JournalEntry{
		PeriodID:    period.ID,
		EntryDate:   time.Now(),
		SourceType:  domain.EntrySourceBooking,
		SourceID:    bookingID,
		Description: fmt.Sprintf("Booking %s confirmed: passenger charged, operator payable", bookingID[:8]),
		CreatedBy:   "system", // System-generated (outbox relay)
	}

	// Post entry with two balanced lines:
	// Line 1: Debit AR, Credit Revenue
	// Line 2: Debit AR, Credit Operator Payable (operator's cut of the fare)
	lines := []*domain.JournalLine{
		{
			AccountID:  arAccount.ID,
			OperatorID: nil, // Passenger responsibility, no operator breakdown
			Debit:      amount,
			Credit:     0,
		},
		{
			AccountID:  revenueAccount.ID,
			OperatorID: nil,
			Debit:      0,
			Credit:     amount, // Platform's take (simplification: for now, 100% goes to operator)
		},
		{
			AccountID:  arAccount.ID,
			OperatorID: nil,
			Debit:      amount, // Second pass: operator's payable
			Credit:     0,
		},
		{
			AccountID:  operatorPayableAccount.ID,
			OperatorID: &operatorID, // Sub-ledger breakdown by operator
			Debit:      0,
			Credit:     amount,
		},
	}

	if err := s.journalRepo.PostEntry(ctx, entry, lines); err != nil {
		return fmt.Errorf("post journal entry: %w", err)
	}

	// Update denormalized operator ledger balance
	if err := s.operatorLedgerRepo.AddPayable(ctx, operatorID, amount); err != nil {
		return fmt.Errorf("update operator ledger: %w", err)
	}

	s.log.InfoContext(ctx, "booking confirmed posted to ledger",
		"booking_id", bookingID, "operator_id", operatorID, "amount", amount)

	return nil
}

// ProcessBookingCancelled reverses the entry created by ProcessBookingConfirmed.
// Creates a reversing entry (references the original via reversed_entry_id).
func (s *JournalService) ProcessBookingCancelled(ctx context.Context, bookingID string, payload map[string]interface{}) error {
	operatorID := payload["operator_id"].(string)
	amount := payload["amount"].(float64)
	wasConfirmed := payload["was_confirmed"].(bool)

	if !wasConfirmed {
		// Booking was held but never confirmed; no ledger entry to reverse
		s.log.DebugContext(ctx, "booking cancelled (never confirmed); no ledger reversal",
			"booking_id", bookingID)
		return nil
	}

	// Find the original entry
	originalEntry, err := s.journalRepo.FindBySource(ctx, domain.EntrySourceBooking, bookingID)
	if err != nil {
		return fmt.Errorf("find original entry for reversal: %w", err)
	}

	// Create reversing entry (Booking Cancelled events reverse the Booking Confirmed entries)
	period, err := s.periodRepo.GetOrCreateCurrentPeriod(ctx)
	if err != nil {
		return fmt.Errorf("get current period: %w", err)
	}

	// Get accounts
	arAccount, err := s.accountRepo.FindByCode(ctx, "1200-AR-BOOKINGS")
	if err != nil {
		return fmt.Errorf("find AR account: %w", err)
	}
	revenueAccount, err := s.accountRepo.FindByCode(ctx, "5100-REV-BOOKINGS")
	if err != nil {
		return fmt.Errorf("find revenue account: %w", err)
	}
	operatorPayableAccount, err := s.accountRepo.FindByCode(ctx, "2300-OP-PAYABLE")
	if err != nil {
		return fmt.Errorf("find operator payable account: %w", err)
	}

	reversingEntry := &domain.JournalEntry{
		PeriodID:        period.ID,
		EntryDate:       time.Now(),
		SourceType:      domain.EntrySourceBooking,
		SourceID:        bookingID + "-cancel",
		Description:     fmt.Sprintf("Booking %s cancelled: reversing entry", bookingID[:8]),
		CreatedBy:       "system",
		ReversedEntryID: &originalEntry.ID,
	}

	// Reversing lines (flip debit/credit from original)
	lines := []*domain.JournalLine{
		{
			AccountID:  arAccount.ID,
			OperatorID: nil,
			Debit:      0, // Reversed (was debit)
			Credit:     amount,
		},
		{
			AccountID:  revenueAccount.ID,
			OperatorID: nil,
			Debit:      amount, // Reversed (was credit)
			Credit:     0,
		},
		{
			AccountID:  arAccount.ID,
			OperatorID: nil,
			Debit:      0,
			Credit:     amount,
		},
		{
			AccountID:  operatorPayableAccount.ID,
			OperatorID: &operatorID,
			Debit:      amount, // Reversed (was credit)
			Credit:     0,
		},
	}

	if err := s.journalRepo.PostEntry(ctx, reversingEntry, lines); err != nil {
		return fmt.Errorf("post reversing entry: %w", err)
	}

	// Update operator ledger (subtract payable)
	if err := s.operatorLedgerRepo.SubtractPayable(ctx, operatorID, amount); err != nil {
		return fmt.Errorf("update operator ledger: %w", err)
	}

	s.log.InfoContext(ctx, "booking cancelled posted (reversing entry)",
		"booking_id", bookingID, "operator_id", operatorID, "amount", amount)

	return nil
}

// ProcessPaidButExpired handles the edge case where payment succeeded after hold expired.
// Per MITIGATION_PLAN.md §1.3: auto-refund immediately (no ledger debit).
func (s *JournalService) ProcessPaidButExpired(ctx context.Context, bookingID string, payload map[string]interface{}) error {
	// In Phase 4, this is a stub: ledger will reflect refund via a separate refund entry.
	// Phase 4 focus: revenue recognition for normal bookings; Phase 5+ handles refund automation.
	s.log.WarnContext(ctx, "paid_but_expired event received; refund handling deferred to Phase 5",
		"booking_id", bookingID)
	return nil
}
