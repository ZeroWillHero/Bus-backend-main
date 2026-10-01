package service_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/finance/domain"
	"github.com/sl-bus/backend/internal/finance/repository"
	"github.com/sl-bus/backend/internal/finance/service"
	"github.com/sl-bus/backend/internal/platform/testutil"
)

type env struct {
	ctx   context.Context
	pool  *pgxpool.Pool
	svc   *service.JournalService
	jrepo *repository.JournalRepository
	opID  string
	arID  string
	revID string
	payID string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testutil.NewTestPool(t)
	jrepo := repository.NewJournalRepository(pool)
	acct := repository.NewAccountRepository(pool)
	svc := service.NewJournalService(
		jrepo, acct,
		repository.NewOperatorLedgerRepository(pool),
		repository.NewPeriodRepository(pool),
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	e := &env{ctx: context.Background(), pool: pool, svc: svc, jrepo: jrepo, opID: testutil.SeedOperator(t, pool)}
	for code, dst := range map[string]*string{
		"1200-AR-BOOKINGS": &e.arID, "5100-REV-BOOKINGS": &e.revID, "2300-OP-PAYABLE": &e.payID,
	} {
		a, err := acct.FindByCode(e.ctx, code)
		if err != nil {
			t.Fatalf("chart of accounts is missing %s (migration 000009): %v", code, err)
		}
		*dst = a.ID
	}
	return e
}

func (e *env) newBookingID(t *testing.T) string {
	t.Helper()
	var id string
	if err := e.pool.QueryRow(e.ctx, `SELECT gen_random_uuid()`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) confirmPayload(amount float64) map[string]interface{} {
	return map[string]interface{}{"operator_id": e.opID, "amount": amount}
}

func (e *env) cancelPayload(amount float64, wasConfirmed bool) map[string]interface{} {
	return map[string]interface{}{"operator_id": e.opID, "amount": amount, "was_confirmed": wasConfirmed}
}

func (e *env) payable(t *testing.T) string {
	t.Helper()
	var bal string
	err := e.pool.QueryRow(e.ctx,
		`SELECT COALESCE((SELECT payable_balance::text FROM finance.operator_ledger_balances WHERE operator_id=$1), '0')`,
		e.opID).Scan(&bal)
	if err != nil {
		t.Fatal(err)
	}
	return bal
}

func (e *env) count(t *testing.T, q string, args ...interface{}) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestLedgerEntriesAreImmutable proves the ledger is INSERT-only for every role,
// not just the app role: history can only be corrected with a reversing entry.
func TestLedgerEntriesAreImmutable(t *testing.T) {
	e := newEnv(t)
	booking := e.newBookingID(t)
	if err := e.svc.ProcessBookingConfirmed(e.ctx, booking, e.confirmPayload(1500)); err != nil {
		t.Fatalf("post: %v", err)
	}

	stmts := []string{
		`UPDATE finance.journal_lines SET debit = debit + 1 WHERE journal_entry_id IN (SELECT id FROM finance.journal_entries WHERE source_id = $1)`,
		`DELETE FROM finance.journal_lines WHERE journal_entry_id IN (SELECT id FROM finance.journal_entries WHERE source_id = $1)`,
		`UPDATE finance.journal_entries SET description = 'tampered' WHERE source_id = $1`,
		`DELETE FROM finance.journal_entries WHERE source_id = $1`,
	}

	// 1) superuser (the migration role): the append-only trigger must still stop it.
	for _, q := range stmts {
		if _, err := e.pool.Exec(e.ctx, q, booking); err == nil {
			t.Errorf("superuser mutation succeeded, ledger is not immutable: %s", q)
		} else if !strings.Contains(err.Error(), "append-only") {
			t.Errorf("expected append-only error, got: %v (%s)", err, q)
		}
	}
	for _, tbl := range []string{"finance.journal_entries", "finance.journal_lines"} {
		if _, err := e.pool.Exec(e.ctx, "TRUNCATE "+tbl+" CASCADE"); err == nil {
			t.Errorf("TRUNCATE %s succeeded", tbl)
		}
	}

	// 2) the real app role: permission denied (REVOKE) or the trigger, either way an error.
	conn, err := e.pool.Acquire(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(e.ctx, `SET ROLE slbus_app`); err != nil {
		t.Fatalf("set role: %v", err)
	}
	defer conn.Exec(e.ctx, `RESET ROLE`)
	for _, q := range stmts {
		if _, err := conn.Exec(e.ctx, q, booking); err == nil {
			t.Errorf("slbus_app mutation succeeded: %s", q)
		}
	}

	// Data is untouched.
	if n := e.count(t, `SELECT count(*) FROM finance.journal_entries WHERE source_id=$1 AND description <> 'tampered'`, booking); n != 1 {
		t.Errorf("expected the original entry to survive, found %d", n)
	}
}

// TestTrialBalanceAlwaysBalances: after a mix of confirms and cancels, total
// debits equal total credits globally AND for every individual entry, and the
// denormalized operator balance equals what the ledger lines say.
func TestTrialBalanceAlwaysBalances(t *testing.T) {
	e := newEnv(t)
	amounts := []float64{1500, 2500.50, 99.99, 0.10, 0.20, 33.33, 1234.56, 750}
	var cancelled int
	for i := 0; i < 24; i++ {
		b := e.newBookingID(t)
		amt := amounts[i%len(amounts)]
		if err := e.svc.ProcessBookingConfirmed(e.ctx, b, e.confirmPayload(amt)); err != nil {
			t.Fatalf("confirm %d: %v", i, err)
		}
		if i%3 == 0 {
			if err := e.svc.ProcessBookingCancelled(e.ctx, b, e.cancelPayload(amt, true)); err != nil {
				t.Fatalf("cancel %d: %v", i, err)
			}
			cancelled++
		}
	}

	var debit, credit string
	if err := e.pool.QueryRow(e.ctx, `SELECT COALESCE(SUM(debit),0)::text, COALESCE(SUM(credit),0)::text FROM finance.journal_lines`).Scan(&debit, &credit); err != nil {
		t.Fatal(err)
	}
	if debit != credit {
		t.Fatalf("trial balance is off: debits=%s credits=%s", debit, credit)
	}
	if n := e.count(t, `
		SELECT count(*) FROM (
		  SELECT journal_entry_id FROM finance.journal_lines
		  GROUP BY journal_entry_id HAVING SUM(debit) <> SUM(credit)) x`); n != 0 {
		t.Errorf("%d individual entries are unbalanced", n)
	}

	// Operator sub-ledger: payable account credits - debits for this operator == running balance.
	var fromLines string
	if err := e.pool.QueryRow(e.ctx, `
		SELECT COALESCE(SUM(credit) - SUM(debit), 0)::text FROM finance.journal_lines
		WHERE operator_id = $1 AND account_id = $2`, e.opID, e.payID).Scan(&fromLines); err != nil {
		t.Fatal(err)
	}
	if got := e.payable(t); got != fromLines {
		t.Errorf("operator_ledger_balances=%s but ledger lines say %s", got, fromLines)
	}
	if cancelled == 0 {
		t.Fatal("test did not exercise reversals")
	}
}

func TestUnbalancedEntryIsRejected(t *testing.T) {
	e := newEnv(t)
	booking := e.newBookingID(t)
	period, err := repository.NewPeriodRepository(e.pool).GetOrCreateCurrentPeriod(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = e.jrepo.PostEntry(e.ctx,
		&domain.JournalEntry{PeriodID: period.ID, SourceType: domain.EntrySourceAdjustment, SourceID: booking, Description: "bad"},
		[]*domain.JournalLine{
			{AccountID: e.arID, Debit: 100},
			{AccountID: e.revID, Credit: 90},
		})
	if err == nil {
		t.Fatal("unbalanced entry was accepted")
	}
	if n := e.count(t, `SELECT count(*) FROM finance.journal_entries WHERE source_id=$1`, booking); n != 0 {
		t.Errorf("rejected entry left %d rows behind (not atomic)", n)
	}
}

// TestBookingConfirmedIsIdempotent: the outbox is at-least-once, so the same
// event arriving many times, even concurrently, must post exactly once.
func TestBookingConfirmedIsIdempotent(t *testing.T) {
	e := newEnv(t)
	booking := e.newBookingID(t)

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- e.svc.ProcessBookingConfirmed(e.ctx, booking, e.confirmPayload(1000))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("redelivery must succeed quietly, got: %v", err)
		}
	}

	if n := e.count(t, `SELECT count(*) FROM finance.journal_entries WHERE source_type='booking' AND source_id=$1`, booking); n != 1 {
		t.Errorf("expected exactly 1 entry, got %d (double posting)", n)
	}
	if got := e.payable(t); got != "1000.00" {
		t.Errorf("operator payable = %s, want 1000.00 (balance double-counted?)", got)
	}
}

func TestCancelReversesExactlyOnce(t *testing.T) {
	e := newEnv(t)
	booking := e.newBookingID(t)
	if err := e.svc.ProcessBookingConfirmed(e.ctx, booking, e.confirmPayload(800)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ { // redelivered cancel
		if err := e.svc.ProcessBookingCancelled(e.ctx, booking, e.cancelPayload(800, true)); err != nil {
			t.Fatalf("cancel #%d: %v", i, err)
		}
	}
	if got := e.payable(t); got != "0.00" {
		t.Errorf("payable after confirm+cancel = %s, want 0.00", got)
	}
	if n := e.count(t, `
		SELECT count(*) FROM finance.journal_entries r
		JOIN finance.journal_entries o ON o.id = r.reversed_entry_id
		WHERE o.source_id = $1 AND r.source_type = 'refund'`, booking); n != 1 {
		t.Errorf("expected exactly 1 reversing entry linked to the original, got %d", n)
	}
}

func TestCancelOfUnconfirmedBookingPostsNothing(t *testing.T) {
	e := newEnv(t)
	booking := e.newBookingID(t)
	if err := e.svc.ProcessBookingCancelled(e.ctx, booking, e.cancelPayload(500, false)); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT count(*) FROM finance.journal_entries WHERE source_id=$1`, booking); n != 0 {
		t.Errorf("unconfirmed cancel posted %d entries", n)
	}
}

func TestCancelBeforeConfirmIsRetryableNotLost(t *testing.T) {
	e := newEnv(t)
	booking := e.newBookingID(t)
	err := e.svc.ProcessBookingCancelled(e.ctx, booking, e.cancelPayload(500, true))
	if err == nil {
		t.Fatal("cancel with no original entry must error so the outbox retries it later")
	}
}

// TestMalformedPayloadReturnsErrorNotPanic: a poison event must not crash the worker.
func TestMalformedPayloadReturnsErrorNotPanic(t *testing.T) {
	e := newEnv(t)
	bad := []map[string]interface{}{
		{},
		{"operator_id": e.opID},
		{"amount": 10.0},
		{"operator_id": e.opID, "amount": "ten"},
		{"operator_id": e.opID, "amount": -5.0},
		{"operator_id": 42, "amount": 5.0},
	}
	for i, p := range bad {
		err := e.svc.ProcessBookingConfirmed(e.ctx, e.newBookingID(t), p)
		if !errors.Is(err, service.ErrBadPayload) {
			t.Errorf("payload %d: want ErrBadPayload, got %v", i, err)
		}
	}
}

// TestLedgerRoundingNoDrift: float amounts like 0.1/0.2/33.33 must land on exact
// cents; after hundreds of postings the running balance equals the ledger lines.
func TestLedgerRoundingNoDrift(t *testing.T) {
	e := newEnv(t)
	cents := []float64{0.10, 0.20, 33.33, 19.99, 0.01, 1234.57}
	var wantCents int64
	for i := 0; i < 300; i++ {
		amt := cents[i%len(cents)]
		if err := e.svc.ProcessBookingConfirmed(e.ctx, e.newBookingID(t), e.confirmPayload(amt)); err != nil {
			t.Fatalf("post %d: %v", i, err)
		}
		wantCents += int64(amt*100 + 0.5)
	}
	var gotCents int64
	if err := e.pool.QueryRow(e.ctx, `SELECT (payable_balance*100)::bigint FROM finance.operator_ledger_balances WHERE operator_id=$1`, e.opID).Scan(&gotCents); err != nil {
		t.Fatal(err)
	}
	if gotCents != wantCents {
		t.Errorf("balance drifted: got %d cents, want %d (diff %d)", gotCents, wantCents, gotCents-wantCents)
	}
	var fromLines int64
	if err := e.pool.QueryRow(e.ctx, `SELECT (SUM(credit)*100)::bigint FROM finance.journal_lines WHERE operator_id=$1 AND account_id=$2`, e.opID, e.payID).Scan(&fromLines); err != nil {
		t.Fatal(err)
	}
	if fromLines != gotCents {
		t.Errorf("ledger lines (%d) disagree with running balance (%d)", fromLines, gotCents)
	}
	_ = fmt.Sprint
}
