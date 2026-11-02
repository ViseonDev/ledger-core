package grpcapi

import (
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
	"github.com/ViseonDev/ledger-core/internal/domain/money"
	"github.com/ViseonDev/ledger-core/internal/ports"
)

func TestCodeFor(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want codes.Code
	}{
		{name: "unbalanced", err: ledger.ErrUnbalanced, want: codes.FailedPrecondition},
		{name: "insufficient funds", err: ledger.ErrInsufficientFunds, want: codes.FailedPrecondition},
		{name: "currency mismatch", err: money.ErrCurrencyMismatch, want: codes.InvalidArgument},
		{name: "account not found", err: ledger.ErrAccountNotFound, want: codes.NotFound},
		{name: "idempotency conflict", err: ports.ErrIdempotencyConflict, want: codes.Aborted},
		{name: "account exists", err: ledger.ErrAccountExists, want: codes.AlreadyExists},
		{name: "unknown", err: fmt.Errorf("disk on fire"), want: codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codeFor(tt.err); got != tt.want {
				t.Errorf("codeFor(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestCodeForUnwrapsWrappedErrors(t *testing.T) {
	wrapped := fmt.Errorf("posting: %w", fmt.Errorf("account x: %w", ledger.ErrInsufficientFunds))

	if got := codeFor(wrapped); got != codes.FailedPrecondition {
		t.Errorf("codeFor(wrapped) = %v, want FailedPrecondition", got)
	}
}

// REST and gRPC should agree on client error vs server error.
func TestErrorMappingsAgreeOnSeverity(t *testing.T) {
	clientErrors := []error{
		ledger.ErrUnbalanced,
		ledger.ErrInsufficientFunds,
		ledger.ErrAccountClosed,
		money.ErrCurrencyMismatch,
		money.ErrInvalidCurrency,
		ledger.ErrAccountNotFound,
		ledger.ErrTransactionNotFound,
		ports.ErrIdempotencyConflict,
		ledger.ErrAlreadyReversed,
		ledger.ErrAccountExists,
		ledger.ErrInvalidAccountCode,
	}

	for _, err := range clientErrors {
		if got := codeFor(err); got == codes.Internal || got == codes.Unknown {
			t.Errorf("%v maps to %v, but the REST layer treats it as a client error", err, got)
		}
	}
}
