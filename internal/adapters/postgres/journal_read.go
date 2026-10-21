package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
	"github.com/ViseonDev/ledger-core/internal/domain/money"
)

const selectTransactionSQL = `
SELECT id, reference, currency, posted_at, reversal_of, metadata
FROM transactions
WHERE id = $1
`

const selectEntriesForTransactionSQL = `
SELECT account_id, direction, amount, currency
FROM entries
WHERE transaction_id = $1
ORDER BY id
`

const selectBalanceSQL = `
SELECT a.account_type, a.currency, b.debits, b.credits
FROM account_balances b
JOIN accounts a ON a.id = b.account_id
WHERE b.account_id = $1
`

// recomputeBalanceSQL derives a balance from the journal itself, ignoring the cache.
const recomputeBalanceSQL = `
SELECT a.account_type,
       a.currency,
       COALESCE(SUM(e.amount) FILTER (WHERE e.direction = 'DEBIT'),  0) AS debits,
       COALESCE(SUM(e.amount) FILTER (WHERE e.direction = 'CREDIT'), 0) AS credits
FROM accounts a
LEFT JOIN entries e ON e.account_id = a.id
WHERE a.id = $1
GROUP BY a.account_type, a.currency
`

const selectEntriesForAccountSQL = `
SELECT e.id, e.transaction_id, e.direction, e.amount, e.currency, e.posted_at, t.reference
FROM entries e
JOIN transactions t ON t.id = e.transaction_id
WHERE e.account_id = $1 AND e.id > $2
ORDER BY e.id
LIMIT $3
`

// JournalEntry is a read model: one entry with the transaction context needed to interpret it.
type JournalEntry struct {
	ID            int64
	TransactionID ledger.TransactionID
	Reference     string
	Direction     ledger.Direction
	Amount        money.Money
	PostedAt      time.Time
}

// TransactionByID loads a transaction and its entries.
func (s *Store) TransactionByID(ctx context.Context, id ledger.TransactionID) (*ledger.Transaction, error) {
	var (
		txID       string
		reference  *string
		currency   string
		postedAt   time.Time
		reversalOf *string
		metadata   []byte
	)

	err := s.pool.QueryRow(ctx, selectTransactionSQL, id.String()).
		Scan(&txID, &reference, &currency, &postedAt, &reversalOf, &metadata)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ledger.ErrTransactionNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("loading transaction %s: %w", id, translateError(err))
	}

	entries, err := s.entriesForTransaction(ctx, id)
	if err != nil {
		return nil, err
	}

	var meta map[string]string
	if len(metadata) > 0 {
		if err = json.Unmarshal(metadata, &meta); err != nil {
			return nil, fmt.Errorf("decoding metadata for %s: %w", id, err)
		}
	}

	parsedID, err := ledger.ParseTransactionID(txID)
	if err != nil {
		return nil, err
	}

	var reversed ledger.TransactionID
	if reversalOf != nil {
		reversed, err = ledger.ParseTransactionID(*reversalOf)
		if err != nil {
			return nil, err
		}
	}

	return ledger.RehydrateTransaction(ledger.TransactionParams{
		ID:        parsedID,
		Reference: derefString(reference),
		Entries:   entries,
		PostedAt:  postedAt,
		Metadata:  meta,
	}, reversed)
}

func (s *Store) entriesForTransaction(ctx context.Context, id ledger.TransactionID) ([]ledger.Entry, error) {
	rows, err := s.pool.Query(ctx, selectEntriesForTransactionSQL, id.String())
	if err != nil {
		return nil, fmt.Errorf("loading entries for %s: %w", id, translateError(err))
	}

	entries, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ledger.Entry, error) {
		var (
			accountID    string
			direction    string
			amount       int64
			currencyCode string
		)
		if scanErr := row.Scan(&accountID, &direction, &amount, &currencyCode); scanErr != nil {
			return ledger.Entry{}, scanErr
		}
		return buildEntry(accountID, direction, amount, currencyCode)
	})
	if err != nil {
		return nil, fmt.Errorf("scanning entries for %s: %w", id, err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: transaction %s has no entries", ledger.ErrTransactionNotFound, id)
	}
	return entries, nil
}

// Balance reads the cached balance from account_balances table.
func (s *Store) Balance(ctx context.Context, id ledger.AccountID) (ledger.Balance, error) {
	return s.scanBalance(ctx, selectBalanceSQL, id)
}

// RecomputeBalance derives the balance from every entry ever written, ignoring the cache.
func (s *Store) RecomputeBalance(ctx context.Context, id ledger.AccountID) (ledger.Balance, error) {
	return s.scanBalance(ctx, recomputeBalanceSQL, id)
}

func (s *Store) scanBalance(ctx context.Context, query string, id ledger.AccountID) (ledger.Balance, error) {
	var (
		accountType  string
		currencyCode string
		debits       int64
		credits      int64
	)

	err := s.pool.QueryRow(ctx, query, id.String()).Scan(&accountType, &currencyCode, &debits, &credits)
	if errors.Is(err, pgx.ErrNoRows) {
		return ledger.Balance{}, fmt.Errorf("%w: %s", ledger.ErrAccountNotFound, id)
	}
	if err != nil {
		return ledger.Balance{}, fmt.Errorf("reading balance for %s: %w", id, translateError(err))
	}

	parsedType, err := ledger.ParseAccountType(accountType)
	if err != nil {
		return ledger.Balance{}, err
	}
	currency, err := money.ParseCurrency(currencyCode)
	if err != nil {
		return ledger.Balance{}, err
	}
	debitMoney, err := money.New(debits, currency)
	if err != nil {
		return ledger.Balance{}, err
	}
	creditMoney, err := money.New(credits, currency)
	if err != nil {
		return ledger.Balance{}, err
	}

	return ledger.NewBalance(id, parsedType, debitMoney, creditMoney)
}

// EntriesForAccount returns a page of one account's journal.
func (s *Store) EntriesForAccount(ctx context.Context, id ledger.AccountID, after int64, limit int) ([]JournalEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	rows, err := s.pool.Query(ctx, selectEntriesForAccountSQL, id.String(), after, limit)
	if err != nil {
		return nil, fmt.Errorf("listing entries for %s: %w", id, translateError(err))
	}

	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (JournalEntry, error) {
		var (
			entryID       int64
			transactionID string
			direction     string
			amount        int64
			currencyCode  string
			postedAt      time.Time
			reference     *string
		)
		if err := row.Scan(&entryID, &transactionID, &direction, &amount, &currencyCode, &postedAt, &reference); err != nil {
			return JournalEntry{}, err
		}

		parsedTxID, err := ledger.ParseTransactionID(transactionID)
		if err != nil {
			return JournalEntry{}, err
		}
		parsedDirection, err := ledger.ParseDirection(direction)
		if err != nil {
			return JournalEntry{}, err
		}
		currency, err := money.ParseCurrency(currencyCode)
		if err != nil {
			return JournalEntry{}, err
		}
		amountMoney, err := money.New(amount, currency)
		if err != nil {
			return JournalEntry{}, err
		}

		return JournalEntry{
			ID:            entryID,
			TransactionID: parsedTxID,
			Reference:     derefString(reference),
			Direction:     parsedDirection,
			Amount:        amountMoney,
			PostedAt:      postedAt,
		}, nil
	})
}

func buildEntry(accountID, direction string, amount int64, currencyCode string) (ledger.Entry, error) {
	parsedAccount, err := ledger.ParseAccountID(accountID)
	if err != nil {
		return ledger.Entry{}, err
	}
	parsedDirection, err := ledger.ParseDirection(direction)
	if err != nil {
		return ledger.Entry{}, err
	}
	currency, err := money.ParseCurrency(currencyCode)
	if err != nil {
		return ledger.Entry{}, err
	}
	amountMoney, err := money.New(amount, currency)
	if err != nil {
		return ledger.Entry{}, err
	}
	return ledger.NewEntry(parsedAccount, parsedDirection, amountMoney)
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
