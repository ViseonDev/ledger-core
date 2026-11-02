package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ViseonDev/ledger-core/internal/domain/money"
	"github.com/ViseonDev/ledger-core/internal/domain/payment"
)

// ErrNoPaymentDue indicates that no payments are currently due for claiming.
var ErrNoPaymentDue = errors.New("postgres: no payment due")

const insertPaymentSQL = `
INSERT INTO payment_intents
    (id, reference, customer_ref, merchant_ref, amount, fee_amount, currency, status, next_attempt_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
`

const insertPaymentStepSQL = `
INSERT INTO payment_steps (payment_id, seq, name, status)
VALUES ($1, $2, $3, $4)
`

const selectPaymentSQL = `
SELECT id, reference, customer_ref, merchant_ref, amount, fee_amount, currency,
       status, attempts, next_attempt_at, COALESCE(last_error, '')
FROM payment_intents
WHERE id = $1
`

const selectPaymentStepsSQL = `
SELECT seq, name, status, attempts, COALESCE(external_ref, ''), COALESCE(last_error, '')
FROM payment_steps
WHERE payment_id = $1
ORDER BY seq
`

// claimDuePaymentSQL locks and leases one due payment using FOR UPDATE SKIP LOCKED.
const claimDuePaymentSQL = `
UPDATE payment_intents
SET next_attempt_at = now() + $1::interval,
    updated_at      = now()
WHERE id = (
    SELECT id
    FROM payment_intents
    WHERE status IN ('PENDING', 'IN_PROGRESS', 'COMPENSATING')
      AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING id
`

const updatePaymentSQL = `
UPDATE payment_intents
SET status          = $2,
    attempts        = $3,
    next_attempt_at = $4,
    last_error      = NULLIF($5, ''),
    updated_at      = now()
WHERE id = $1
`

const updatePaymentStepSQL = `
UPDATE payment_steps
SET status       = $3,
    attempts     = $4,
    external_ref = NULLIF($5, ''),
    last_error   = NULLIF($6, ''),
    updated_at   = now()
WHERE payment_id = $1 AND seq = $2
`

const setStepTransactionSQL = `
UPDATE payment_steps
SET transaction_id = $3, updated_at = now()
WHERE payment_id = $1 AND seq = $2
`

const setStepCompensationSQL = `
UPDATE payment_steps
SET compensation_transaction_id = $3, updated_at = now()
WHERE payment_id = $1 AND seq = $2
`

// InsertPayment writes a new payment and each of its initial steps.
func InsertPayment(ctx context.Context, db DBTX, intent *payment.Intent) error {
	_, err := db.Exec(ctx, insertPaymentSQL,
		intent.ID().String(),
		intent.Reference(),
		intent.CustomerRef(),
		intent.MerchantRef(),
		intent.Amount().Amount(),
		intent.Fee().Amount(),
		intent.Amount().Currency().Code(),
		intent.Status().String(),
		intent.NextAttemptAt(),
	)
	if err != nil {
		return fmt.Errorf("inserting payment %s: %w", intent.ID(), translateError(err))
	}

	batch := &pgx.Batch{}
	for _, step := range intent.Steps() {
		batch.Queue(insertPaymentStepSQL, intent.ID().String(), step.Seq, step.Name, step.Status.String())
	}

	results := db.SendBatch(ctx, batch)
	execErr := drainBatch(results, batch.Len())
	closeErr := results.Close()

	if execErr != nil {
		return fmt.Errorf("inserting payment steps: %w", translateError(execErr))
	}
	if closeErr != nil {
		return fmt.Errorf("closing batch: %w", translateError(closeErr))
	}
	return nil
}

// LoadPayment reads a payment intent and all its steps.
func LoadPayment(ctx context.Context, db DBTX, id payment.ID) (*payment.Intent, error) {
	var (
		paymentID   string
		reference   string
		customerRef string
		merchantRef string
		amount      int64
		fee         int64
		currency    string
		status      string
		attempts    int
		nextAttempt time.Time
		lastError   string
	)

	err := db.QueryRow(ctx, selectPaymentSQL, id.String()).Scan(
		&paymentID, &reference, &customerRef, &merchantRef,
		&amount, &fee, &currency, &status, &attempts, &nextAttempt, &lastError,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("payment %s: %w", id, pgx.ErrNoRows)
	}
	if err != nil {
		return nil, fmt.Errorf("loading payment %s: %w", id, translateError(err))
	}

	parsedCurrency, err := money.ParseCurrency(currency)
	if err != nil {
		return nil, err
	}
	amountMoney, err := money.New(amount, parsedCurrency)
	if err != nil {
		return nil, err
	}
	feeMoney, err := money.New(fee, parsedCurrency)
	if err != nil {
		return nil, err
	}
	parsedStatus, err := payment.ParseStatus(status)
	if err != nil {
		return nil, err
	}

	steps, err := loadPaymentSteps(ctx, db, id)
	if err != nil {
		return nil, err
	}

	return payment.Rehydrate(payment.Params{
		ID:          payment.ID(paymentID),
		Reference:   reference,
		CustomerRef: customerRef,
		MerchantRef: merchantRef,
		Amount:      amountMoney,
		Fee:         feeMoney,
		Now:         nextAttempt,
	}, parsedStatus, attempts, nextAttempt, lastError, steps)
}

func loadPaymentSteps(ctx context.Context, db DBTX, id payment.ID) ([]payment.StepState, error) {
	rows, err := db.Query(ctx, selectPaymentStepsSQL, id.String())
	if err != nil {
		return nil, fmt.Errorf("loading steps for %s: %w", id, translateError(err))
	}

	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (payment.StepState, error) {
		var (
			seq         int
			name        string
			status      string
			attempts    int
			externalRef string
			lastError   string
		)
		if scanErr := row.Scan(&seq, &name, &status, &attempts, &externalRef, &lastError); scanErr != nil {
			return payment.StepState{}, scanErr
		}
		parsed, parseErr := payment.ParseStepStatus(status)
		if parseErr != nil {
			return payment.StepState{}, parseErr
		}
		return payment.StepState{
			Seq: seq, Name: name, Status: parsed,
			Attempts: attempts, ExternalRef: externalRef, LastError: lastError,
		}, nil
	})
}

// ClaimDuePayment leases one claimable payment by pushing its next_attempt_at into the future.
func (s *Store) ClaimDuePayment(ctx context.Context, lease time.Duration) (payment.ID, error) {
	var id string

	// Pass lease as interval string and cast in SQL for pgx compatibility.
	interval := fmt.Sprintf("%d milliseconds", lease.Milliseconds())

	err := s.pool.QueryRow(ctx, claimDuePaymentSQL, interval).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoPaymentDue
	}
	if err != nil {
		return "", fmt.Errorf("claiming payment: %w", translateError(err))
	}
	return payment.ID(id), nil
}

// SavePayment updates the payment intent and each step's latest status and error.
func SavePayment(ctx context.Context, db DBTX, intent *payment.Intent) error {
	batch := &pgx.Batch{}

	batch.Queue(updatePaymentSQL,
		intent.ID().String(),
		intent.Status().String(),
		intent.Attempts(),
		intent.NextAttemptAt(),
		intent.LastError(),
	)
	for _, step := range intent.Steps() {
		batch.Queue(updatePaymentStepSQL,
			intent.ID().String(), step.Seq, step.Status.String(),
			step.Attempts, step.ExternalRef, step.LastError,
		)
	}

	results := db.SendBatch(ctx, batch)
	execErr := drainBatch(results, batch.Len())
	closeErr := results.Close()

	if execErr != nil {
		return fmt.Errorf("saving payment %s: %w", intent.ID(), translateError(execErr))
	}
	if closeErr != nil {
		return fmt.Errorf("closing batch: %w", translateError(closeErr))
	}
	return nil
}

// LinkStepTransaction connects a forward step to the ledger transaction it posted.
func LinkStepTransaction(ctx context.Context, db DBTX, id payment.ID, seq int, transactionID string) error {
	_, err := db.Exec(ctx, setStepTransactionSQL, id.String(), seq, transactionID)
	if err != nil {
		return fmt.Errorf("linking step transaction: %w", translateError(err))
	}
	return nil
}

// LinkStepCompensation connects an undone step to its reversing transaction.
func LinkStepCompensation(ctx context.Context, db DBTX, id payment.ID, seq int, transactionID string) error {
	_, err := db.Exec(ctx, setStepCompensationSQL, id.String(), seq, transactionID)
	if err != nil {
		return fmt.Errorf("linking step compensation: %w", translateError(err))
	}
	return nil
}
