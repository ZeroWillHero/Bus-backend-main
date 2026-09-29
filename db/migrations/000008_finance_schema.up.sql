-- Phase 4 — Financial Ledger (DATABASE_DESIGN.md §4, MITIGATION_PLAN.md §2)
-- Immutable double-entry GL, operator sub-ledger, payout settlement tracking.

CREATE SCHEMA IF NOT EXISTS finance;

-- Chart of Accounts (hierarchical GL with standard asset|liability|equity|revenue|expense types)
CREATE TABLE finance.accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code TEXT NOT NULL UNIQUE, -- e.g. "1000", "5100-REV-BOOKINGS"
    name TEXT NOT NULL,
    account_type TEXT NOT NULL CHECK (account_type IN ('asset', 'liability', 'equity', 'revenue', 'expense')),
    parent_account_id UUID REFERENCES finance.accounts(id) ON DELETE RESTRICT,
    is_control BOOLEAN DEFAULT FALSE, -- e.g. "Operator Payable" shared by all operators
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_accounts_code ON finance.accounts(code);
CREATE INDEX idx_accounts_type ON finance.accounts(account_type);

-- Accounting periods (open/closed for reconciliation gate)
CREATE TABLE finance.accounting_periods (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed', 'locked')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at TIMESTAMPTZ,
    UNIQUE (period_start, period_end)
);

CREATE INDEX idx_periods_status ON finance.accounting_periods(status);

-- Journal Entries (immutable; corrections via new entry referencing reversed_entry_id)
CREATE TABLE finance.journal_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    period_id UUID NOT NULL REFERENCES finance.accounting_periods(id) ON DELETE RESTRICT,
    entry_date DATE NOT NULL,
    source_type TEXT NOT NULL CHECK (source_type IN ('booking', 'refund', 'payout', 'adjustment')),
    source_id UUID NOT NULL, -- e.g. booking.bookings.id for source_type='booking'
    description TEXT NOT NULL,
    created_by UUID NOT NULL, -- identity.users.id (who posted this entry)
    reversed_entry_id UUID REFERENCES finance.journal_entries(id) ON DELETE RESTRICT,
    posted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_journal_entries_period ON finance.journal_entries(period_id);
CREATE INDEX idx_journal_entries_source ON finance.journal_entries(source_type, source_id);
CREATE INDEX idx_journal_entries_date ON finance.journal_entries(entry_date);

-- Journal Lines (double-entry: debit OR credit, never both; operator_id for sub-ledger)
-- UNIQUE(journal_entry_id, account_id) is NOT enforced — an entry can post same account multiple times
CREATE TABLE finance.journal_lines (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    journal_entry_id UUID NOT NULL REFERENCES finance.journal_entries(id) ON DELETE RESTRICT,
    account_id UUID NOT NULL REFERENCES finance.accounts(id) ON DELETE RESTRICT,
    operator_id UUID, -- For sub-ledger breakdown (especially "Operator Payable" control account)
    debit NUMERIC(12,2) DEFAULT 0.00 CHECK (debit >= 0),
    credit NUMERIC(12,2) DEFAULT 0.00 CHECK (credit >= 0),
    CHECK ((debit = 0.00 AND credit > 0.00) OR (debit > 0.00 AND credit = 0.00)), -- XOR: debit or credit, not both
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_journal_lines_entry ON finance.journal_lines(journal_entry_id);
CREATE INDEX idx_journal_lines_account ON finance.journal_lines(account_id);
CREATE INDEX idx_journal_lines_operator ON finance.journal_lines(operator_id); -- For sub-ledger queries

-- Operator Ledger Balances (deliberately denormalized running total; updated atomically with each journal_lines insert)
CREATE TABLE finance.operator_ledger_balances (
    operator_id UUID PRIMARY KEY REFERENCES ops.operators(id) ON DELETE RESTRICT,
    payable_balance NUMERIC(12,2) NOT NULL DEFAULT 0.00,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Incoming payments (from DirectPay or other gateway; keyed to booking)
CREATE TABLE finance.payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id UUID NOT NULL REFERENCES booking.bookings(id) ON DELETE RESTRICT,
    gateway TEXT NOT NULL, -- e.g. "directpay", "stripe", "mock"
    gateway_txn_id TEXT NOT NULL UNIQUE,
    amount NUMERIC(12,2) NOT NULL,
    currency CHAR(3) NOT NULL DEFAULT 'LKR',
    status TEXT NOT NULL DEFAULT 'received' CHECK (status IN ('pending', 'received', 'failed', 'refunded')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_payments_booking ON finance.payments(booking_id);
CREATE INDEX idx_payments_gateway ON finance.payments(gateway, gateway_txn_id);
CREATE INDEX idx_payments_status ON finance.payments(status);

-- Operator Payouts (settlement; tracks execution status per MITIGATION_PLAN.md §2.3)
CREATE TABLE finance.payouts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operator_id UUID NOT NULL REFERENCES ops.operators(id) ON DELETE RESTRICT,
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    amount NUMERIC(12,2) NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'initiated', 'executed', 'failed', 'reconciled')),
    bank_txn_ref TEXT, -- Bank transaction reference (filled once executed)
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    initiated_at TIMESTAMPTZ,
    executed_at TIMESTAMPTZ,
    reconciled_at TIMESTAMPTZ
);

CREATE INDEX idx_payouts_operator ON finance.payouts(operator_id);
CREATE INDEX idx_payouts_status ON finance.payouts(status);
CREATE INDEX idx_payouts_period ON finance.payouts(period_start, period_end);

-- Enable RLS for finance schema tables (operator can only see their own payables/payouts)
ALTER TABLE finance.accounts DISABLE ROW LEVEL SECURITY; -- Chart of Accounts is public
ALTER TABLE finance.accounting_periods DISABLE ROW LEVEL SECURITY; -- Periods are public
ALTER TABLE finance.journal_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE finance.journal_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE finance.operator_ledger_balances ENABLE ROW LEVEL SECURITY;
ALTER TABLE finance.payments ENABLE ROW LEVEL SECURITY;
ALTER TABLE finance.payouts ENABLE ROW LEVEL SECURITY;

-- RLS Policy: operator can see own entries/lines/balances/payouts
CREATE POLICY operator_view_own_ledger ON finance.journal_lines
    FOR SELECT
    USING (
        operator_id IS NULL -- Control account lines (no operator_id) visible to all auth
        OR operator_id = (SELECT id FROM ops.operators WHERE user_id = current_setting('app.current_user_id', true)::uuid)
        OR current_setting('app.current_role', true) = 'platform_admin'
    );

CREATE POLICY operator_view_own_balance ON finance.operator_ledger_balances
    FOR SELECT
    USING (
        operator_id = (SELECT id FROM ops.operators WHERE user_id = current_setting('app.current_user_id', true)::uuid)
        OR current_setting('app.current_role', true) = 'platform_admin'
    );

CREATE POLICY operator_view_own_payouts ON finance.payouts
    FOR SELECT
    USING (
        operator_id = (SELECT id FROM ops.operators WHERE user_id = current_setting('app.current_user_id', true)::uuid)
        OR current_setting('app.current_role', true) = 'platform_admin'
    );

-- Immutability: App role (slbus_app) cannot UPDATE/DELETE journal entries (audit constraint)
-- Insert-only for ledger safety
REVOKE UPDATE, DELETE ON finance.journal_entries FROM slbus_app;
REVOKE UPDATE, DELETE ON finance.journal_lines FROM slbus_app;

-- Function to verify debits = credits on each entry (MITIGATION_PLAN.md §2.2)
CREATE OR REPLACE FUNCTION finance.check_journal_entry_balanced()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    entry_debit NUMERIC;
    entry_credit NUMERIC;
BEGIN
    SELECT
        COALESCE(SUM(debit), 0.00),
        COALESCE(SUM(credit), 0.00)
    INTO entry_debit, entry_credit
    FROM finance.journal_lines
    WHERE journal_entry_id = NEW.journal_entry_id;

    IF entry_debit != entry_credit THEN
        RAISE EXCEPTION 'Journal entry % does not balance: debit %, credit %',
            NEW.journal_entry_id, entry_debit, entry_credit;
    END IF;

    RETURN NEW;
END;
$$;

-- Constraint trigger (deferred so entry is complete before check)
CREATE CONSTRAINT TRIGGER trg_journal_entry_balanced
AFTER INSERT ON finance.journal_lines
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION finance.check_journal_entry_balanced();

-- Grant read to slbus_app (it will SELECT for reporting, but only INSERT to journal_entries/lines)
GRANT SELECT ON ALL TABLES IN SCHEMA finance TO slbus_app;
GRANT INSERT ON finance.journal_entries, finance.journal_lines TO slbus_app;
GRANT INSERT ON finance.payments, finance.payouts TO slbus_app;
GRANT UPDATE ON finance.operator_ledger_balances, finance.payouts TO slbus_app;
