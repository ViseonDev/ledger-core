package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ViseonDev/ledger-core/internal/adapters/postgres"
	"github.com/ViseonDev/ledger-core/internal/adapters/psp"
	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
	"github.com/ViseonDev/ledger-core/internal/domain/money"
	"github.com/ViseonDev/ledger-core/internal/domain/payment"
	"github.com/ViseonDev/ledger-core/internal/ports"
)

// maxStepAttempts bounds retries per step before giving up and unwinding.
const maxStepAttempts = 6

// Orchestrator coordinates payment saga execution and compensation.
type Orchestrator struct {
	store     *postgres.Store
	processor psp.Processor
	ids       ports.IDGenerator
	clock     ports.Clock
	logger    *slog.Logger
}

// NewOrchestrator creates a new payment saga orchestrator.
func NewOrchestrator(store *postgres.Store, processor psp.Processor, ids ports.IDGenerator, clk ports.Clock, logger *slog.Logger) *Orchestrator {
	return &Orchestrator{store: store, processor: processor, ids: ids, clock: clk, logger: logger}
}

// StartPaymentCommand holds input for starting a payment.
type StartPaymentCommand struct {
	Reference   string `json:"reference"`
	CustomerRef string `json:"customer_ref"`
	MerchantRef string `json:"merchant_ref"`
	Amount      int64  `json:"amount"`
	Fee         int64  `json:"fee"`
	Currency    string `json:"currency"`
}

// PaymentStepView represents a step in API responses.
type PaymentStepView struct {
	Seq       int    `json:"seq"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"last_error,omitempty"`
}

// PaymentView represents payment status and steps in API responses.
type PaymentView struct {
	ID          string            `json:"id"`
	Reference   string            `json:"reference"`
	CustomerRef string            `json:"customer_ref"`
	MerchantRef string            `json:"merchant_ref"`
	Amount      int64             `json:"amount"`
	Fee         int64             `json:"fee"`
	Currency    string            `json:"currency"`
	Status      string            `json:"status"`
	Steps       []PaymentStepView `json:"steps"`
	LastError   string            `json:"last_error,omitempty"`
}

// StartPayment initializes a payment intent idempotently. The background worker picks it up from here.
func (o *Orchestrator) StartPayment(ctx context.Context, idem postgres.IdempotentRequest, cmd StartPaymentCommand) (postgres.Outcome[PaymentView], error) {
	return postgres.Execute(ctx, o.store, idem, func(ctx context.Context, db postgres.DBTX) (PaymentView, error) {
		currency, err := money.ParseCurrency(cmd.Currency)
		if err != nil {
			return PaymentView{}, err
		}
		amount, err := money.New(cmd.Amount, currency)
		if err != nil {
			return PaymentView{}, err
		}
		fee, err := money.New(cmd.Fee, currency)
		if err != nil {
			return PaymentView{}, err
		}
		id, err := o.ids.NewTransactionID()
		if err != nil {
			return PaymentView{}, err
		}

		intent, err := payment.New(payment.Params{
			ID:          payment.ID(id.String()),
			Reference:   cmd.Reference,
			CustomerRef: cmd.CustomerRef,
			MerchantRef: cmd.MerchantRef,
			Amount:      amount,
			Fee:         fee,
			Now:         o.clock.Now(),
		})
		if err != nil {
			return PaymentView{}, err
		}
		if insertErr := postgres.InsertPayment(ctx, db, intent); insertErr != nil {
			return PaymentView{}, insertErr
		}

		o.logger.InfoContext(ctx, "payment started",
			slog.String("payment_id", intent.ID().String()),
			slog.String("reference", intent.Reference()),
			slog.Int64("amount", intent.Amount().Amount()),
		)
		return paymentView(intent), nil
	})
}

// PaymentByID fetches the latest status of a payment by ID.
func (o *Orchestrator) PaymentByID(ctx context.Context, id string) (PaymentView, error) {
	intent, err := postgres.LoadPayment(ctx, o.store.Pool(), payment.ID(id))
	if err != nil {
		return PaymentView{}, err
	}
	return paymentView(intent), nil
}

// Advance runs one step forward or backward for a payment.
func (o *Orchestrator) Advance(ctx context.Context, id payment.ID) error {
	intent, err := postgres.LoadPayment(ctx, o.store.Pool(), id)
	if err != nil {
		return err
	}
	if intent.Status().IsTerminal() {
		return nil
	}

	if intent.Status() == payment.Compensating {
		return o.compensateOne(ctx, intent)
	}
	return o.executeOne(ctx, intent)
}

func (o *Orchestrator) executeOne(ctx context.Context, intent *payment.Intent) error {
	if err := intent.Begin(); err != nil {
		return err
	}
	step, ok := intent.NextStep()
	if !ok {
		return payment.ErrNoStepsRemaining
	}

	externalRef, err := o.runStep(ctx, intent, step)
	if err != nil {
		return o.handleStepFailure(ctx, intent, step, err, "execute")
	}

	if err := intent.CompleteStep(step.Seq, externalRef); err != nil {
		return err
	}
	intent.Reschedule(o.clock.Now())

	o.logger.InfoContext(ctx, "payment step completed",
		slog.String("payment_id", intent.ID().String()),
		slog.String("step", step.Name),
		slog.String("status", intent.Status().String()),
	)
	return o.store.InTx(ctx, func(ctx context.Context, db postgres.DBTX) error {
		return postgres.SavePayment(ctx, db, intent)
	})
}

func (o *Orchestrator) compensateOne(ctx context.Context, intent *payment.Intent) error {
	step, ok := intent.NextCompensation()
	if !ok {
		return nil
	}

	if err := o.undoStep(ctx, intent, step); err != nil {
		return o.handleStepFailure(ctx, intent, step, err, "compensate")
	}

	if err := intent.CompleteCompensation(step.Seq); err != nil {
		return err
	}
	intent.Reschedule(o.clock.Now())

	o.logger.InfoContext(ctx, "payment step compensated",
		slog.String("payment_id", intent.ID().String()),
		slog.String("step", step.Name),
		slog.String("status", intent.Status().String()),
	)
	return o.store.InTx(ctx, func(ctx context.Context, db postgres.DBTX) error {
		return postgres.SavePayment(ctx, db, intent)
	})
}

// handleStepFailure categorizes errors to retry or unwind cleanly.
func (o *Orchestrator) handleStepFailure(ctx context.Context, intent *payment.Intent, step payment.StepState, cause error, phase string) error {
	retryable := isRetryableStepError(cause)
	exhausted := step.Attempts+1 >= maxStepAttempts

	if retryable && !exhausted {
		backoff := backoffFor(step.Attempts + 1)
		if err := intent.RecordAttempt(step.Seq, cause.Error(), o.clock.Now().Add(backoff)); err != nil {
			return err
		}

		o.logger.WarnContext(ctx, "payment step will retry",
			slog.String("payment_id", intent.ID().String()),
			slog.String("step", step.Name),
			slog.String("phase", phase),
			slog.Int("attempt", step.Attempts+1),
			slog.Duration("backoff", backoff),
			slog.String("error", cause.Error()),
		)
		return o.store.InTx(ctx, func(ctx context.Context, db postgres.DBTX) error {
			return postgres.SavePayment(ctx, db, intent)
		})
	}

	if phase == "compensate" {
		if err := intent.FailCompensation(step.Seq, cause.Error()); err != nil {
			return err
		}
		o.logger.ErrorContext(ctx, "payment compensation failed; manual intervention required",
			slog.String("payment_id", intent.ID().String()),
			slog.String("step", step.Name),
			slog.String("error", cause.Error()),
		)
	} else {
		if err := intent.FailStep(step.Seq, cause.Error()); err != nil {
			return err
		}
		o.logger.WarnContext(ctx, "payment step failed; unwinding",
			slog.String("payment_id", intent.ID().String()),
			slog.String("step", step.Name),
			slog.String("error", cause.Error()),
		)
	}

	intent.Reschedule(o.clock.Now())
	return o.store.InTx(ctx, func(ctx context.Context, db postgres.DBTX) error {
		return postgres.SavePayment(ctx, db, intent)
	})
}

// runStep dispatches a forward step.
func (o *Orchestrator) runStep(ctx context.Context, intent *payment.Intent, step payment.StepState) (string, error) {
	switch step.Name {
	case payment.StepReserve:
		return o.postStepTransaction(ctx, intent, step, o.reserveEntries)

	case payment.StepAuthorize:
		response, err := o.processor.Authorize(ctx, psp.AuthorizeRequest{
			IdempotencyKey: stepKey(intent, step, "execute"),
			Reference:      intent.Reference(),
			MerchantRef:    intent.MerchantRef(),
			AmountMinor:    intent.Amount().Amount(),
			Currency:       intent.Amount().Currency().Code(),
		})
		if err != nil {
			return "", err
		}
		return response.AuthorizationID, nil

	case payment.StepCapture:
		authRef := stepExternalRef(intent, payment.StepAuthorize)
		response, err := o.processor.Capture(ctx, stepKey(intent, step, "execute"), authRef, intent.Amount().Amount())
		if err != nil {
			return "", err
		}
		return response.CaptureID, nil

	case payment.StepSettle:
		return o.postStepTransaction(ctx, intent, step, o.settleEntries)

	default:
		return "", fmt.Errorf("%w: unknown step %q", payment.ErrInvalidStep, step.Name)
	}
}

// undoStep dispatches a compensating step.
func (o *Orchestrator) undoStep(ctx context.Context, intent *payment.Intent, step payment.StepState) error {
	switch step.Name {
	case payment.StepReserve, payment.StepSettle:
		return o.reverseStepTransaction(ctx, intent, step)

	case payment.StepAuthorize:
		return o.processor.Void(ctx, stepKey(intent, step, "compensate"), step.ExternalRef)

	case payment.StepCapture:
		return o.processor.Refund(ctx, stepKey(intent, step, "compensate"), step.ExternalRef, intent.Amount().Amount())

	default:
		return fmt.Errorf("%w: unknown step %q", payment.ErrInvalidStep, step.Name)
	}
}

// entryBuilder builds ledger entries for local steps.
type entryBuilder func(ctx context.Context, db postgres.DBTX, intent *payment.Intent) ([]ledger.Entry, error)

// postStepTransaction executes a local ledger movement idempotently.
func (o *Orchestrator) postStepTransaction(ctx context.Context, intent *payment.Intent, step payment.StepState, build entryBuilder) (string, error) {
	outcome, err := postgres.Execute(ctx, o.store, postgres.IdempotentRequest{
		Key:      stepKey(intent, step, "execute"),
		Endpoint: "saga:step",
		Body:     []byte(fmt.Sprintf(`{"payment":%q,"step":%d}`, intent.ID(), step.Seq)),
	}, func(ctx context.Context, db postgres.DBTX) (string, error) {
		entries, bErr := build(ctx, db, intent)
		if bErr != nil {
			return "", bErr
		}
		id, idErr := o.ids.NewTransactionID()
		if idErr != nil {
			return "", idErr
		}

		tx, txErr := ledger.NewTransaction(ledger.TransactionParams{
			ID:        id,
			Reference: intent.Reference(),
			Entries:   entries,
			PostedAt:  o.clock.Now(),
			Metadata: map[string]string{
				"payment_id": intent.ID().String(),
				"saga_step":  step.Name,
			},
		})
		if txErr != nil {
			return "", txErr
		}
		if postErr := postgres.PostTransaction(ctx, db, tx); postErr != nil {
			return "", postErr
		}
		if linkErr := postgres.LinkStepTransaction(ctx, db, intent.ID(), step.Seq, tx.ID().String()); linkErr != nil {
			return "", linkErr
		}
		return tx.ID().String(), nil
	})
	if err != nil {
		return "", err
	}
	return outcome.Value, nil
}

// reverseStepTransaction posts a reversal for a previously posted local step transaction.
func (o *Orchestrator) reverseStepTransaction(ctx context.Context, intent *payment.Intent, step payment.StepState) error {
	_, err := postgres.Execute(ctx, o.store, postgres.IdempotentRequest{
		Key:      stepKey(intent, step, "compensate"),
		Endpoint: "saga:compensate",
		Body:     []byte(fmt.Sprintf(`{"payment":%q,"step":%d}`, intent.ID(), step.Seq)),
	}, func(ctx context.Context, db postgres.DBTX) (string, error) {
		originalID, parseErr := ledger.ParseTransactionID(step.ExternalRef)
		if parseErr != nil {
			return "", fmt.Errorf("step %q has no ledger transaction to reverse: %w", step.Name, parseErr)
		}
		original, loadErr := postgres.LoadTransaction(ctx, db, originalID)
		if loadErr != nil {
			return "", loadErr
		}
		reversalID, idErr := o.ids.NewTransactionID()
		if idErr != nil {
			return "", idErr
		}

		reversal, revErr := ledger.NewReversal(original, reversalID, o.clock.Now(),
			fmt.Sprintf("saga compensation for payment %s step %s", intent.ID(), step.Name))
		if revErr != nil {
			return "", revErr
		}
		if postErr := postgres.PostTransaction(ctx, db, reversal); postErr != nil {
			return "", postErr
		}
		if linkErr := postgres.LinkStepCompensation(ctx, db, intent.ID(), step.Seq, reversal.ID().String()); linkErr != nil {
			return "", linkErr
		}
		return reversal.ID().String(), nil
	})
	return err
}

// reserveEntries moves funds from customer wallet to pending settlement.
func (o *Orchestrator) reserveEntries(ctx context.Context, db postgres.DBTX, intent *payment.Intent) ([]ledger.Entry, error) {
	walletCode, err := ledger.CustomerWalletCode(intent.CustomerRef())
	if err != nil {
		return nil, err
	}
	accounts, err := postgres.AccountsByCodes(ctx, db, []string{walletCode.String(), pendingSettlementCode})
	if err != nil {
		return nil, err
	}
	wallet, ok := accounts[walletCode.String()]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ledger.ErrAccountNotFound, walletCode)
	}
	pending, ok := accounts[pendingSettlementCode]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ledger.ErrAccountNotFound, pendingSettlementCode)
	}

	debit, err := ledger.NewEntry(wallet.ID(), ledger.Debit, intent.Amount())
	if err != nil {
		return nil, err
	}
	credit, err := ledger.NewEntry(pending.ID(), ledger.Credit, intent.Amount())
	if err != nil {
		return nil, err
	}
	return []ledger.Entry{debit, credit}, nil
}

// settleEntries moves pending settlement into merchant payable and platform fee revenue.
func (o *Orchestrator) settleEntries(ctx context.Context, db postgres.DBTX, intent *payment.Intent) ([]ledger.Entry, error) {
	payableCode, err := ledger.MerchantPayableCode(intent.MerchantRef())
	if err != nil {
		return nil, err
	}
	accounts, err := postgres.AccountsByCodes(ctx, db, []string{payableCode.String(), pendingSettlementCode, feeRevenueCode})
	if err != nil {
		return nil, err
	}
	payable, ok := accounts[payableCode.String()]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ledger.ErrAccountNotFound, payableCode)
	}
	pending, ok := accounts[pendingSettlementCode]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ledger.ErrAccountNotFound, pendingSettlementCode)
	}
	revenue, ok := accounts[feeRevenueCode]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ledger.ErrAccountNotFound, feeRevenueCode)
	}

	merchantAmount, err := intent.MerchantAmount()
	if err != nil {
		return nil, err
	}

	debit, err := ledger.NewEntry(pending.ID(), ledger.Debit, intent.Amount())
	if err != nil {
		return nil, err
	}
	entries := []ledger.Entry{debit}

	if merchantAmount.IsPositive() {
		credit, err := ledger.NewEntry(payable.ID(), ledger.Credit, merchantAmount)
		if err != nil {
			return nil, err
		}
		entries = append(entries, credit)
	}
	if intent.Fee().IsPositive() {
		credit, err := ledger.NewEntry(revenue.ID(), ledger.Credit, intent.Fee())
		if err != nil {
			return nil, err
		}
		entries = append(entries, credit)
	}
	return entries, nil
}

// Account codes used by the payment saga.
const (
	pendingSettlementCode = "liabilities:settlement:pending"
	feeRevenueCode        = "revenue:fees:processing"
)

// stepKey builds an idempotency key for one step and phase.
func stepKey(intent *payment.Intent, step payment.StepState, phase string) string {
	return fmt.Sprintf("saga:%s:%d:%s", intent.ID(), step.Seq, phase)
}

// stepExternalRef retrieves external reference stored for a step (e.g. auth ID for capture).
func stepExternalRef(intent *payment.Intent, name string) string {
	for _, step := range intent.Steps() {
		if step.Name == name {
			return step.ExternalRef
		}
	}
	return ""
}

// isRetryableStepError decides whether a failed step should be retried.
// A PSP decline is final; ledger errors go through isRetryableLedgerError.
func isRetryableStepError(err error) bool {
	if errors.Is(err, psp.ErrDeclined) {
		return false
	}
	return psp.Classify(err) || isRetryableLedgerError(err)
}

// isRetryableLedgerError checks whether a ledger error could succeed on retry.
func isRetryableLedgerError(err error) bool {
	switch {
	case errors.Is(err, ledger.ErrInsufficientFunds),
		errors.Is(err, ledger.ErrAccountClosed),
		errors.Is(err, ledger.ErrAccountNotFound),
		errors.Is(err, ledger.ErrUnbalanced):
		return false
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return false
	default:
		return true
	}
}

// backoffFor computes capped exponential backoff.
func backoffFor(attempt int) time.Duration {
	const base = time.Second
	const maxCap = 60 * time.Second

	delay := base << min(attempt-1, 6)
	if delay > maxCap {
		delay = maxCap
	}
	return delay
}

func paymentView(intent *payment.Intent) PaymentView {
	steps := make([]PaymentStepView, 0, len(intent.Steps()))
	for _, step := range intent.Steps() {
		steps = append(steps, PaymentStepView{
			Seq: step.Seq, Name: step.Name, Status: step.Status.String(),
			Attempts: step.Attempts, LastError: step.LastError,
		})
	}
	return PaymentView{
		ID:          intent.ID().String(),
		Reference:   intent.Reference(),
		CustomerRef: intent.CustomerRef(),
		MerchantRef: intent.MerchantRef(),
		Amount:      intent.Amount().Amount(),
		Fee:         intent.Fee().Amount(),
		Currency:    intent.Amount().Currency().Code(),
		Status:      intent.Status().String(),
		Steps:       steps,
		LastError:   intent.LastError(),
	}
}
