package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
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

// ErrBadPayload marks an outbox payload that is missing or has a wrongly typed
// field. It is returned (never panicked) so a poison event cannot crash the worker.
var ErrBadPayload = errors.New("malformed outbox payload")

func payloadString(p map[string]interface{}, key string) (string, error) {
	v, ok := p[key].(string)
	if !ok || v == "" {
		return "", fmt.Errorf("%w: %q must be a non-empty string", ErrBadPayload, key)
	}
	return v, nil
}

func payloadAmount(p map[string]interface{}, key string) (float64, error) {
	v, ok := p[key].(float64)
	if !ok || v <= 0 {
		return 0, fmt.Errorf("%w: %q must be a positive number", ErrBadPayload, key)
	}
	return roundMoney(v), nil
}

// roundMoney snaps to whole cents so float64 noise (0.1+0.2) never reaches NUMERIC(12,2).
func roundMoney(v float64) float64 { return math.Round(v*100) / 100 }

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

type accounts struct{ ar, revenue, operatorPayable *domain.Account }

func (s *JournalService) loadAccounts(ctx context.Context) (*accounts, error) {
	ar, err := s.accountRepo.FindByCode(ctx, "1200-AR-BOOKINGS")
	if err != nil {
		return nil, fmt.Errorf("find AR account: %w", err)
	}
	rev, err := s.accountRepo.FindByCode(ctx, "5100-REV-BOOKINGS")
	if err != nil {
		return nil, fmt.Errorf("find revenue account: %w", err)
	}
	pay, err := s.accountRepo.FindByCode(ctx, "2300-OP-PAYABLE")
	if err != nil {
		return nil, fmt.Errorf("find operator payable account: %w", err)
	}
	return &accounts{ar: ar, revenue: rev, operatorPayable: pay}, nil
}

// ProcessBookingConfirmed posts a double-entry for a confirmed booking and
// credits the operator's payable balance, atomically.
// Idempotent: a redelivered event hits uq_journal_entries_source, is treated as
// already done, and changes nothing (the outbox is at-least-once).
func (s *JournalService) ProcessBookingConfirmed(ctx context.Context, bookingID string, payload map[string]interface{}) error {
	operatorID, err := payloadString(payload, "operator_id")
	if err != nil {
		return err
	}
	amount, err := payloadAmount(payload, "amount")
	if err != nil {
		return err
	}

	period, err := s.periodRepo.GetOrCreateCurrentPeriod(ctx)
	if err != nil {
		return fmt.Errorf("get current period: %w", err)
	}
	acc, err := s.loadAccounts(ctx)
	if err != nil {
		return err
	}

	entry := &domain.JournalEntry{
		PeriodID:    period.ID,
		EntryDate:   time.Now(),
		SourceType:  domain.EntrySourceBooking,
		SourceID:    bookingID,
		Description: fmt.Sprintf("Booking %s confirmed: passenger charged, operator payable", shortID(bookingID)),
	}
	lines := []*domain.JournalLine{
		{AccountID: acc.ar.ID, Debit: amount},
		{AccountID: acc.revenue.ID, Credit: amount},
		{AccountID: acc.ar.ID, Debit: amount},
		{AccountID: acc.operatorPayable.ID, OperatorID: &operatorID, Credit: amount},
	}

	if err := s.journalRepo.PostEntryAdjustingPayable(ctx, entry, lines, operatorID, amount); err != nil {
		if errors.Is(err, domain.ErrEntryAlreadyPosted) {
			s.log.InfoContext(ctx, "booking already posted; skipping redelivered event", "booking_id", bookingID)
			return nil
		}
		return fmt.Errorf("post journal entry: %w", err)
	}

	s.log.InfoContext(ctx, "booking confirmed posted to ledger",
		"booking_id", bookingID, "operator_id", operatorID, "amount", amount)
	return nil
}

// ProcessBookingCancelled posts a reversing entry for a booking that had been
// confirmed (and so had a ledger entry). The reversal is recorded as a 'refund'
// sourced from the booking id and points at the original via reversed_entry_id.
func (s *JournalService) ProcessBookingCancelled(ctx context.Context, bookingID string, payload map[string]interface{}) error {
	wasConfirmed, _ := payload["was_confirmed"].(bool)
	if !wasConfirmed {
		s.log.DebugContext(ctx, "booking cancelled (never confirmed); no ledger reversal", "booking_id", bookingID)
		return nil
	}
	operatorID, err := payloadString(payload, "operator_id")
	if err != nil {
		return err
	}
	amount, err := payloadAmount(payload, "amount")
	if err != nil {
		return err
	}

	// The original entry must exist; if the confirm event hasn't been relayed yet
	// this errors, the event stays unprocessed, and it is retried after its lease.
	original, err := s.journalRepo.FindBySource(ctx, domain.EntrySourceBooking, bookingID)
	if err != nil {
		return fmt.Errorf("find original entry for reversal: %w", err)
	}
	period, err := s.periodRepo.GetOrCreateCurrentPeriod(ctx)
	if err != nil {
		return fmt.Errorf("get current period: %w", err)
	}
	acc, err := s.loadAccounts(ctx)
	if err != nil {
		return err
	}

	reversal := &domain.JournalEntry{
		PeriodID:        period.ID,
		EntryDate:       time.Now(),
		SourceType:      domain.EntrySourceRefund,
		SourceID:        bookingID,
		Description:     fmt.Sprintf("Booking %s cancelled: reversing entry", shortID(bookingID)),
		ReversedEntryID: &original.ID,
	}
	lines := []*domain.JournalLine{
		{AccountID: acc.ar.ID, Credit: amount},
		{AccountID: acc.revenue.ID, Debit: amount},
		{AccountID: acc.ar.ID, Credit: amount},
		{AccountID: acc.operatorPayable.ID, OperatorID: &operatorID, Debit: amount},
	}

	if err := s.journalRepo.PostEntryAdjustingPayable(ctx, reversal, lines, operatorID, -amount); err != nil {
		if errors.Is(err, domain.ErrEntryAlreadyPosted) {
			s.log.InfoContext(ctx, "reversal already posted; skipping redelivered event", "booking_id", bookingID)
			return nil
		}
		return fmt.Errorf("post reversing entry: %w", err)
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
