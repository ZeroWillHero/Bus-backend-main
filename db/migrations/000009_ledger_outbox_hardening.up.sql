-- Phase 6 hardening: make the ledger and outbox actually work end to end.
-- Found by the Phase 6 integration tests (see PHASE-6-STATUS.md).

-- 1. Chart of accounts. The journal service looks these codes up by name but
--    no migration ever created them, so every ledger posting failed on a fresh DB.
INSERT INTO finance.accounts (code, name, account_type, is_control) VALUES
    ('1200-AR-BOOKINGS',  'Accounts Receivable - Bookings', 'asset',     FALSE),
    ('5100-REV-BOOKINGS', 'Booking Revenue',                'revenue',   FALSE),
    ('2300-OP-PAYABLE',   'Operator Payable',               'liability', TRUE)
ON CONFLICT (code) DO NOTHING;

-- 2. System-generated entries (outbox relay) have no human author.
ALTER TABLE finance.journal_entries ALTER COLUMN created_by DROP NOT NULL;

-- 3. Idempotency. The outbox is at-least-once, so the same event can arrive
--    twice; one entry per (source_type, source_id) makes the second a no-op.
CREATE UNIQUE INDEX uq_journal_entries_source
    ON finance.journal_entries (source_type, source_id);

-- 4. Append-only for EVERY role. The earlier REVOKE only constrains slbus_app;
--    the superuser/migration role and any future role could still rewrite history.
CREATE OR REPLACE FUNCTION finance.reject_ledger_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'finance.% is append-only: % is not allowed (post a reversing entry instead)',
        TG_TABLE_NAME, TG_OP USING ERRCODE = 'integrity_constraint_violation';
END;
$$;

CREATE TRIGGER trg_journal_entries_append_only
    BEFORE UPDATE OR DELETE ON finance.journal_entries
    FOR EACH ROW EXECUTE FUNCTION finance.reject_ledger_mutation();
CREATE TRIGGER trg_journal_lines_append_only
    BEFORE UPDATE OR DELETE ON finance.journal_lines
    FOR EACH ROW EXECUTE FUNCTION finance.reject_ledger_mutation();
CREATE TRIGGER trg_journal_entries_no_truncate
    BEFORE TRUNCATE ON finance.journal_entries
    FOR EACH STATEMENT EXECUTE FUNCTION finance.reject_ledger_mutation();
CREATE TRIGGER trg_journal_lines_no_truncate
    BEFORE TRUNCATE ON finance.journal_lines
    FOR EACH STATEMENT EXECUTE FUNCTION finance.reject_ledger_mutation();

-- 5. Outbox claim lease so several workers can poll without double-processing
--    (FOR UPDATE SKIP LOCKED + a lease that survives past the claiming statement).
ALTER TABLE platform.outbox_events
    ADD COLUMN claimed_until TIMESTAMPTZ,
    ADD COLUMN attempts      INT NOT NULL DEFAULT 0;

CREATE INDEX idx_outbox_pending
    ON platform.outbox_events (created_at)
    WHERE processed_at IS NULL;
