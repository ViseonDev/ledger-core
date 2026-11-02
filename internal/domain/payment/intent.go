package payment

import (
	"fmt"
	"slices"
	"time"

	"github.com/ViseonDev/ledger-core/internal/domain/money"
)

// ID identifies a payment intent.
type ID string

// IsZero reports whether the id is empty.
func (id ID) IsZero() bool { return id == "" }

// String returns the ID as a string.
func (id ID) String() string { return string(id) }

// Step names in order of execution.
const (
	StepReserve   = "reserve"
	StepAuthorize = "authorize"
	StepCapture   = "capture"
	StepSettle    = "settle"
)

// StepOrder defines the sequence forward steps run, and in reverse, how compensation unwinds.
func StepOrder() []string {
	return []string{StepReserve, StepAuthorize, StepCapture, StepSettle}
}

// StepState tracks the outcome and metadata for one step in the saga.
type StepState struct {
	Seq         int
	Name        string
	Status      StepStatus
	Attempts    int
	ExternalRef string
	LastError   string
}

// Intent represents the payment aggregate in flight.
type Intent struct {
	id          ID
	reference   string
	customerRef string
	merchantRef string
	amount      money.Money
	fee         money.Money
	status      Status
	attempts    int
	nextAttempt time.Time
	lastError   string
	steps       []StepState
}

// Params holds inputs required to initialize a new payment.
type Params struct {
	ID          ID
	Reference   string
	CustomerRef string
	MerchantRef string
	Amount      money.Money
	Fee         money.Money
	Now         time.Time
}

// New initializes a pending payment with its steps configured in order.
func New(p Params) (*Intent, error) {
	switch {
	case p.ID.IsZero():
		return nil, fmt.Errorf("%w: payment id is required", ErrInvalidStep)
	case p.CustomerRef == "":
		return nil, fmt.Errorf("%w: customer reference is required", ErrInvalidStep)
	case p.MerchantRef == "":
		return nil, fmt.Errorf("%w: merchant reference is required", ErrInvalidStep)
	case !p.Amount.IsPositive():
		return nil, fmt.Errorf("%w: amount must be positive, got %v", ErrInvalidStep, p.Amount)
	case p.Fee.IsNegative():
		return nil, fmt.Errorf("%w: fee cannot be negative, got %v", ErrInvalidStep, p.Fee)
	case p.Amount.Currency() != p.Fee.Currency():
		return nil, money.CurrencyMismatchError{Left: p.Amount.Currency(), Right: p.Fee.Currency()}
	case p.Now.IsZero():
		return nil, fmt.Errorf("%w: creation time is required", ErrInvalidStep)
	}

	// Fee is deducted from the total amount, so it cannot exceed the amount.
	comparison, err := p.Fee.Compare(p.Amount)
	if err != nil {
		return nil, err
	}
	if comparison > 0 {
		return nil, fmt.Errorf("%w: fee %v exceeds amount %v", ErrInvalidStep, p.Fee, p.Amount)
	}

	names := StepOrder()
	steps := make([]StepState, 0, len(names))
	for i, name := range names {
		steps = append(steps, StepState{Seq: i, Name: name, Status: StepPending})
	}

	return &Intent{
		id:          p.ID,
		reference:   p.Reference,
		customerRef: p.CustomerRef,
		merchantRef: p.MerchantRef,
		amount:      p.Amount,
		fee:         p.Fee,
		status:      Pending,
		nextAttempt: p.Now.UTC(),
		steps:       steps,
	}, nil
}

// Rehydrate rebuilds an Intent from persistent storage.
func Rehydrate(p Params, status Status, attempts int, nextAttempt time.Time, lastError string, steps []StepState) (*Intent, error) {
	intent, err := New(p)
	if err != nil {
		return nil, err
	}
	if !status.IsValid() {
		return nil, fmt.Errorf("%w: %d", ErrInvalidStatus, status)
	}
	if len(steps) != len(StepOrder()) {
		return nil, fmt.Errorf("%w: stored payment has %d steps, expected %d", ErrInvalidStep, len(steps), len(StepOrder()))
	}

	intent.status = status
	intent.attempts = attempts
	intent.nextAttempt = nextAttempt.UTC()
	intent.lastError = lastError
	intent.steps = slices.Clone(steps)
	return intent, nil
}

// Accessors for reading aggregate fields.

// ID returns the payment identifier.
func (i *Intent) ID() ID { return i.id }

// Reference returns the idempotency reference string.
func (i *Intent) Reference() string { return i.reference }

// CustomerRef returns the customer identifier.
func (i *Intent) CustomerRef() string { return i.customerRef }

// MerchantRef returns the merchant identifier.
func (i *Intent) MerchantRef() string { return i.merchantRef }

// Amount returns the total payment amount.
func (i *Intent) Amount() money.Money { return i.amount }

// Fee returns the fee component deducted from the amount.
func (i *Intent) Fee() money.Money { return i.fee }

// Status returns current lifecycle status.
func (i *Intent) Status() Status { return i.status }

// Attempts returns how many overall attempts were made.
func (i *Intent) Attempts() int { return i.attempts }

// NextAttemptAt returns when this payment is next due to run.
func (i *Intent) NextAttemptAt() time.Time { return i.nextAttempt }

// LastError returns the error string from the last failed attempt.
func (i *Intent) LastError() string { return i.lastError }

// Steps returns a defensive copy of all step states.
func (i *Intent) Steps() []StepState { return slices.Clone(i.steps) }

// MerchantAmount is the payout to the merchant (amount minus fee).
func (i *Intent) MerchantAmount() (money.Money, error) { return i.amount.Sub(i.fee) }

// NextStep returns the next pending step when moving forward.
func (i *Intent) NextStep() (StepState, bool) {
	for _, step := range i.steps {
		if step.Status == StepPending {
			return step, true
		}
	}
	return StepState{}, false
}

// NextCompensation returns the next step to reverse when unwinding (scanned in reverse order).
func (i *Intent) NextCompensation() (StepState, bool) {
	for idx := len(i.steps) - 1; idx >= 0; idx-- {
		if i.steps[idx].Status == StepDone {
			return i.steps[idx], true
		}
	}
	return StepState{}, false
}

// Begin transitions a pending payment to in-progress.
func (i *Intent) Begin() error {
	if i.status != Pending && i.status != InProgress {
		return fmt.Errorf("%w: cannot begin from %v", ErrInvalidTransition, i.status)
	}
	i.status = InProgress
	return nil
}

// CompleteStep marks a step done. If this was the final step, the whole payment settles.
func (i *Intent) CompleteStep(seq int, externalRef string) error {
	if i.status.IsTerminal() {
		return fmt.Errorf("%w: %v", ErrTerminal, i.status)
	}
	step, err := i.stepAt(seq)
	if err != nil {
		return err
	}
	if step.Status != StepPending {
		return fmt.Errorf("%w: step %q is %v, expected PENDING", ErrInvalidTransition, step.Name, step.Status)
	}

	i.steps[seq].Status = StepDone
	i.steps[seq].Attempts++
	i.steps[seq].ExternalRef = externalRef
	i.steps[seq].LastError = ""
	i.lastError = ""

	if _, more := i.NextStep(); !more {
		i.status = Settled
	} else {
		i.status = InProgress
	}
	return nil
}

// FailStep marks a step terminally failed and initiates unwinding.
func (i *Intent) FailStep(seq int, reason string) error {
	if i.status.IsTerminal() {
		return fmt.Errorf("%w: %v", ErrTerminal, i.status)
	}
	step, err := i.stepAt(seq)
	if err != nil {
		return err
	}
	if step.Status != StepPending {
		return fmt.Errorf("%w: step %q is %v, expected PENDING", ErrInvalidTransition, step.Name, step.Status)
	}

	i.steps[seq].Status = StepFailed
	i.steps[seq].Attempts++
	i.steps[seq].LastError = reason
	i.lastError = reason

	if _, pending := i.NextCompensation(); pending {
		i.status = Compensating
	} else {
		i.status = Compensated
	}
	return nil
}

// RecordAttempt logs a retryable failure without failing the step, allowing another try.
func (i *Intent) RecordAttempt(seq int, reason string, nextAttempt time.Time) error {
	if _, err := i.stepAt(seq); err != nil {
		return err
	}
	i.steps[seq].Attempts++
	i.steps[seq].LastError = reason
	i.attempts++
	i.lastError = reason
	i.nextAttempt = nextAttempt.UTC()
	return nil
}

// CompleteCompensation marks a previously finished step as successfully undone.
func (i *Intent) CompleteCompensation(seq int) error {
	if i.status != Compensating {
		return fmt.Errorf("%w: cannot compensate from %v", ErrInvalidTransition, i.status)
	}
	step, err := i.stepAt(seq)
	if err != nil {
		return err
	}
	if step.Status != StepDone {
		return fmt.Errorf("%w: step %q is %v, expected DONE", ErrInvalidTransition, step.Name, step.Status)
	}

	i.steps[seq].Status = StepCompensated
	i.steps[seq].LastError = ""

	if _, more := i.NextCompensation(); !more {
		i.status = Compensated
	}
	return nil
}

// FailCompensation records that compensation failed: this enters the state that needs human intervention.
func (i *Intent) FailCompensation(seq int, reason string) error {
	if i.status != Compensating {
		return fmt.Errorf("%w: cannot fail compensation from %v", ErrInvalidTransition, i.status)
	}
	if _, err := i.stepAt(seq); err != nil {
		return err
	}
	i.steps[seq].LastError = reason
	i.lastError = reason
	i.status = CompensationFailed
	return nil
}

// Reschedule updates when the worker should attempt this payment next.
func (i *Intent) Reschedule(at time.Time) {
	i.nextAttempt = at.UTC()
}

// SetStepTransaction links the posted ledger transaction ID to the step.
func (i *Intent) SetStepTransaction(seq int, transactionID string) error {
	if _, err := i.stepAt(seq); err != nil {
		return err
	}
	i.steps[seq].ExternalRef = transactionID
	return nil
}

func (i *Intent) stepAt(seq int) (StepState, error) {
	if seq < 0 || seq >= len(i.steps) {
		return StepState{}, fmt.Errorf("%w: sequence %d out of range", ErrInvalidStep, seq)
	}
	return i.steps[seq], nil
}
