DROP INDEX IF EXISTS platform.idx_outbox_pending;
ALTER TABLE platform.outbox_events DROP COLUMN IF EXISTS attempts, DROP COLUMN IF EXISTS claimed_until;

DROP TRIGGER IF EXISTS trg_journal_lines_no_truncate ON finance.journal_lines;
DROP TRIGGER IF EXISTS trg_journal_entries_no_truncate ON finance.journal_entries;
DROP TRIGGER IF EXISTS trg_journal_lines_append_only ON finance.journal_lines;
DROP TRIGGER IF EXISTS trg_journal_entries_append_only ON finance.journal_entries;
DROP FUNCTION IF EXISTS finance.reject_ledger_mutation();

DROP INDEX IF EXISTS finance.uq_journal_entries_source;
-- created_by NOT NULL and the seeded accounts are intentionally not restored:
-- rows written since may violate NOT NULL, and accounts may be referenced by journal lines.
