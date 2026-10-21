package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ViseonDev/ledger-core/internal/adapters/postgres"
	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
	"github.com/ViseonDev/ledger-core/internal/ports"
)

// postResult is what the work function returns and what gets stored as the idempotency response.
type postResult struct {
	TransactionID string `json:"transaction_id"`
	Amount        int64  `json:"amount"`
}

// idempotentTransfer moves an amount between two accounts under an idempotency key.
func idempotentTransfer(
	t *testing.T,
	store *ledgerStore,
	key string,
	body []byte,
	from, to ledger.Account,
	amount int64,
) (postgres.Outcome[postResult], error) {
	t.Helper()

	return postgres.Execute(t.Context(), store, postgres.IdempotentRequest{
		Key:      key,
		Endpoint: "POST /v1/transactions",
		Body:     body,
	}, func(ctx context.Context, db postgres.DBTX) (postResult, error) {
		tx, txErr := ledger.NewTransaction(ledger.TransactionParams{
			ID:        newTransactionID(t),
			Reference: key,
			Entries:   []ledger.Entry{entry(t, from, ledger.Debit, amount), entry(t, to, ledger.Credit, amount)},
			PostedAt:  time.Now().UTC(),
		})
		if txErr != nil {
			return postResult{}, txErr
		}
		if postErr := postgres.PostTransaction(ctx, db, tx); postErr != nil {
			return postResult{}, postErr
		}
		return postResult{TransactionID: tx.ID().String(), Amount: amount}, nil
	})
}

func fundedPair(t *testing.T, store *ledgerStore) (wallet, pending ledger.Account) {
	t.Helper()

	equity := openAccount(t, store, "equity:retained", ledger.Equity, true)
	wallet = openAccount(t, store, "liabilities:customer:wallet:cus_0001", ledger.Liability, false)
	pending = openAccount(t, store, "liabilities:settlement:pending", ledger.Liability, true)

	postTransaction(t, store, "opening_balance",
		entry(t, equity, ledger.Debit, 1_000_000),
		entry(t, wallet, ledger.Credit, 1_000_000),
	)
	return wallet, pending
}

func countTransactions(t *testing.T, store *ledgerStore, reference string) int {
	t.Helper()

	var count int
	err := store.Pool().QueryRow(t.Context(),
		`SELECT count(*) FROM transactions WHERE reference = $1`, reference).Scan(&count)
	require.NoError(t, err)
	return count
}

func TestIdempotentPostReplaysOriginal(t *testing.T) {
	store := newTestDB(t)
	wallet, pending := fundedPair(t, store)

	body := []byte(`{"amount":25000,"currency":"EUR"}`)

	first, err := idempotentTransfer(t, store, "key_alpha", body, wallet, pending, 25_000)
	require.NoError(t, err)
	assert.False(t, first.Replayed, "the first call did the work")

	second, err := idempotentTransfer(t, store, "key_alpha", body, wallet, pending, 25_000)
	require.NoError(t, err)
	assert.True(t, second.Replayed, "the second call replayed")

	// Same result, and exactly one transaction exists in storage.
	assert.Equal(t, first.Value.TransactionID, second.Value.TransactionID)
	assert.Equal(t, 1, countTransactions(t, store, "key_alpha"))

	balance, err := store.Balance(t.Context(), wallet.ID())
	require.NoError(t, err)
	normal, err := balance.Normal()
	require.NoError(t, err)
	assert.Equal(t, int64(975_000), normal.Amount(), "the wallet was debited exactly once")
}

// Many identical requests arriving at the same instant must move the money exactly once.
func TestIdempotentPostConcurrentDuplicates(t *testing.T) {
	store := newTestDB(t)
	wallet, pending := fundedPair(t, store)

	const callers = 24
	body := []byte(`{"amount":25000,"currency":"EUR"}`)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		results  []postgres.Outcome[postResult]
		failures []error
		// A barrier so every goroutine attempts the claim at the same instant.
		start = make(chan struct{})
	)

	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			outcome, err := idempotentTransfer(t, store, "key_race", body, wallet, pending, 25_000)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, err)
				return
			}
			results = append(results, outcome)
		}()
	}

	close(start)
	wg.Wait()

	for _, err := range failures {
		t.Errorf("concurrent idempotent call failed: %v", err)
	}
	require.Len(t, results, callers)

	// Exactly one caller did the work; the rest replayed.
	fresh := 0
	for _, outcome := range results {
		if !outcome.Replayed {
			fresh++
		}
	}
	assert.Equal(t, 1, fresh, "exactly one caller should have done the work")

	// Every caller got the exact same transaction id.
	for _, outcome := range results {
		assert.Equal(t, results[0].Value.TransactionID, outcome.Value.TransactionID)
	}

	// And the ledger moved the money once.
	assert.Equal(t, 1, countTransactions(t, store, "key_race"))

	balance, err := store.Balance(t.Context(), wallet.ID())
	require.NoError(t, err)
	normal, err := balance.Normal()
	require.NoError(t, err)
	assert.Equal(t, int64(975_000), normal.Amount(),
		"%d concurrent identical requests debited the wallet more than once", callers)
}

func TestIdempotentPostFingerprintConflict(t *testing.T) {
	store := newTestDB(t)
	wallet, pending := fundedPair(t, store)

	_, err := idempotentTransfer(t, store, "key_beta", []byte(`{"amount":25000}`), wallet, pending, 25_000)
	require.NoError(t, err)

	// Same key, different body: must be rejected as a conflict.
	_, err = idempotentTransfer(t, store, "key_beta", []byte(`{"amount":50000}`), wallet, pending, 50_000)
	assert.ErrorIs(t, err, ports.ErrIdempotencyConflict)

	assert.Equal(t, 1, countTransactions(t, store, "key_beta"))
}

func TestIdempotencyIsScopedToEndpoint(t *testing.T) {
	store := newTestDB(t)
	ctx := t.Context()

	body := []byte(`{"amount":100}`)

	first, err := postgres.Execute(ctx, store, postgres.IdempotentRequest{
		Key: "shared_key", Endpoint: "POST /v1/transactions", Body: body,
	}, func(ctx context.Context, db postgres.DBTX) (string, error) {
		return "transactions", nil
	})
	require.NoError(t, err)
	assert.False(t, first.Replayed)

	// The same key against a different endpoint is a different operation.
	second, err := postgres.Execute(ctx, store, postgres.IdempotentRequest{
		Key: "shared_key", Endpoint: "POST /v1/payments", Body: body,
	}, func(ctx context.Context, db postgres.DBTX) (string, error) {
		return "payments", nil
	})
	require.NoError(t, err)
	assert.False(t, second.Replayed, "a different endpoint must not replay")
	assert.Equal(t, "payments", second.Value)
}

// A failed attempt must not burn the key so client retries can succeed.
func TestFailedWorkReleasesTheKey(t *testing.T) {
	store := newTestDB(t)
	wallet, pending := fundedPair(t, store)
	ctx := t.Context()

	body := []byte(`{"amount":25000}`)
	boom := fmt.Errorf("simulated downstream failure")

	_, err := postgres.Execute(ctx, store, postgres.IdempotentRequest{
		Key: "key_retry", Endpoint: "POST /v1/transactions", Body: body,
	}, func(ctx context.Context, db postgres.DBTX) (postResult, error) {
		// Do real work first, then fail: both must roll back together.
		tx, txErr := ledger.NewTransaction(ledger.TransactionParams{
			ID:        newTransactionID(t),
			Reference: "key_retry",
			Entries:   []ledger.Entry{entry(t, wallet, ledger.Debit, 25_000), entry(t, pending, ledger.Credit, 25_000)},
			PostedAt:  time.Now().UTC(),
		})
		require.NoError(t, txErr)
		require.NoError(t, postgres.PostTransaction(ctx, db, tx))
		return postResult{}, boom
	})
	require.ErrorIs(t, err, boom)

	// The posting rolled back with the claim.
	assert.Equal(t, 0, countTransactions(t, store, "key_retry"))

	// And the same key works on retry.
	retry, err := idempotentTransfer(t, store, "key_retry", body, wallet, pending, 25_000)
	require.NoError(t, err)
	assert.False(t, retry.Replayed, "the key was released, so this is fresh work")
	assert.Equal(t, 1, countTransactions(t, store, "key_retry"))
}

func TestIdempotencyRejectsMissingKey(t *testing.T) {
	store := newTestDB(t)

	_, err := postgres.Execute(t.Context(), store, postgres.IdempotentRequest{
		Key: "  ", Endpoint: "POST /v1/transactions", Body: []byte(`{}`),
	}, func(ctx context.Context, db postgres.DBTX) (string, error) {
		t.Fatal("work must not run without a key")
		return "", nil
	})
	assert.ErrorIs(t, err, ports.ErrIdempotencyKeyRequired)
}

func TestPurgeIdempotencyRecords(t *testing.T) {
	store := newTestDB(t)
	wallet, pending := fundedPair(t, store)
	ctx := t.Context()

	_, err := idempotentTransfer(t, store, "key_old", []byte(`{"amount":100}`), wallet, pending, 100)
	require.NoError(t, err)

	// Nothing is old enough yet.
	removed, err := store.PurgeIdempotencyRecords(ctx, time.Now().Add(-24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(0), removed)

	// Everything is old enough now.
	removed, err = store.PurgeIdempotencyRecords(ctx, time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(1), removed)

	// After purge the key is free again.
	fresh, err := idempotentTransfer(t, store, "key_old", []byte(`{"amount":100}`), wallet, pending, 100)
	require.NoError(t, err)
	assert.False(t, fresh.Replayed)
}
