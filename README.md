# Ledger Core

A double-entry ledger and payment orchestrator in Go.

Every money movement is a balanced, append-only journal transaction. Payments run as a small saga (reserve, authorize, capture, settle), and if a step fails for good, the steps already done get undone in reverse order.

## How it works

- **Money is `int64` minor units** with an ISO 4217 currency, so 50.00 EUR is `5000`. No floats anywhere in the domain (a `forbidigo` lint rule enforces it). Splitting an amount uses `Allocate()`, which hands out the leftover cents one by one so the parts always add back up.
- **Transactions can't be built unbalanced.** `NewTransaction()` checks debits == credits, a single currency, and valid entries. There are no exported fields to get around it.
- **The journal is append-only.** Postgres triggers on `transactions` and `entries` reject UPDATE, DELETE and TRUNCATE with SQLSTATE `LG001`. Mistakes are fixed by posting a reversal (`NewReversal()`) that links to the original.
- **No deadlocks between postings.** Account rows are locked with `SELECT ... FOR UPDATE` in sorted id order, so two transactions touching the same accounts always lock them in the same order.
- **Idempotency in the same DB transaction.** Writes need an `Idempotency-Key` header. The key is claimed (`INSERT ... ON CONFLICT DO NOTHING`) in the same transaction as the ledger write, and the request body is fingerprinted with SHA-256. A retry gets the stored response back with `Idempotent-Replayed: true`. The same key with a different body gets `409 Conflict`.
- **Payment saga.** Steps are reserve (ledger), authorize (PSP), capture (PSP), settle (ledger). Transient PSP errors are retried with backoff. A decline, or running out of retries, unwinds what was done: a PSP void for authorize, a refund for capture, a ledger reversal for reserve and settle. If an undo fails, the payment ends in `COMPENSATION_FAILED` and an alert fires.
- **The domain only uses the standard library.** [internal/domain/](internal/domain) has no DB driver, router or protobuf imports. An architecture test for this is still WIP in [internal/arch/](internal/arch).

Layout: REST (chi) and gRPC adapters call the use cases in `internal/app`, which use the domain and the outbound adapters (Postgres via pgx, Redis for worker leases, an in-memory PSP simulator).

## Running it

Needs Docker with Compose v2, Make, and Go 1.26+ if you run the tests outside containers.

```bash
cp .env.example .env
make up
```

This starts PostgreSQL 18 and Redis 8, runs the migrations and starts `ledgerd` (REST on `:8080`, gRPC on `:9090`).

```bash
make test-unit   # no Docker needed
make test-int    # real Postgres and Redis via testcontainers
make lint
```

Load demo data (chart of accounts, top-ups, payments and some reversals, all deterministic):

```bash
make seed
```

Check that every cached balance matches the journal:

```bash
make reconcile
# checked N accounts, 0 drifted
```

## API examples

Open an account, then read its balance. Accounts are named by a colon-separated code. Balances are read by account id (UUID):

```bash
curl -s -X POST http://localhost:8080/v1/accounts \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: open-wallet-alice" \
  -d '{"code": "liabilities:customer:wallet:alice", "name": "Alice wallet", "type": "LIABILITY", "currency": "EUR"}' | jq .

curl -s "http://localhost:8080/v1/accounts/<account-id>/balance?authoritative=true" | jq .
```

```json
{
  "account_id": "<account-id>",
  "currency": "EUR",
  "debits": 0,
  "credits": 25000,
  "balance": 25000,
  "authoritative": true
}
```

Post a transaction:

```bash
curl -i -X POST http://localhost:8080/v1/transactions \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: topup-eur-0001" \
  -d '{
    "reference": "topup-0001",
    "currency": "EUR",
    "entries": [
      {"account_code": "assets:cash:operating", "direction": "DEBIT", "amount": 5000},
      {"account_code": "liabilities:customer:wallet:alice", "direction": "CREDIT", "amount": 5000}
    ]
  }'
```

Errors come back as RFC 9457 problem details. An unbalanced transaction, for example:

```bash
curl -s -X POST http://localhost:8080/v1/transactions \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: bad-tx-001" \
  -d '{
    "reference": "bad-0001",
    "currency": "EUR",
    "entries": [
      {"account_code": "assets:cash:operating", "direction": "DEBIT", "amount": 5000},
      {"account_code": "liabilities:customer:wallet:alice", "direction": "CREDIT", "amount": 4000}
    ]
  }' | jq .
```

```json
{
  "type": "https://ledger.local/problems/unbalanced-transaction",
  "title": "Transaction does not balance",
  "status": 422,
  "detail": "ledger: transaction does not balance: debits total 5000 EUR, credits total 4000 EUR",
  "instance": "/v1/transactions",
  "request_id": "..."
}
```

The same operations are available over gRPC ([proto/ledger/v1/ledger.proto](proto/ledger/v1/ledger.proto)):

```bash
grpcurl -plaintext -d '{"account_id": "<account-id>"}' \
  localhost:9090 ledger.v1.LedgerService/GetBalance
```

## Repository layout

```
cmd/
  ledgerd/        service (REST, gRPC, saga worker)
  ledgerctl/      CLI: migrate, seed, reconcile, purge-idempotency
  junitreport/    turns CI JUnit output into a summary and publishes it
internal/
  domain/         money, ledger, payment (standard library only)
  app/            use cases: ledger, payment orchestrator, worker
  ports/          interfaces the app depends on
  adapters/       postgres, httpapi, grpcapi, psp, redis, uuidgen
  platform/       config, logging, metrics, clock
  seed/           deterministic demo data
  arch/           architecture tests (WIP)
migrations/       SQL migrations (up and down)
proto/            protobuf definitions (Buf)
deploy/           Caddy, backups, Prometheus alerts
test/integration/ integration tests (testcontainers)
```

## Deployment

`make prod-up` runs the production setup on a single VPS:

- Postgres, Redis and `ledgerd` only listen on `127.0.0.1` ([docker-compose.prod.yml](docker-compose.prod.yml)). Without that, Docker's own firewall rules would expose them past ufw.
- Caddy terminates TLS with Let's Encrypt ([deploy/Caddyfile](deploy/Caddyfile)). `GET` endpoints and health checks are public so anyone can look around. Writes need basic auth, and `/metrics` returns 404.
- A nightly `pg_dump` ([deploy/backup.sh](deploy/backup.sh)) is checked with `pg_restore --list` and kept for 14 days.
- Alerts ([deploy/alerts.yml](deploy/alerts.yml)) fire on `COMPENSATION_FAILED` and when a cached balance drifts from the journal.

## Not included

- User auth. The service expects to sit behind a proxy (Caddy basic auth in the deploy setup).
- Multi-region. It's a single Postgres primary.
