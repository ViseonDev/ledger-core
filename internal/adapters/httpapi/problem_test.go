package httpapi

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
	"github.com/ViseonDev/ledger-core/internal/domain/money"
	"github.com/ViseonDev/ledger-core/internal/ports"
)

func TestProblemFor(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "unbalanced", err: ledger.ErrUnbalanced, wantStatus: http.StatusUnprocessableEntity},
		{name: "insufficient funds", err: ledger.ErrInsufficientFunds, wantStatus: http.StatusUnprocessableEntity},
		{name: "account closed", err: ledger.ErrAccountClosed, wantStatus: http.StatusUnprocessableEntity},
		{name: "currency mismatch", err: money.ErrCurrencyMismatch, wantStatus: http.StatusUnprocessableEntity},
		{name: "invalid currency", err: money.ErrInvalidCurrency, wantStatus: http.StatusBadRequest},
		{name: "invalid account code", err: ledger.ErrInvalidAccountCode, wantStatus: http.StatusBadRequest},
		{name: "account not found", err: ledger.ErrAccountNotFound, wantStatus: http.StatusNotFound},
		{name: "transaction not found", err: ledger.ErrTransactionNotFound, wantStatus: http.StatusNotFound},
		{name: "idempotency conflict", err: ports.ErrIdempotencyConflict, wantStatus: http.StatusConflict},
		{name: "already reversed", err: ledger.ErrAlreadyReversed, wantStatus: http.StatusConflict},
		{name: "account exists", err: ledger.ErrAccountExists, wantStatus: http.StatusConflict},
		{name: "key required", err: ports.ErrIdempotencyKeyRequired, wantStatus: http.StatusPreconditionRequired},
		{name: "unknown", err: fmt.Errorf("the disk is on fire"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := problemFor(tt.err)

			if got.Status != tt.wantStatus {
				t.Errorf("Status = %d, want %d", got.Status, tt.wantStatus)
			}
			if got.Title == "" {
				t.Error("Title is empty")
			}
			if got.Type == "" {
				t.Error("Type is empty")
			}
		})
	}
}

// Wrapped errors must map the same way, because every layer in this codebase
// wraps with %w on the way out.
func TestProblemForUnwrapsWrappedErrors(t *testing.T) {
	wrapped := fmt.Errorf("posting transaction: %w",
		fmt.Errorf("account liabilities:customer:wallet:cus_1: %w", ledger.ErrInsufficientFunds))

	if got := problemFor(wrapped); got.Status != http.StatusUnprocessableEntity {
		t.Errorf("Status = %d, want 422: errors.Is should see through the wrapping", got.Status)
	}
}

// Every mapped problem type must be distinct, or a client cannot branch on it.
func TestProblemTypesAreUnique(t *testing.T) {
	errs := []error{
		ledger.ErrUnbalanced, ledger.ErrInsufficientFunds, ledger.ErrAccountClosed,
		money.ErrCurrencyMismatch, money.ErrInvalidCurrency, ledger.ErrAccountNotFound,
		ledger.ErrTransactionNotFound, ports.ErrIdempotencyConflict, ledger.ErrAlreadyReversed,
		ledger.ErrAccountExists, ledger.ErrTransactionExists, ports.ErrIdempotencyKeyRequired,
	}

	seen := make(map[string]string, len(errs))
	for _, err := range errs {
		problem := problemFor(err)
		if previous, ok := seen[problem.Type]; ok {
			t.Errorf("problem type %q is used by both %q and %q", problem.Type, previous, err)
		}
		seen[problem.Type] = err.Error()
	}
}
