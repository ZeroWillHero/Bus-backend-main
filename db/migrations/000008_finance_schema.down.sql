DROP TRIGGER IF EXISTS trg_journal_entry_balanced ON finance.journal_lines;
DROP FUNCTION IF EXISTS finance.check_journal_entry_balanced();
DROP SCHEMA IF EXISTS finance CASCADE;
