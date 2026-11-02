CREATE TYPE payment_status AS ENUM (
    'PENDING',
    'IN_PROGRESS',
    'SETTLED',
    'COMPENSATING',
    'COMPENSATED',
    'COMPENSATION_FAILED'
);

CREATE TYPE payment_step_status AS ENUM ('PENDING', 'DONE', 'COMPENSATED', 'FAILED');

CREATE TABLE payment_intents (
    id              uuid           PRIMARY KEY,
    reference       text           NOT NULL,
    customer_ref    text           NOT NULL,
    merchant_ref    text           NOT NULL,
    amount          bigint         NOT NULL,
    fee_amount      bigint         NOT NULL,
    currency        char(3)        NOT NULL,
    status          payment_status NOT NULL,
    attempts        int            NOT NULL DEFAULT 0,
    -- Acts as the lease: a worker claiming this payment pushes next_attempt_at
    -- into the future so another worker does not pick it up concurrently.
    next_attempt_at timestamptz    NOT NULL DEFAULT now(),
    last_error      text,
    created_at      timestamptz    NOT NULL DEFAULT now(),
    updated_at      timestamptz    NOT NULL DEFAULT now(),

    CONSTRAINT payment_amount_positive CHECK (amount > 0),
    CONSTRAINT payment_fee_non_negative CHECK (fee_amount >= 0),
    CONSTRAINT payment_fee_within_amount CHECK (fee_amount <= amount),
    CONSTRAINT payment_currency_format CHECK (currency ~ '^[A-Z]{3}$')
);

-- Partial index over live statuses only: settled payments stick around forever
-- and are never claimed again, so we save index space and write overhead.
CREATE INDEX payment_intents_due_idx
    ON payment_intents (next_attempt_at)
    WHERE status IN ('PENDING', 'IN_PROGRESS', 'COMPENSATING');

CREATE INDEX payment_intents_reference_idx ON payment_intents (reference);

CREATE TABLE payment_steps (
    payment_id                  uuid                NOT NULL REFERENCES payment_intents (id) ON DELETE CASCADE,
    seq                         int                 NOT NULL,
    name                        text                NOT NULL,
    status                      payment_step_status NOT NULL,
    attempts                    int                 NOT NULL DEFAULT 0,
    -- Ledger transaction posted by this step, so compensation can reverse it directly.
    transaction_id              uuid                REFERENCES transactions (id),
    compensation_transaction_id uuid                REFERENCES transactions (id),
    -- External processor reference (auth ID, capture ID) needed for void or refund.
    external_ref                text,
    last_error                  text,
    updated_at                  timestamptz         NOT NULL DEFAULT now(),

    PRIMARY KEY (payment_id, seq)
);

COMMENT ON COLUMN payment_intents.next_attempt_at IS
    'When this payment may next be claimed. Pushed forward on claim so crashed workers payments automatically become claimable again.';

