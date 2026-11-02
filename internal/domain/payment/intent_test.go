package payment

import (
	"errors"
	"testing"
	"time"

	"github.com/ViseonDev/ledger-core/internal/domain/money"
)

func eur(t *testing.T, amount int64) money.Money {
	t.Helper()
	m, err := money.New(amount, money.EUR)
	if err != nil {
		t.Fatalf("money.New(%d): %v", amount, err)
	}
	return m
}

var createdAt = time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)

func newIntent(t *testing.T) *Intent {
	t.Helper()
	intent, err := New(Params{
		ID:          "01H8Z9V4KQ7Y2M3N4P5Q6R7S8T",
		Reference:   "order_4471",
		CustomerRef: "cus_demo_0001",
		MerchantRef: "mer_demo_01",
		Amount:      eur(t, 25_000),
		Fee:         eur(t, 750),
		Now:         createdAt,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return intent
}

func TestNewIntent(t *testing.T) {
	intent := newIntent(t)

	if intent.Status() != Pending {
		t.Errorf("Status() = %v, want Pending", intent.Status())
	}
	if got := len(intent.Steps()); got != 4 {
		t.Fatalf("Steps() has %d entries, want 4", got)
	}
	for i, step := range intent.Steps() {
		if step.Seq != i {
			t.Errorf("step %d has Seq %d", i, step.Seq)
		}
		if step.Status != StepPending {
			t.Errorf("step %q starts as %v, want PENDING", step.Name, step.Status)
		}
	}

	merchant, err := intent.MerchantAmount()
	if err != nil {
		t.Fatalf("MerchantAmount: %v", err)
	}
	if merchant.Amount() != 24_250 {
		t.Errorf("MerchantAmount() = %v, want EUR 242.50", merchant)
	}
}

func TestNewIntentValidation(t *testing.T) {
	valid := Params{
		ID: "p1", CustomerRef: "cus_1", MerchantRef: "mer_1",
		Amount: eur(t, 1000), Fee: eur(t, 100), Now: createdAt,
	}

	tests := []struct {
		name   string
		mutate func(*Params)
	}{
		{name: "no id", mutate: func(p *Params) { p.ID = "" }},
		{name: "no customer", mutate: func(p *Params) { p.CustomerRef = "" }},
		{name: "no merchant", mutate: func(p *Params) { p.MerchantRef = "" }},
		{name: "zero amount", mutate: func(p *Params) { p.Amount = eur(t, 0) }},
		{name: "negative amount", mutate: func(p *Params) { p.Amount = eur(t, -1) }},
		{name: "negative fee", mutate: func(p *Params) { p.Fee = eur(t, -1) }},
		{name: "fee exceeds amount", mutate: func(p *Params) { p.Fee = eur(t, 1001) }},
		{name: "no creation time", mutate: func(p *Params) { p.Now = time.Time{} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := valid
			tt.mutate(&params)

			if _, err := New(params); err == nil {
				t.Fatal("New succeeded, want an error")
			}
		})
	}

	t.Run("fee equal to amount is allowed", func(t *testing.T) {
		params := valid
		params.Fee = eur(t, 1000)
		if _, err := New(params); err != nil {
			t.Fatalf("New returned unexpected error: %v", err)
		}
	})
}

func TestHappyPath(t *testing.T) {
	intent := newIntent(t)

	if err := intent.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	for seq := range 4 {
		step, ok := intent.NextStep()
		if !ok {
			t.Fatalf("no step at position %d", seq)
		}
		if step.Seq != seq {
			t.Fatalf("NextStep() returned seq %d, want %d", step.Seq, seq)
		}
		if err := intent.CompleteStep(step.Seq, "ext_"+step.Name); err != nil {
			t.Fatalf("CompleteStep(%d): %v", seq, err)
		}
	}

	if intent.Status() != Settled {
		t.Errorf("Status() = %v, want Settled", intent.Status())
	}
	if _, ok := intent.NextStep(); ok {
		t.Error("NextStep() returned a step after settling")
	}
}

// Steps unwind in reverse order to ensure accounts remain balanced and non-negative.
func TestCompensationRunsInReverseOrder(t *testing.T) {
	intent := newIntent(t)
	if err := intent.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	// Complete the first three steps, then fail the fourth.
	for seq := range 3 {
		if err := intent.CompleteStep(seq, "ext"); err != nil {
			t.Fatalf("CompleteStep(%d): %v", seq, err)
		}
	}
	if err := intent.FailStep(3, "processor declined"); err != nil {
		t.Fatalf("FailStep: %v", err)
	}
	if intent.Status() != Compensating {
		t.Fatalf("Status() = %v, want Compensating", intent.Status())
	}

	var undone []string
	for {
		step, ok := intent.NextCompensation()
		if !ok {
			break
		}
		undone = append(undone, step.Name)
		if err := intent.CompleteCompensation(step.Seq); err != nil {
			t.Fatalf("CompleteCompensation(%d): %v", step.Seq, err)
		}
	}

	want := []string{StepCapture, StepAuthorize, StepReserve}
	if len(undone) != len(want) {
		t.Fatalf("undid %d steps (%v), want %d", len(undone), undone, len(want))
	}
	for i := range want {
		if undone[i] != want[i] {
			t.Errorf("compensation %d undid %q, want %q", i, undone[i], want[i])
		}
	}

	if intent.Status() != Compensated {
		t.Errorf("Status() = %v, want Compensated", intent.Status())
	}
}

// Failing the very first step means nothing happened, so there is nothing to
// undo and the payment is immediately compensated.
func TestFailingFirstStepNeedsNoCompensation(t *testing.T) {
	intent := newIntent(t)
	if err := intent.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	if err := intent.FailStep(0, "insufficient funds"); err != nil {
		t.Fatalf("FailStep: %v", err)
	}

	if intent.Status() != Compensated {
		t.Errorf("Status() = %v, want Compensated", intent.Status())
	}
	if _, ok := intent.NextCompensation(); ok {
		t.Error("NextCompensation() returned a step when nothing had completed")
	}
}

func TestCompensationFailureIsTerminal(t *testing.T) {
	intent := newIntent(t)
	if err := intent.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := intent.CompleteStep(0, "tx1"); err != nil {
		t.Fatalf("CompleteStep: %v", err)
	}
	if err := intent.FailStep(1, "declined"); err != nil {
		t.Fatalf("FailStep: %v", err)
	}

	if err := intent.FailCompensation(0, "processor unreachable after 6 attempts"); err != nil {
		t.Fatalf("FailCompensation: %v", err)
	}

	if intent.Status() != CompensationFailed {
		t.Errorf("Status() = %v, want CompensationFailed", intent.Status())
	}
	if !intent.Status().IsTerminal() {
		t.Error("CompensationFailed must be terminal")
	}
	if err := intent.CompleteStep(1, "x"); !errors.Is(err, ErrTerminal) {
		t.Errorf("CompleteStep on a terminal payment error = %v, want ErrTerminal", err)
	}
}

func TestTerminalStatesRejectFurtherWork(t *testing.T) {
	for _, status := range AllStatuses() {
		if !status.IsTerminal() {
			continue
		}
		t.Run(status.String(), func(t *testing.T) {
			intent := newIntent(t)
			intent.status = status

			if err := intent.CompleteStep(0, "x"); !errors.Is(err, ErrTerminal) {
				t.Errorf("CompleteStep from %v error = %v, want ErrTerminal", status, err)
			}
			if err := intent.FailStep(0, "x"); !errors.Is(err, ErrTerminal) {
				t.Errorf("FailStep from %v error = %v, want ErrTerminal", status, err)
			}
		})
	}
}

func TestStatusRoundTrip(t *testing.T) {
	for _, status := range AllStatuses() {
		parsed, err := ParseStatus(status.String())
		if err != nil {
			t.Errorf("ParseStatus(%q): %v", status, err)
			continue
		}
		if parsed != status {
			t.Errorf("round trip of %v produced %v", status, parsed)
		}
	}
}

func TestRecordAttemptDoesNotAdvanceTheStep(t *testing.T) {
	intent := newIntent(t)
	if err := intent.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	later := createdAt.Add(30 * time.Second)
	if err := intent.RecordAttempt(0, "connection reset", later); err != nil {
		t.Fatalf("RecordAttempt: %v", err)
	}

	step, ok := intent.NextStep()
	if !ok {
		t.Fatal("NextStep() returned nothing after a retryable failure")
	}
	if step.Seq != 0 {
		t.Errorf("NextStep() = %d, want the same step retried", step.Seq)
	}
	if step.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", step.Attempts)
	}
	if !intent.NextAttemptAt().Equal(later) {
		t.Errorf("NextAttemptAt() = %v, want %v", intent.NextAttemptAt(), later)
	}
}
