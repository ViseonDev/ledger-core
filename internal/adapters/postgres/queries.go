package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
)

const selectAccountsByCodesSQL = selectAccountSQL + `WHERE code = ANY($1::text[])`

// InsertAccount writes an account inside a caller-supplied transaction.
func InsertAccount(ctx context.Context, db DBTX, account ledger.Account) error {
	_, err := db.Exec(ctx, insertAccountSQL,
		account.ID().String(),
		account.Code().String(),
		account.Name(),
		account.Type().String(),
		account.Currency().Code(),
		account.AllowsNegative(),
		account.Status().String(),
	)
	if err != nil {
		return fmt.Errorf("inserting account %s: %w", account.Code(), translateError(err))
	}
	return nil
}

// AccountsByCodes loads several accounts at once, keyed by code.
func AccountsByCodes(ctx context.Context, db DBTX, codes []string) (map[string]ledger.Account, error) {
	if len(codes) == 0 {
		return map[string]ledger.Account{}, nil
	}

	rows, err := db.Query(ctx, selectAccountsByCodesSQL, codes)
	if err != nil {
		return nil, fmt.Errorf("loading accounts by code: %w", translateError(err))
	}

	loaded, err := pgx.CollectRows(rows, scanAccount)
	if err != nil {
		return nil, fmt.Errorf("scanning accounts: %w", err)
	}

	accounts := make(map[string]ledger.Account, len(loaded))
	for _, account := range loaded {
		accounts[account.Code().String()] = account
	}
	return accounts, nil
}

// LoadTransaction reads a transaction and its entries using the given handle,
// which may be the pool or an open transaction.
func LoadTransaction(ctx context.Context, db DBTX, id ledger.TransactionID) (*ledger.Transaction, error) {
	var (
		txID       string
		reference  *string
		currency   string
		postedAt   time.Time
		reversalOf *string
		metadata   []byte
	)

	err := db.QueryRow(ctx, selectTransactionSQL, id.String()).
		Scan(&txID, &reference, &currency, &postedAt, &reversalOf, &metadata)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ledger.ErrTransactionNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("loading transaction %s: %w", id, translateError(err))
	}

	entries, err := loadEntries(ctx, db, id)
	if err != nil {
		return nil, err
	}

	var meta map[string]string
	if len(metadata) > 0 {
		if metaErr := json.Unmarshal(metadata, &meta); metaErr != nil {
			return nil, fmt.Errorf("decoding metadata for %s: %w", id, metaErr)
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

func loadEntries(ctx context.Context, db DBTX, id ledger.TransactionID) ([]ledger.Entry, error) {
	rows, err := db.Query(ctx, selectEntriesForTransactionSQL, id.String())
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
