CREATE TYPE entry_direction AS ENUM ('DEBIT', 'CREDIT');

CREATE TABLE transactions (
    id          uuid        PRIMARY KEY,
    reference   text,
    currency    char(3)     NOT NULL,
    posted_at   timestamptz NOT NULL,
    reversal_of uuid        REFERENCES transactions (id),
    metadata    jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT clock_timestamp(),

    CONSTRAINT transactions_currency_format CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT transactions_reversal_not_self CHECK (reversal_of IS NULL OR reversal_of <> id)
);

-- A transaction is reversed at most once. Partial index allows multiple unreversed transactions with NULL.
CREATE UNIQUE INDEX transactions_reversal_of_key
    ON transactions (reversal_of)
    WHERE reversal_of IS NOT NULL;

CREATE INDEX transactions_reference_idx
    ON transactions (reference)
    WHERE reference IS NOT NULL;

CREATE INDEX transactions_posted_at_idx ON transactions (posted_at DESC);

CREATE TABLE entries (
    id             bigint          GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    transaction_id uuid            NOT NULL REFERENCES transactions (id),
    account_id     uuid            NOT NULL REFERENCES accounts (id),
    direction      entry_direction NOT NULL,
    amount         bigint          NOT NULL,
    currency       char(3)         NOT NULL,
    posted_at      timestamptz     NOT NULL,

    -- Enforce positive amount in DB as secondary defence.
    CONSTRAINT entries_amount_positive CHECK (amount > 0)
);

-- Index for fetching an account's entries in sequence.
CREATE INDEX entries_account_id_idx ON entries (account_id, id);
CREATE INDEX entries_transaction_id_idx ON entries (transaction_id);

-- The journal is append-only.
CREATE FUNCTION ledger_forbid_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION
        'ledger: % on %.% is forbidden; the journal is append-only. Correct a mistake by posting a reversal.',
        TG_OP, TG_TABLE_SCHEMA, TG_TABLE_NAME
        USING ERRCODE = 'LG001';
END;
$$;

COMMENT ON FUNCTION ledger_forbid_mutation() IS
    'Raises SQLSTATE LG001 for any UPDATE, DELETE or TRUNCATE on an append-only table.';

CREATE TRIGGER transactions_append_only
    BEFORE UPDATE OR DELETE ON transactions
    FOR EACH ROW EXECUTE FUNCTION ledger_forbid_mutation();

CREATE TRIGGER transactions_no_truncate
    BEFORE TRUNCATE ON transactions
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_forbid_mutation();

CREATE TRIGGER entries_append_only
    BEFORE UPDATE OR DELETE ON entries
    FOR EACH ROW EXECUTE FUNCTION ledger_forbid_mutation();

CREATE TRIGGER entries_no_truncate
    BEFORE TRUNCATE ON entries
    FOR EACH STATEMENT EXECUTE FUNCTION ledger_forbid_mutation();
