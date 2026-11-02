package integration

import (
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ViseonDev/ledger-core/internal/adapters/postgres"
	"github.com/ViseonDev/ledger-core/internal/adapters/psp"
	"github.com/ViseonDev/ledger-core/internal/adapters/uuidgen"
	"github.com/ViseonDev/ledger-core/internal/app"
	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
	"github.com/ViseonDev/ledger-core/internal/platform/clock"
)

type sagaHarness struct {
	store        *postgres.Store
	orchestrator *app.Orchestrator
	worker       *app.Worker
	processor    *psp.Simulator
	accounts     map[string]ledger.Account
}

// newSagaHarness builds a test saga stack with required accounts and an initial customer balance.
func newSagaHarness(t *testing.T, walletBalance int64) *sagaHarness {
	t.Helper()

	store := newTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	processor := psp.NewSimulator()

	orchestrator := app.NewOrchestrator(store, processor, uuidgen.New(), clock.New(), logger)
	worker := app.NewWorker(store, orchestrator, nil, logger, "test-worker")

	accounts := map[string]ledger.Account{
		"equity":  openAccount(t, store, "equity:retained", ledger.Equity, true),
		"wallet":  openAccount(t, store, "liabilities:customer:wallet:cus_demo_0001", ledger.Liability, false),
		"pending": openAccount(t, store, "liabilities:settlement:pending", ledger.Liability, false),
		"payable": openAccount(t, store, "liabilities:merchant:payable:mer_demo_01", ledger.Liability, false),
		"revenue": openAccount(t, store, "revenue:fees:processing", ledger.Revenue, true),
	}

	postTransaction(t, store, "opening_balance",
		entry(t, accounts["equity"], ledger.Debit, walletBalance),
		entry(t, accounts["wallet"], ledger.Credit, walletBalance),
	)

	return &sagaHarness{
		store:        store,
		orchestrator: orchestrator,
		worker:       worker,
		processor:    processor,
		accounts:     accounts,
	}
}

// start creates a payment intent and returns its identifier.
func (h *sagaHarness) start(t *testing.T, key string, amount, fee int64) string {
	t.Helper()

	outcome, err := h.orchestrator.StartPayment(t.Context(), postgres.IdempotentRequest{
		Key:      key,
		Endpoint: "POST /v1/payments",
		Body:     []byte(`{"amount":` + itoa(amount) + `}`),
	}, app.StartPaymentCommand{
		Reference:   key,
		CustomerRef: "cus_demo_0001",
		MerchantRef: "mer_demo_01",
		Amount:      amount,
		Fee:         fee,
		Currency:    "EUR",
	})
	require.NoError(t, err)
	return outcome.Value.ID
}

// drive advances the worker until the payment reaches a terminal state.
func (h *sagaHarness) drive(t *testing.T, paymentID string, maxTicks int) app.PaymentView {
	t.Helper()

	for range maxTicks {
		view, err := h.orchestrator.PaymentByID(t.Context(), paymentID)
		require.NoError(t, err)

		switch view.Status {
		case "SETTLED", "COMPENSATED", "COMPENSATION_FAILED":
			return view
		}

		if _, tickErr := h.worker.Tick(t.Context()); tickErr != nil {
			t.Fatalf("worker tick: %v", tickErr)
		}
	}

	view, err := h.orchestrator.PaymentByID(t.Context(), paymentID)
	require.NoError(t, err)
	t.Fatalf("payment did not reach a terminal state within %d ticks; status is %s, steps %+v",
		maxTicks, view.Status, view.Steps)
	return view
}

func (h *sagaHarness) balance(t *testing.T, name string) int64 {
	t.Helper()

	balance, err := h.store.Balance(t.Context(), h.accounts[name].ID())
	require.NoError(t, err)

	normal, err := balance.Normal()
	require.NoError(t, err)
	return normal.Amount()
}

func itoa(v int64) string {
	return fmt.Sprintf("%d", v)
}

func TestSagaHappyPath(t *testing.T) {
	h := newSagaHarness(t, 100_000)

	paymentID := h.start(t, "pay_happy", 25_000, 750)
	view := h.drive(t, paymentID, 12)

	assert.Equal(t, "SETTLED", view.Status)
	for _, step := range view.Steps {
		assert.Equal(t, "DONE", step.Status, "step %s did not complete", step.Name)
	}

	// Customer paid 25000; merchant gets 24250; fee revenue is 750.
	assert.Equal(t, int64(75_000), h.balance(t, "wallet"))
	assert.Equal(t, int64(24_250), h.balance(t, "payable"))
	assert.Equal(t, int64(750), h.balance(t, "revenue"))
	assert.Equal(t, int64(0), h.balance(t, "pending"), "pending settlement should be empty after settling")
}

// A saga that reaches COMPENSATED leaves every balance exactly as it was before the payment started.
func TestSagaCompensationRestoresBalances(t *testing.T) {
	failures := []struct {
		name string
		step string
		err  error
	}{
		{name: "authorize declined", step: "authorize", err: psp.ErrDeclined},
		{name: "capture declined", step: "capture", err: psp.ErrDeclined},
	}

	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			h := newSagaHarness(t, 100_000)

			before := map[string]int64{
				"wallet":  h.balance(t, "wallet"),
				"pending": h.balance(t, "pending"),
				"payable": h.balance(t, "payable"),
				"revenue": h.balance(t, "revenue"),
			}

			// Fail this step repeatedly so retries exhaust and saga unwinds.
			for range 8 {
				h.processor.FailStep(tt.step, tt.err)
			}

			paymentID := h.start(t, "pay_"+tt.step, 25_000, 750)
			view := h.drive(t, paymentID, 40)

			assert.Equal(t, "COMPENSATED", view.Status,
				"steps: %+v, last error: %s", view.Steps, view.LastError)

			for account, want := range before {
				assert.Equal(t, want, h.balance(t, account),
					"account %s was not restored after compensation", account)
			}

			// Verify journal recorded compensating reversals, not row deletions.
			var reversals int
			err := h.store.Pool().QueryRow(t.Context(),
				`SELECT count(*) FROM transactions WHERE reversal_of IS NOT NULL`).Scan(&reversals)
			require.NoError(t, err)
			assert.Positive(t, reversals, "compensation must post reversals, not delete rows")
		})
	}
}

// A decline on the initial local step means nothing moved, so nothing needs unwinding.
func TestSagaInsufficientFundsCompensatesImmediately(t *testing.T) {
	h := newSagaHarness(t, 1_000) // not enough for 25000 payment

	paymentID := h.start(t, "pay_broke", 25_000, 750)
	view := h.drive(t, paymentID, 12)

	assert.Equal(t, "COMPENSATED", view.Status)
	assert.Equal(t, "FAILED", view.Steps[0].Status)
	assert.Contains(t, view.LastError, "insufficient funds")

	// Insufficient funds is terminal and should not retry.
	assert.Equal(t, 1, view.Steps[0].Attempts,
		"insufficient funds should fail once, not retry six times")

	assert.Equal(t, int64(1_000), h.balance(t, "wallet"))
	assert.Equal(t, int64(0), h.balance(t, "pending"))
}

// Transient processor failures must be retried, not compensated.
func TestSagaRetriesTransientFailures(t *testing.T) {
	h := newSagaHarness(t, 100_000)

	// One transient failure, clears after first trigger.
	h.processor.FailStep("authorize", psp.ErrUnavailable)

	paymentID := h.start(t, "pay_flaky", 25_000, 750)

	view := h.driveThroughBackoff(t, paymentID, 20)

	assert.Equal(t, "SETTLED", view.Status, "a transient failure must not unwind the payment")
	assert.Equal(t, int64(75_000), h.balance(t, "wallet"))
}

// An ambiguous outcome (timeout) is retried with the same idempotency key rather than compensated.
func TestSagaTreatsTimeoutAsRetryableNotFailure(t *testing.T) {
	h := newSagaHarness(t, 100_000)

	h.processor.FailStep("authorize", psp.ErrAmbiguous)

	paymentID := h.start(t, "pay_timeout", 25_000, 750)
	view := h.driveThroughBackoff(t, paymentID, 20)

	assert.Equal(t, "SETTLED", view.Status,
		"a timeout is an unknown outcome, not a failure; compensating here would cause double-charge bugs")
	assert.Empty(t, h.processor.Voided, "nothing should have been voided")
	assert.Empty(t, h.processor.Refunded, "nothing should have been refunded")
}

func TestSagaIsIdempotentOnStart(t *testing.T) {
	h := newSagaHarness(t, 100_000)

	first := h.start(t, "pay_once", 25_000, 750)
	second := h.start(t, "pay_once", 25_000, 750)

	assert.Equal(t, first, second, "the same key must return the same payment")

	var count int
	err := h.store.Pool().QueryRow(t.Context(), `SELECT count(*) FROM payment_intents`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// A worker that terminates mid-payment must not strand it: the lease expires and another worker resumes.
func TestSagaSurvivesWorkerRestart(t *testing.T) {
	h := newSagaHarness(t, 100_000)

	paymentID := h.start(t, "pay_restart", 25_000, 750)

	// Advance two steps, then discard the worker.
	for range 2 {
		_, tickErr := h.worker.Tick(t.Context())
		require.NoError(t, tickErr)
	}

	partial, err := h.orchestrator.PaymentByID(t.Context(), paymentID)
	require.NoError(t, err)
	require.Equal(t, "IN_PROGRESS", partial.Status)

	// Make the payment immediately claimable again, simulating lease expiration.
	_, err = h.store.Pool().Exec(t.Context(),
		`UPDATE payment_intents SET next_attempt_at = now() WHERE id = $1`, paymentID)
	require.NoError(t, err)

	// A new worker over the same store finishes the workflow.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	replacement := app.NewWorker(h.store, h.orchestrator, nil, logger, "replacement")

	for range 12 {
		view, viewErr := h.orchestrator.PaymentByID(t.Context(), paymentID)
		require.NoError(t, viewErr)
		if view.Status == "SETTLED" {
			break
		}
		_, tickErr := replacement.Tick(t.Context())
		require.NoError(t, tickErr)
		_, execErr := h.store.Pool().Exec(t.Context(),
			`UPDATE payment_intents SET next_attempt_at = now() WHERE id = $1`, paymentID)
		require.NoError(t, execErr)
	}

	final, err := h.orchestrator.PaymentByID(t.Context(), paymentID)
	require.NoError(t, err)
	assert.Equal(t, "SETTLED", final.Status)
	assert.Equal(t, int64(75_000), h.balance(t, "wallet"))
}

// driveThroughBackoff advances payments past scheduled retry backoffs in tests.
func (h *sagaHarness) driveThroughBackoff(t *testing.T, paymentID string, maxTicks int) app.PaymentView {
	t.Helper()

	for range maxTicks {
		view, err := h.orchestrator.PaymentByID(t.Context(), paymentID)
		require.NoError(t, err)

		switch view.Status {
		case "SETTLED", "COMPENSATED", "COMPENSATION_FAILED":
			return view
		}

		_, updateErr := h.store.Pool().Exec(t.Context(),
			`UPDATE payment_intents SET next_attempt_at = now() WHERE id = $1`, paymentID)
		require.NoError(t, updateErr)

		if _, tickErr := h.worker.Tick(t.Context()); tickErr != nil {
			t.Fatalf("worker tick: %v", tickErr)
		}
	}

	view, err := h.orchestrator.PaymentByID(t.Context(), paymentID)
	require.NoError(t, err)
	t.Fatalf("payment stuck at %s after %d ticks: %+v", view.Status, maxTicks, view.Steps)
	return view
}
