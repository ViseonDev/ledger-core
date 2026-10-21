package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
	"github.com/ViseonDev/ledger-core/internal/domain/money"
)

const insertAccountSQL = `
INSERT INTO accounts (id, code, name, account_type, currency, allow_negative, status)
VALUES ($1, $2, $3, $4, $5, $6, $7)
`

const selectAccountSQL = `
SELECT id, code, name, account_type, currency, allow_negative, status
FROM accounts
`

const closeAccountSQL = `
UPDATE accounts
SET status = 'CLOSED', updated_at = now()
WHERE id = $1 AND status = 'ACTIVE'
`

// OpenAccount inserts a new account. The account_balances row is created by a
// trigger, so this is a single statement.
func (s *Store) OpenAccount(ctx context.Context, account ledger.Account) error {
	return s.openAccount(ctx, s.pool, account)
}

func (s *Store) openAccount(ctx context.Context, db DBTX, account ledger.Account) error {
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

// CloseAccount marks an account closed. It reports ErrAccountNotFound if no
// active account has that id.
func (s *Store) CloseAccount(ctx context.Context, id ledger.AccountID) error {
	tag, err := s.pool.Exec(ctx, closeAccountSQL, id.String())
	if err != nil {
		return fmt.Errorf("closing account %s: %w", id, translateError(err))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", ledger.ErrAccountNotFound, id)
	}
	return nil
}

// AccountByID loads one account.
func (s *Store) AccountByID(ctx context.Context, id ledger.AccountID) (ledger.Account, error) {
	return s.accountBy(ctx, s.pool, "WHERE id = $1", id.String())
}

// AccountByCode loads one account by its human-readable code.
func (s *Store) AccountByCode(ctx context.Context, code ledger.AccountCode) (ledger.Account, error) {
	return s.accountBy(ctx, s.pool, "WHERE code = $1", code.String())
}

func (s *Store) accountBy(ctx context.Context, db DBTX, where string, arg any) (ledger.Account, error) {
	rows, err := db.Query(ctx, selectAccountSQL+where, arg)
	if err != nil {
		return ledger.Account{}, fmt.Errorf("querying account: %w", translateError(err))
	}

	account, err := pgx.CollectExactlyOneRow(rows, scanAccount)
	if errors.Is(err, pgx.ErrNoRows) {
		return ledger.Account{}, fmt.Errorf("%w: %v", ledger.ErrAccountNotFound, arg)
	}
	if err != nil {
		return ledger.Account{}, fmt.Errorf("scanning account: %w", err)
	}
	return account, nil
}

// ListAccounts returns accounts whose code sits at or beneath a prefix, ordered by code.
func (s *Store) ListAccounts(ctx context.Context, prefix string, limit int) ([]ledger.Account, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	const listSQL = selectAccountSQL + `
WHERE ($1 = '' OR code = $1 OR code LIKE $2 ESCAPE '\')
ORDER BY code
LIMIT $3
`

	rows, err := s.pool.Query(ctx, listSQL, prefix, likePrefixPattern(prefix), limit)
	if err != nil {
		return nil, fmt.Errorf("listing accounts: %w", translateError(err))
	}

	accounts, err := pgx.CollectRows(rows, scanAccount)
	if err != nil {
		return nil, fmt.Errorf("scanning accounts: %w", err)
	}
	return accounts, nil
}

// likePrefixPattern builds a LIKE pattern matching the given code prefix and
// everything beneath it, escaping wildcards.
func likePrefixPattern(prefix string) string {
	if prefix == "" {
		return ""
	}
	return escapeLike(prefix) + `:%`
}

func escapeLike(s string) string {
	escaped := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '%', '_', '\\':
			escaped = append(escaped, '\\')
		}
		escaped = append(escaped, s[i])
	}
	return string(escaped)
}

// scanAccount turns one row into a domain Account.
func scanAccount(row pgx.CollectableRow) (ledger.Account, error) {
	var (
		id            string
		code          string
		name          string
		accountType   string
		currencyCode  string
		allowNegative bool
		status        string
	)
	if err := row.Scan(&id, &code, &name, &accountType, &currencyCode, &allowNegative, &status); err != nil {
		return ledger.Account{}, err
	}

	accountID, err := ledger.ParseAccountID(id)
	if err != nil {
		return ledger.Account{}, err
	}
	accountCode, err := ledger.ParseAccountCode(code)
	if err != nil {
		return ledger.Account{}, err
	}
	parsedType, err := ledger.ParseAccountType(accountType)
	if err != nil {
		return ledger.Account{}, err
	}
	currency, err := money.ParseCurrency(currencyCode)
	if err != nil {
		return ledger.Account{}, err
	}
	parsedStatus, err := ledger.ParseAccountStatus(status)
	if err != nil {
		return ledger.Account{}, err
	}

	return ledger.RehydrateAccount(ledger.AccountParams{
		ID:            accountID,
		Code:          accountCode,
		Name:          name,
		Type:          parsedType,
		Currency:      currency,
		AllowNegative: allowNegative,
	}, parsedStatus)
}
