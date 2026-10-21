package integration

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
	"github.com/ViseonDev/ledger-core/internal/domain/money"
)

// newAccountID generates a UUIDv7 in infrastructure.
func newAccountID(t *testing.T) ledger.AccountID {
	t.Helper()

	raw, err := uuid.NewV7()
	require.NoError(t, err)

	id, err := ledger.ParseAccountID(raw.String())
	require.NoError(t, err)
	return id
}

func newTransactionID(t *testing.T) ledger.TransactionID {
	t.Helper()

	raw, err := uuid.NewV7()
	require.NoError(t, err)

	id, err := ledger.ParseTransactionID(raw.String())
	require.NoError(t, err)
	return id
}

// eur creates a money.Money value in EUR.
func eur(t *testing.T, amount int64) money.Money {
	t.Helper()

	m, err := money.New(amount, money.EUR)
	require.NoError(t, err)
	return m
}

// openAccount creates and persists an account, defaulting to EUR.
func openAccount(t *testing.T, store *ledgerStore, code string, accountType ledger.AccountType, allowNegative bool) ledger.Account {
	t.Helper()

	parsedCode, err := ledger.ParseAccountCode(code)
	require.NoError(t, err)

	account, err := ledger.NewAccount(ledger.AccountParams{
		ID:            newAccountID(t),
		Code:          parsedCode,
		Name:          code,
		Type:          accountType,
		Currency:      money.EUR,
		AllowNegative: allowNegative,
	})
	require.NoError(t, err)
	require.NoError(t, store.OpenAccount(t.Context(), account))

	return account
}

// entry creates a ledger.Entry in EUR for clean test readability.
func entry(t *testing.T, account ledger.Account, direction ledger.Direction, amount int64) ledger.Entry {
	t.Helper()

	e, err := ledger.NewEntry(account.ID(), direction, eur(t, amount))
	require.NoError(t, err)
	return e
}

// postTransaction builds and posts a transaction from the given entries.
func postTransaction(t *testing.T, store *ledgerStore, reference string, entries ...ledger.Entry) *ledger.Transaction {
	t.Helper()

	tx, err := ledger.NewTransaction(ledger.TransactionParams{
		ID:        newTransactionID(t),
		Reference: reference,
		Entries:   entries,
		PostedAt:  time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NoError(t, store.Post(t.Context(), tx))

	return tx
}
