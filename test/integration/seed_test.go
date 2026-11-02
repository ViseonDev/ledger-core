package integration

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ViseonDev/ledger-core/internal/seed"
)

func seedConfig() seed.Config {
	cfg := seed.DefaultConfig()
	// Pin anchor to fixed date for reproducibility, using smaller volumes for fast tests.
	cfg.Anchor = time.Date(2026, time.August, 17, 0, 0, 0, 0, time.UTC)
	cfg.Customers = 12
	cfg.Merchants = 4
	cfg.HistoryDays = 120
	return cfg
}

func TestSeedIsIdempotent(t *testing.T) {
	store := newTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	seeder := seed.New(store, logger)
	cfg := seedConfig()

	first, err := seeder.Run(t.Context(), cfg)
	require.NoError(t, err)
	assert.Positive(t, first.AccountsCreated)
	assert.Positive(t, first.TransactionsCreated)
	assert.Zero(t, first.AccountsSkipped)

	countAccounts := func() int {
		var n int
		require.NoError(t, store.Pool().QueryRow(t.Context(), `SELECT count(*) FROM accounts`).Scan(&n))
		return n
	}
	countTransactions := func() int {
		var n int
		require.NoError(t, store.Pool().QueryRow(t.Context(), `SELECT count(*) FROM transactions`).Scan(&n))
		return n
	}

	accountsAfterFirst := countAccounts()
	transactionsAfterFirst := countTransactions()

	// Second run must find everything already present and skip creation.
	second, err := seeder.Run(t.Context(), cfg)
	require.NoError(t, err)

	assert.Zero(t, second.AccountsCreated, "the second run created accounts")
	assert.Zero(t, second.TransactionsCreated, "the second run created transactions")
	assert.Equal(t, accountsAfterFirst, countAccounts())
	assert.Equal(t, transactionsAfterFirst, countTransactions())
}

func TestSeedProducesEnoughData(t *testing.T) {
	store := newTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	_, err := seed.New(store, logger).Run(t.Context(), seedConfig())
	require.NoError(t, err)

	var transactions, entries, reversals int
	require.NoError(t, store.Pool().QueryRow(t.Context(), `SELECT count(*) FROM transactions`).Scan(&transactions))
	require.NoError(t, store.Pool().QueryRow(t.Context(), `SELECT count(*) FROM entries`).Scan(&entries))
	require.NoError(t, store.Pool().QueryRow(t.Context(),
		`SELECT count(*) FROM transactions WHERE reversal_of IS NOT NULL`).Scan(&reversals))

	assert.Greater(t, transactions, 100, "not enough transactions for a journal page to be meaningful")
	assert.Greater(t, entries, 200)
	assert.Positive(t, reversals, "the demo needs at least one reversal chain")

	// Historical spread should cover months.
	var spanDays float64 //nolint:forbidigo // epoch calculation in days
	require.NoError(t, store.Pool().QueryRow(t.Context(),
		`SELECT EXTRACT(EPOCH FROM (max(posted_at) - min(posted_at))) / 86400 FROM transactions`).Scan(&spanDays))
	assert.Greater(t, spanDays, 60.0, "history is not spread across months")
}

// Seeded data must balance globally: sum(debits) == sum(credits).
func TestSeededLedgerBalances(t *testing.T) {
	store := newTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	_, err := seed.New(store, logger).Run(t.Context(), seedConfig())
	require.NoError(t, err)

	var debits, credits int64
	require.NoError(t, store.Pool().QueryRow(t.Context(), `
		SELECT COALESCE(SUM(amount) FILTER (WHERE direction = 'DEBIT'),  0),
		       COALESCE(SUM(amount) FILTER (WHERE direction = 'CREDIT'), 0)
		FROM entries
	`).Scan(&debits, &credits))

	assert.Equal(t, debits, credits, "the seeded ledger does not balance")

	// Verify every cached balance matches recomputed totals from the journal.
	accounts, err := store.ListAccounts(t.Context(), "", 1000)
	require.NoError(t, err)

	for _, account := range accounts {
		cached, bErr := store.Balance(t.Context(), account.ID())
		require.NoError(t, bErr)
		authoritative, rErr := store.RecomputeBalance(t.Context(), account.ID())
		require.NoError(t, rErr)

		assert.Equal(t, authoritative.Debits().Amount(), cached.Debits().Amount(), "account %s", account.Code())
		assert.Equal(t, authoritative.Credits().Amount(), cached.Credits().Amount(), "account %s", account.Code())
	}
}

// Customer wallets have allow_negative false, so none should be overdrawn.
func TestSeededWalletsAreNeverNegative(t *testing.T) {
	store := newTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	_, err := seed.New(store, logger).Run(t.Context(), seedConfig())
	require.NoError(t, err)

	wallets, err := store.ListAccounts(t.Context(), "liabilities:customer:wallet", 1000)
	require.NoError(t, err)
	require.NotEmpty(t, wallets)

	for _, wallet := range wallets {
		balance, err := store.Balance(t.Context(), wallet.ID())
		require.NoError(t, err)

		normal, err := balance.Normal()
		require.NoError(t, err)
		assert.False(t, normal.IsNegative(), "wallet %s is overdrawn at %v", wallet.Code(), normal)
	}
}
