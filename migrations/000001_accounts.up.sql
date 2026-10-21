-- Account classification. An enum rather than a text column with a CHECK:
CREATE TYPE account_type AS ENUM ('ASSET', 'LIABILITY', 'EQUITY', 'REVENUE', 'EXPENSE');

CREATE TYPE account_status AS ENUM ('ACTIVE', 'CLOSED');

CREATE TABLE accounts (
    id             uuid           PRIMARY KEY,
    code           text           NOT NULL,
    name           text           NOT NULL,
    account_type   account_type   NOT NULL,
    currency       char(3)        NOT NULL,
    allow_negative boolean        NOT NULL DEFAULT false,
    status         account_status NOT NULL DEFAULT 'ACTIVE',
    created_at     timestamptz    NOT NULL DEFAULT now(),
    updated_at     timestamptz    NOT NULL DEFAULT now(),

    CONSTRAINT accounts_code_key UNIQUE (code),

    -- The same grammar AccountCode.Parse enforces in the domain.
    -- Protects seeds, migrations, and direct psql queries.
    CONSTRAINT accounts_code_format
        CHECK (code ~ '^[a-z0-9_-]+(:[a-z0-9_-]+)+$'),

    CONSTRAINT accounts_currency_format
        CHECK (currency ~ '^[A-Z]{3}$'),

    -- Mirrors ErrAccountCodeTypeMismatch: prevents inverted signs in balance computation.
    CONSTRAINT accounts_code_root_matches_type CHECK (
        (account_type = 'ASSET'     AND code LIKE 'assets:%')      OR
        (account_type = 'LIABILITY' AND code LIKE 'liabilities:%') OR
        (account_type = 'EQUITY'    AND code LIKE 'equity:%')      OR
        (account_type = 'REVENUE'   AND code LIKE 'revenue:%')     OR
        (account_type = 'EXPENSE'   AND code LIKE 'expense:%')
    )
);

-- text_pattern_ops makes prefix queries index-usable regardless of collation.
CREATE INDEX accounts_code_prefix_idx ON accounts (code text_pattern_ops);

COMMENT ON TABLE accounts IS
    'Chart of accounts. Rows are mutable only in name and status; code, type and currency are fixed at creation because changing them would invalidate historical balances.';
