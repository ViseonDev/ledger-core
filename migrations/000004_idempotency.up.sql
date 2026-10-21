CREATE TABLE idempotency_records (
    key           text        NOT NULL,
    endpoint      text        NOT NULL,
    fingerprint   bytea       NOT NULL,
    response      jsonb       NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (key, endpoint),

    CONSTRAINT idempotency_key_length      CHECK (length(key) BETWEEN 1 AND 255),
    CONSTRAINT idempotency_endpoint_length CHECK (length(endpoint) BETWEEN 1 AND 128),
    CONSTRAINT idempotency_fingerprint_len CHECK (length(fingerprint) = 32)
);

-- Records are purged on a TTL, so the sweep query needs an index it can range
-- scan rather than a full table scan of a table that only grows.
CREATE INDEX idempotency_records_created_at_idx ON idempotency_records (created_at);

COMMENT ON TABLE idempotency_records IS
    'One row per (idempotency key, endpoint). Inserted and completed inside the same transaction as the work it guards, so a visible row always describes finished work.';

COMMENT ON COLUMN idempotency_records.fingerprint IS
    'SHA-256 of the canonicalised request body. Reusing a key with a different body is rejected rather than replayed.';
