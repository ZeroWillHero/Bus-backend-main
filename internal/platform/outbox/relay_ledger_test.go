package outbox_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"

	bookingrepo "github.com/sl-bus/backend/internal/booking/repository"
	financerepo "github.com/sl-bus/backend/internal/finance/repository"
	financesvc "github.com/sl-bus/backend/internal/finance/service"
	"github.com/sl-bus/backend/internal/platform/outbox"
	"github.com/sl-bus/backend/internal/platform/testutil"
)

func bookingHandler(fn func(context.Context, string, map[string]interface{}) error) outbox.Handler {
	return func(ctx context.Context, p map[string]interface{}) error {
		id, ok := p["booking_id"].(string)
		if !ok {
			return fmt.Errorf("no booking_id")
		}
		return fn(ctx, id, p)
	}
}

// TestRelayPostsLedgerExactlyOnceAcrossCompetingWorkers drives the real path:
// outbox_events -> relay (two workers racing) -> journal service -> ledger.
// Every confirmed booking must hit the ledger exactly once even though delivery
// is at-least-once and a poison event sits in the same stream.
func TestRelayPostsLedgerExactlyOnceAcrossCompetingWorkers(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewTestPool(t)
	if _, err := pool.Exec(ctx, `DELETE FROM platform.outbox_events`); err != nil {
		t.Fatal(err)
	}
	operatorID := testutil.SeedOperator(t, pool)
	outboxRepo := bookingrepo.NewOutboxRepository(pool)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc := financesvc.NewJournalService(
		financerepo.NewJournalRepository(pool), financerepo.NewAccountRepository(pool),
		financerepo.NewOperatorLedgerRepository(pool), financerepo.NewPeriodRepository(pool), nil, log)

	newWorker := func() *outbox.Relay {
		return &outbox.Relay{
			Source: outboxRepo, BatchSize: 5, Log: log,
			Handlers: map[string]outbox.Handler{
				"BookingConfirmed": bookingHandler(svc.ProcessBookingConfirmed),
				"BookingCancelled": bookingHandler(svc.ProcessBookingCancelled),
			},
		}
	}

	const bookings = 60 // 100.00 each; every 4th is later cancelled
	cancelled := 0
	for i := 0; i < bookings; i++ {
		var id string
		if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&id); err != nil {
			t.Fatal(err)
		}
		payload := map[string]interface{}{"booking_id": id, "operator_id": operatorID, "amount": 100.0}
		if err := outboxRepo.Write(ctx, nil, "BookingConfirmed", payload); err != nil {
			t.Fatal(err)
		}
		if i%4 == 0 {
			cancel := map[string]interface{}{"booking_id": id, "operator_id": operatorID, "amount": 100.0, "was_confirmed": true}
			if err := outboxRepo.Write(ctx, nil, "BookingCancelled", cancel); err != nil {
				t.Fatal(err)
			}
			cancelled++
		}
	}
	// Poison event: valid JSON, but no operator_id. Must not crash or block the rest.
	if err := outboxRepo.Write(ctx, nil, "BookingConfirmed", map[string]interface{}{"booking_id": "00000000-0000-0000-0000-00000000dead", "amount": 5.0}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker := newWorker()
			for i := 0; i < 40; i++ { // bounded: no infinite loop on the poison event
				if _, err := worker.RunOnce(ctx); err != nil {
					t.Errorf("relay: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	var entries, reversals int
	_ = pool.QueryRow(ctx, `
		SELECT count(DISTINCT e.id) FILTER (WHERE e.source_type='booking'),
		       count(DISTINCT e.id) FILTER (WHERE e.source_type='refund')
		FROM finance.journal_entries e JOIN finance.journal_lines l ON l.journal_entry_id = e.id
		WHERE l.operator_id = $1`, operatorID).Scan(&entries, &reversals)
	if entries != bookings || reversals != cancelled {
		t.Errorf("ledger has %d booking / %d refund entries, want %d / %d", entries, reversals, bookings, cancelled)
	}

	var payable string
	_ = pool.QueryRow(ctx, `SELECT payable_balance::text FROM finance.operator_ledger_balances WHERE operator_id=$1`, operatorID).Scan(&payable)
	if want := fmt.Sprintf("%.2f", float64(bookings-cancelled)*100); payable != want {
		t.Errorf("operator payable = %s, want %s", payable, want)
	}

	var pending int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM platform.outbox_events WHERE processed_at IS NULL`).Scan(&pending)
	if pending != 1 {
		t.Errorf("%d events still unprocessed, want exactly the 1 poison event", pending)
	}
}
