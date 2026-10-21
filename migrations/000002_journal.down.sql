DROP TRIGGER IF EXISTS entries_no_truncate ON entries;
DROP TRIGGER IF EXISTS entries_append_only ON entries;
DROP TRIGGER IF EXISTS transactions_no_truncate ON transactions;
DROP TRIGGER IF EXISTS transactions_append_only ON transactions;
DROP FUNCTION IF EXISTS ledger_forbid_mutation();
DROP TABLE IF EXISTS entries;
DROP TABLE IF EXISTS transactions;
DROP TYPE IF EXISTS entry_direction;
