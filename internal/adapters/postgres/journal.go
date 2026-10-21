package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
	"github.com/ViseonDev/ledger-core/internal/domain/money"
)

// lockBalancesSQL locks the balance rows for a set of accounts in a deterministic order.
const lockBalancesSQL = `
SELECT account_id, debits, credits
FROM account_balances
WHERE account_id = ANY($1::uuid[])
ORDER BY account_id
FOR UPDATE
`

const selectAccountsForPostingSQL = `
SELECT id, code, name, account_type, currency, allow_negative, status
FROM accounts
WHERE id = ANY($1::uuid[])
`

const insertTransactionSQL = `
INSERT INTO transactions (id, reference, currency, posted_at, reversal_of, metadata)
VALUES ($1, $2, $3, $4, $5, $6)
`

const insertEntrySQL = `
INSERT INTO entries (transaction_id, account_id, direction, amount, currency, posted_at)
VALUES ($1, $2, $3, $4, $5, $6)
`

const applyBalanceDeltaSQL = `
UPDATE account_balances
SET debits     = debits  + $2,
    credits    = credits + $3,
    version    = version + 1,
    updated_at = now()
WHERE account_id = $1
`

// Post writes a transaction and updates every affected balance atomically.
func (s *Store) Post(ctx context.Context, tx *ledger.Transaction) error {
	return s.InTx(ctx, func(ctx context.Context, db DBTX) error {
		return PostTransaction(ctx, db, tx)
	})
}

// PostTransaction writes a transaction inside a caller-supplied database transaction.
func PostTransaction(ctx context.Context, db DBTX, tx *ledger.Transaction) error {
	accountIDs := tx.AccountIDs() // sorted ascending by the aggregate
	idStrings := make([]string, len(accountIDs))
	for i, id := range accountIDs {
		idStrings[i] = id.String()
	}

	balances, err := lockBalances(ctx, db, idStrings)
	if err != nil {
		return err
	}
	if len(balances) != len(accountIDs) {
		return missingAccountError(accountIDs, balances)
	}

	accounts, err := loadAccountsForPosting(ctx, db, idStrings)
	if err != nil {
		return err
	}

	deltas, err := validateAndComputeDeltas(tx, accounts, balances)
	if err != nil {
		return err
	}

	if err := insertTransaction(ctx, db, tx); err != nil {
		return err
	}
	return writeEntriesAndBalances(ctx, db, tx, accountIDs, deltas)
}

// storedBalance is the raw cumulative total pair as stored.
type storedBalance struct {
	debits  int64
	credits int64
}

func lockBalances(ctx context.Context, db DBTX, accountIDs []string) (map[ledger.AccountID]storedBalance, error) {
	rows, err := db.Query(ctx, lockBalancesSQL, accountIDs)
	if err != nil {
		return nil, fmt.Errorf("locking balances: %w", translateError(err))
	}
	defer rows.Close()

	balances := make(map[ledger.AccountID]storedBalance, len(accountIDs))
	for rows.Next() {
		var (
			id      string
			debits  int64
			credits int64
		)
		if err := rows.Scan(&id, &debits, &credits); err != nil {
			return nil, fmt.Errorf("scanning locked balance: %w", err)
		}
		accountID, err := ledger.ParseAccountID(id)
		if err != nil {
			return nil, fmt.Errorf("stored account id %q: %w", id, err)
		}
		balances[accountID] = storedBalance{debits: debits, credits: credits}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("locking balances: %w", translateError(err))
	}
	return balances, nil
}

func loadAccountsForPosting(ctx context.Context, db DBTX, accountIDs []string) (map[ledger.AccountID]ledger.Account, error) {
	rows, err := db.Query(ctx, selectAccountsForPostingSQL, accountIDs)
	if err != nil {
		return nil, fmt.Errorf("loading accounts: %w", translateError(err))
	}

	loaded, err := pgx.CollectRows(rows, scanAccount)
	if err != nil {
		return nil, fmt.Errorf("scanning accounts: %w", err)
	}

	accounts := make(map[ledger.AccountID]ledger.Account, len(loaded))
	for _, account := range loaded {
		accounts[account.ID()] = account
	}
	return accounts, nil
}

// validateAndComputeDeltas enforces account rules against the locked
// balances, and returns the per-account change to apply.
func validateAndComputeDeltas(
	tx *ledger.Transaction,
	accounts map[ledger.AccountID]ledger.Account,
	stored map[ledger.AccountID]storedBalance,
) (map[ledger.AccountID]storedBalance, error) {
	deltas := make(map[ledger.AccountID]storedBalance, len(accounts))

	for _, accountID := range tx.AccountIDs() {
		account, ok := accounts[accountID]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ledger.ErrAccountNotFound, accountID)
		}

		current := stored[accountID]
		debits, err := money.New(current.debits, account.Currency())
		if err != nil {
			return nil, err
		}
		credits, err := money.New(current.credits, account.Currency())
		if err != nil {
			return nil, err
		}
		balance, err := ledger.NewBalance(accountID, account.Type(), debits, credits)
		if err != nil {
			return nil, err
		}

		var delta storedBalance
		for _, entry := range tx.EntriesFor(accountID) {
			if err = account.AcceptsAmount(entry.Amount()); err != nil {
				return nil, fmt.Errorf("account %s: %w", account.Code(), err)
			}

			balance, err = balance.Apply(entry.Direction(), entry.Amount())
			if err != nil {
				return nil, fmt.Errorf("account %s: %w", account.Code(), err)
			}
			if entry.IsDebit() {
				delta.debits += entry.Amount().Amount()
			} else {
				delta.credits += entry.Amount().Amount()
			}
		}

		if !account.AllowsNegative() {
			normal, err := balance.Normal()
			if err != nil {
				return nil, err
			}
			if normal.IsNegative() {
				return nil, fmt.Errorf("%w: account %s would end at %v",
					ledger.ErrInsufficientFunds, account.Code(), normal)
			}
		}

		deltas[accountID] = delta
	}

	return deltas, nil
}

func insertTransaction(ctx context.Context, db DBTX, tx *ledger.Transaction) error {
	metadata, err := json.Marshal(tx.Metadata())
	if err != nil {
		return fmt.Errorf("encoding metadata: %w", err)
	}

	var reversalOf *string
	if tx.IsReversal() {
		s := tx.ReversalOf().String()
		reversalOf = &s
	}

	_, err = db.Exec(ctx, insertTransactionSQL,
		tx.ID().String(),
		nullableString(tx.Reference()),
		tx.Currency().Code(),
		tx.PostedAt(),
		reversalOf,
		metadata,
	)
	if err != nil {
		return fmt.Errorf("inserting transaction %s: %w", tx.ID(), translateError(err))
	}
	return nil
}

// writeEntriesAndBalances sends the entry inserts and balance updates as one
// batch (single network round-trip).
func writeEntriesAndBalances(
	ctx context.Context,
	db DBTX,
	tx *ledger.Transaction,
	accountIDs []ledger.AccountID,
	deltas map[ledger.AccountID]storedBalance,
) error {
	batch := &pgx.Batch{}

	for _, entry := range tx.Entries() {
		batch.Queue(insertEntrySQL,
			tx.ID().String(),
			entry.AccountID().String(),
			entry.Direction().String(),
			entry.Amount().Amount(),
			tx.Currency().Code(),
			tx.PostedAt(),
		)
	}
	for _, accountID := range accountIDs {
		delta := deltas[accountID]
		batch.Queue(applyBalanceDeltaSQL, accountID.String(), delta.debits, delta.credits)
	}

	results := db.SendBatch(ctx, batch)
	execErr := drainBatch(results, batch.Len())
	closeErr := results.Close()

	if execErr != nil {
		return fmt.Errorf("writing entries and balances: %w", translateError(execErr))
	}
	if closeErr != nil {
		return fmt.Errorf("closing batch: %w", translateError(closeErr))
	}
	return nil
}

// drainBatch consumes every queued result.
func drainBatch(results pgx.BatchResults, queued int) error {
	for i := range queued {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("batch statement %d: %w", i, err)
		}
	}
	return nil
}

func missingAccountError(requested []ledger.AccountID, found map[ledger.AccountID]storedBalance) error {
	for _, id := range requested {
		if _, ok := found[id]; !ok {
			return fmt.Errorf("%w: %s", ledger.ErrAccountNotFound, id)
		}
	}
	return fmt.Errorf("%w: expected %d accounts, locked %d", ledger.ErrAccountNotFound, len(requested), len(found))
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
