package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
	"github.com/ViseonDev/ledger-core/internal/domain/money"
)

func TestPostTransactionRoundTrip(t *testing.T) {
	store := newTestDB(t)
	ctx := t.Context()

	wallet := openAccount(t, store, "liabilities:customer:wallet:cus_0001", ledger.Liability, false)
	pending := openAccount(t, store, "liabilities:settlement:pending", ledger.Liability, false)

	// Fund the wallet: credit the wallet, debit retained earnings.
	equity := openAccount(t, store, "equity:retained", ledger.Equity, true)
	postTransaction(t, store, "opening_balance",
		entry(t, equity, ledger.Debit, 100_000),
		entry(t, wallet, ledger.Credit, 100_000),
	)

	posted := postTransaction(t, store, "order_4471",
		entry(t, wallet, ledger.Debit, 25_000),
		entry(t, pending, ledger.Credit, 25_000),
	)

	loaded, err := store.TransactionByID(ctx, posted.ID())
	require.NoError(t, err)

	assert.Equal(t, posted.ID(), loaded.ID())
	assert.Equal(t, "order_4471", loaded.Reference())
	assert.Equal(t, money.EUR, loaded.Currency())
	assert.Equal(t, 2, loaded.EntryCount())
	assert.False(t, loaded.IsReversal())
	assert.True(t, posted.PostedAt().Equal(loaded.PostedAt()))

	balance, err := store.Balance(ctx, wallet.ID())
	require.NoError(t, err)

	normal, err := balance.Normal()
	require.NoError(t, err)
	assert.Equal(t, int64(75_000), normal.Amount(), "wallet should hold 100000 - 25000")
}

// The journal is append-only, enforced by the database.
func TestJournalIsAppendOnly(t *testing.T) {
	store := newTestDB(t)
	ctx := t.Context()

	equity := openAccount(t, store, "equity:retained", ledger.Equity, true)
	wallet := openAccount(t, store, "liabilities:customer:wallet:cus_0001", ledger.Liability, false)
	tx := postTransaction(t, store, "opening_balance",
		entry(t, equity, ledger.Debit, 10_000),
		entry(t, wallet, ledger.Credit, 10_000),
	)

	pool := store.Pool()

	t.Run("update on transactions is rejected", func(t *testing.T) {
		_, err := pool.Exec(ctx, `UPDATE transactions SET reference = 'tampered' WHERE id = $1`, tx.ID().String())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "append-only")
	})

	t.Run("delete on transactions is rejected", func(t *testing.T) {
		_, err := pool.Exec(ctx, `DELETE FROM transactions WHERE id = $1`, tx.ID().String())
		require.Error(t, err)
	})

	t.Run("update on entries is rejected", func(t *testing.T) {
		_, err := pool.Exec(ctx, `UPDATE entries SET amount = 1 WHERE transaction_id = $1`, tx.ID().String())
		require.Error(t, err)
	})

	t.Run("delete on entries is rejected", func(t *testing.T) {
		_, err := pool.Exec(ctx, `DELETE FROM entries WHERE transaction_id = $1`, tx.ID().String())
		require.Error(t, err)
	})

	// TRUNCATE bypasses row-level triggers, so the statement-level trigger must catch it.
	t.Run("truncate is rejected", func(t *testing.T) {
		_, err := pool.Exec(ctx, `TRUNCATE entries`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "append-only")
	})

	// And the data is still intact.
	loaded, err := store.TransactionByID(ctx, tx.ID())
	require.NoError(t, err)
	assert.Equal(t, "opening_balance", loaded.Reference())
}

// An account that does not allow negative balances cannot be overdrawn.
func TestPostRejectsOverdraft(t *testing.T) {
	store := newTestDB(t)
	ctx := t.Context()

	equity := openAccount(t, store, "equity:retained", ledger.Equity, true)
	wallet := openAccount(t, store, "liabilities:customer:wallet:cus_0001", ledger.Liability, false)
	pending := openAccount(t, store, "liabilities:settlement:pending", ledger.Liability, false)

	postTransaction(t, store, "opening_balance",
		entry(t, equity, ledger.Debit, 10_000),
		entry(t, wallet, ledger.Credit, 10_000),
	)

	// Spending exactly the balance is allowed.
	tx, err := ledger.NewTransaction(ledger.TransactionParams{
		ID:       newTransactionID(t),
		Entries:  []ledger.Entry{entry(t, wallet, ledger.Debit, 10_000), entry(t, pending, ledger.Credit, 10_000)},
		PostedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NoError(t, store.Post(ctx, tx))

	// One cent more must be rejected.
	overdraft, err := ledger.NewTransaction(ledger.TransactionParams{
		ID:       newTransactionID(t),
		Entries:  []ledger.Entry{entry(t, wallet, ledger.Debit, 1), entry(t, pending, ledger.Credit, 1)},
		PostedAt: time.Now().UTC(),
	})
	require.NoError(t, err)

	err = store.Post(ctx, overdraft)
	require.ErrorIs(t, err, ledger.ErrInsufficientFunds)

	// Entire posting was rolled back.
	_, err = store.TransactionByID(ctx, overdraft.ID())
	assert.ErrorIs(t, err, ledger.ErrTransactionNotFound)

	balance, err := store.Balance(ctx, wallet.ID())
	require.NoError(t, err)
	normal, err := balance.Normal()
	require.NoError(t, err)
	assert.True(t, normal.IsZero(), "wallet should be exactly empty, got %v", normal)
}

// A closed account accepts no new entries.
func TestPostRejectsClosedAccount(t *testing.T) {
	store := newTestDB(t)
	ctx := t.Context()

	equity := openAccount(t, store, "equity:retained", ledger.Equity, true)
	wallet := openAccount(t, store, "liabilities:customer:wallet:cus_0001", ledger.Liability, false)

	require.NoError(t, store.CloseAccount(ctx, wallet.ID()))

	tx, err := ledger.NewTransaction(ledger.TransactionParams{
		ID:       newTransactionID(t),
		Entries:  []ledger.Entry{entry(t, equity, ledger.Debit, 100), entry(t, wallet, ledger.Credit, 100)},
		PostedAt: time.Now().UTC(),
	})
	require.NoError(t, err)

	err = store.Post(ctx, tx)
	assert.ErrorIs(t, err, ledger.ErrAccountClosed)
}

// An entry's currency must match its account's currency.
func TestPostRejectsCurrencyMismatchWithAccount(t *testing.T) {
	store := newTestDB(t)
	ctx := t.Context()

	eurCode, err := ledger.ParseAccountCode("equity:retained")
	require.NoError(t, err)
	eurAccount, err := ledger.NewAccount(ledger.AccountParams{
		ID: newAccountID(t), Code: eurCode, Name: "retained", Type: ledger.Equity, Currency: money.EUR, AllowNegative: true,
	})
	require.NoError(t, err)
	require.NoError(t, store.OpenAccount(ctx, eurAccount))

	usdCode, err := ledger.ParseAccountCode("liabilities:customer:wallet:cus_usd")
	require.NoError(t, err)
	usdAccount, err := ledger.NewAccount(ledger.AccountParams{
		ID: newAccountID(t), Code: usdCode, Name: "usd wallet", Type: ledger.Liability, Currency: money.USD, AllowNegative: true,
	})
	require.NoError(t, err)
	require.NoError(t, store.OpenAccount(ctx, usdAccount))

	// A balanced EUR transaction, but one account holds USD.
	debit, err := ledger.NewEntry(eurAccount.ID(), ledger.Debit, eur(t, 500))
	require.NoError(t, err)
	credit, err := ledger.NewEntry(usdAccount.ID(), ledger.Credit, eur(t, 500))
	require.NoError(t, err)

	tx, err := ledger.NewTransaction(ledger.TransactionParams{
		ID: newTransactionID(t), Entries: []ledger.Entry{debit, credit}, PostedAt: time.Now().UTC(),
	})
	require.NoError(t, err, "domain accepts this since both entries are EUR, but accounts differ")

	err = store.Post(ctx, tx)
	assert.ErrorIs(t, err, money.ErrCurrencyMismatch)
}

// A USD transaction must load back as USD (and not get defaulted to EUR).
func TestMultiCurrencyRoundTrip(t *testing.T) {
	store := newTestDB(t)
	ctx := t.Context()

	makeUSD := func(code string, accountType ledger.AccountType) ledger.Account {
		parsed, err := ledger.ParseAccountCode(code)
		require.NoError(t, err)
		account, err := ledger.NewAccount(ledger.AccountParams{
			ID: newAccountID(t), Code: parsed, Name: code, Type: accountType, Currency: money.USD, AllowNegative: true,
		})
		require.NoError(t, err)
		require.NoError(t, store.OpenAccount(ctx, account))
		return account
	}

	equity := makeUSD("equity:retained", ledger.Equity)
	wallet := makeUSD("liabilities:customer:wallet:cus_usd", ledger.Liability)

	amount, err := money.New(4_200, money.USD)
	require.NoError(t, err)
	debit, err := ledger.NewEntry(equity.ID(), ledger.Debit, amount)
	require.NoError(t, err)
	credit, err := ledger.NewEntry(wallet.ID(), ledger.Credit, amount)
	require.NoError(t, err)

	tx, err := ledger.NewTransaction(ledger.TransactionParams{
		ID: newTransactionID(t), Entries: []ledger.Entry{debit, credit}, PostedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
	require.NoError(t, store.Post(ctx, tx))

	loaded, err := store.TransactionByID(ctx, tx.ID())
	require.NoError(t, err)
	assert.Equal(t, money.USD, loaded.Currency(), "a USD transaction must not load back as EUR")
	for _, e := range loaded.Entries() {
		assert.Equal(t, money.USD, e.Currency())
	}
}

// A transaction is reversed at most once.
func TestReverseTwiceIsRejected(t *testing.T) {
	store := newTestDB(t)
	ctx := t.Context()

	equity := openAccount(t, store, "equity:retained", ledger.Equity, true)
	wallet := openAccount(t, store, "liabilities:customer:wallet:cus_0001", ledger.Liability, true)

	original := postTransaction(t, store, "mistake",
		entry(t, equity, ledger.Debit, 5_000),
		entry(t, wallet, ledger.Credit, 5_000),
	)

	first, err := ledger.NewReversal(original, newTransactionID(t), time.Now().UTC(), "posted in error")
	require.NoError(t, err)
	require.NoError(t, store.Post(ctx, first))

	second, err := ledger.NewReversal(original, newTransactionID(t), time.Now().UTC(), "posted in error again")
	require.NoError(t, err, "the domain cannot know the first reversal exists")

	err = store.Post(ctx, second)
	assert.ErrorIs(t, err, ledger.ErrAlreadyReversed)

	// Reversal left the balances where they started.
	balance, err := store.Balance(ctx, wallet.ID())
	require.NoError(t, err)
	normal, err := balance.Normal()
	require.NoError(t, err)
	assert.True(t, normal.IsZero(), "reversal should restore the balance, got %v", normal)
}

// Cached balances always equal the balance recomputed from the journal under concurrency.
func TestBalancesReconcileUnderConcurrency(t *testing.T) {
	store := newTestDB(t)
	ctx := t.Context()

	const (
		writers           = 16
		postingsPerWriter = 20
		openingBalance    = 1_000_000
		amountPerTransfer = 100
	)

	equity := openAccount(t, store, "equity:retained", ledger.Equity, true)
	pending := openAccount(t, store, "liabilities:settlement:pending", ledger.Liability, true)

	wallets := make([]ledger.Account, 4)
	for i := range wallets {
		wallets[i] = openAccount(t, store,
			fmt.Sprintf("liabilities:customer:wallet:cus_%04d", i),
			ledger.Liability, false)
		postTransaction(t, store, "opening_balance",
			entry(t, equity, ledger.Debit, openingBalance),
			entry(t, wallets[i], ledger.Credit, openingBalance),
		)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, writers*postingsPerWriter)

	for w := range writers {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			for i := range postingsPerWriter {
				wallet := wallets[(worker+i)%len(wallets)]

				tx, err := ledger.NewTransaction(ledger.TransactionParams{
					ID:        newTransactionID(t),
					Reference: fmt.Sprintf("w%d_i%d", worker, i),
					Entries: []ledger.Entry{
						entry(t, wallet, ledger.Debit, amountPerTransfer),
						entry(t, pending, ledger.Credit, amountPerTransfer),
					},
					PostedAt: time.Now().UTC(),
				})
				if err != nil {
					errCh <- err
					return
				}
				if err := store.Post(ctx, tx); err != nil {
					errCh <- fmt.Errorf("worker %d posting %d: %w", worker, i, err)
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent posting failed: %v", err)
	}
	if t.Failed() {
		t.FailNow()
	}

	allAccounts := append([]ledger.Account{equity, pending}, wallets...)
	for _, account := range allAccounts {
		cached, err := store.Balance(ctx, account.ID())
		require.NoError(t, err)

		authoritative, err := store.RecomputeBalance(ctx, account.ID())
		require.NoError(t, err)

		assert.Equal(t, authoritative.Debits().Amount(), cached.Debits().Amount(),
			"account %s: cached debits drifted from the journal", account.Code())
		assert.Equal(t, authoritative.Credits().Amount(), cached.Credits().Amount(),
			"account %s: cached credits drifted from the journal", account.Code())
	}

	var totalDebits, totalCredits int64
	err := store.Pool().QueryRow(ctx, `
		SELECT COALESCE(SUM(amount) FILTER (WHERE direction = 'DEBIT'),  0),
		       COALESCE(SUM(amount) FILTER (WHERE direction = 'CREDIT'), 0)
		FROM entries
	`).Scan(&totalDebits, &totalCredits)
	require.NoError(t, err)
	assert.Equal(t, totalDebits, totalCredits, "the ledger as a whole does not balance")
}

// Two transactions touching the same two accounts in opposite order must not deadlock.
func TestConcurrentOppositeOrderPostingsDoNotDeadlock(t *testing.T) {
	store := newTestDB(t)
	ctx := t.Context()

	equity := openAccount(t, store, "equity:retained", ledger.Equity, true)
	left := openAccount(t, store, "liabilities:customer:wallet:cus_left", ledger.Liability, true)
	right := openAccount(t, store, "liabilities:customer:wallet:cus_right", ledger.Liability, true)

	postTransaction(t, store, "fund_left", entry(t, equity, ledger.Debit, 1_000_000), entry(t, left, ledger.Credit, 1_000_000))
	postTransaction(t, store, "fund_right", entry(t, equity, ledger.Debit, 1_000_000), entry(t, right, ledger.Credit, 1_000_000))

	const rounds = 100

	move := func(from, to ledger.Account) error {
		tx, err := ledger.NewTransaction(ledger.TransactionParams{
			ID:       newTransactionID(t),
			Entries:  []ledger.Entry{entry(t, from, ledger.Debit, 1), entry(t, to, ledger.Credit, 1)},
			PostedAt: time.Now().UTC(),
		})
		if err != nil {
			return err
		}
		return store.Post(ctx, tx)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2*rounds)

	wg.Add(2)
	go func() {
		defer wg.Done()
		for range rounds {
			if err := move(left, right); err != nil {
				errCh <- fmt.Errorf("left->right: %w", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range rounds {
			if err := move(right, left); err != nil {
				errCh <- fmt.Errorf("right->left: %w", err)
				return
			}
		}
	}()

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("%v", err)
		var deadlock interface{ SQLState() string }
		if errors.As(err, &deadlock) && deadlock.SQLState() == "40P01" {
			t.Error("deadlock detected: check the ORDER BY in lockBalancesSQL")
		}
	}
}

func TestPostRejectsUnknownAccount(t *testing.T) {
	store := newTestDB(t)

	equity := openAccount(t, store, "equity:retained", ledger.Equity, true)
	ghost := newAccountID(t) // never opened

	ghostEntry, err := ledger.NewEntry(ghost, ledger.Credit, eur(t, 100))
	require.NoError(t, err)

	tx, err := ledger.NewTransaction(ledger.TransactionParams{
		ID:       newTransactionID(t),
		Entries:  []ledger.Entry{entry(t, equity, ledger.Debit, 100), ghostEntry},
		PostedAt: time.Now().UTC(),
	})
	require.NoError(t, err)

	err = store.Post(context.Background(), tx)
	assert.ErrorIs(t, err, ledger.ErrAccountNotFound)
}
