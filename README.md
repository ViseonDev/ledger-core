# Ledger Core & Payment Orchestration

A Go backend built to move money safely and never lose track of a single cent. It uses a strict double entry ledger to guarantee your books always balance, alongside a smart payment orchestrator. If a complex transaction fails halfway through, the system automatically hits undo to prevent double charges and locked funds.

## Quick start

```bash
cp .env.example .env
make up      # Starts PostgreSQL and Redis, waits for health
make lint    # Run linters
make down    # Stops and removes PostgreSQL and Redis
make fmt     # Run formatters
make clean   # Remove volumes and build artefacts
```