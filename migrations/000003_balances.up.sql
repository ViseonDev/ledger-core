CREATE TABLE account_balances (
    account_id uuid        PRIMARY KEY REFERENCES accounts (id),
    debits     bigint      NOT NULL DEFAULT 0,
    credits    bigint      NOT NULL DEFAULT 0,
    version    bigint      NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- Cumulative totals only ever increase.
    CONSTRAINT account_balances_debits_non_negative  CHECK (debits  >= 0),
    CONSTRAINT account_balances_credits_non_negative CHECK (credits >= 0)
);

COMMENT ON TABLE account_balances IS
    'Materialised cache of each account cumulative debits and credits. Maintained inside the same transaction as the entries that change it.';

-- Every account must have a balance row so row-locking on posting does not find 0 rows.
CREATE FUNCTION ledger_create_account_balance() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO account_balances (account_id, updated_at)
    VALUES (NEW.id, now());
    RETURN NEW;
END;
$$;

CREATE TRIGGER accounts_create_balance
    AFTER INSERT ON accounts
    FOR EACH ROW EXECUTE FUNCTION ledger_create_account_balance();

-- Backfill for any account that already exists when this migration runs.
INSERT INTO account_balances (account_id, updated_at)
SELECT id, now() FROM accounts
ON CONFLICT (account_id) DO NOTHING;
