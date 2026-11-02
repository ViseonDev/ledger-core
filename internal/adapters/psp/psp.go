// Package psp models a payment processor.
package psp

import (
	"context"
	"errors"
	"fmt"
)

// Error categories. The saga retries some and unwinds on others.
var (
	// ErrDeclined is a terminal rejection: card declined, account blocked, etc.
	// Retrying will not help.
	ErrDeclined = errors.New("psp: declined")

	// ErrUnavailable is a transient failure: 5xx, connection reset, etc.
	// Retrying will probably succeed.
	ErrUnavailable = errors.New("psp: temporarily unavailable")

	// ErrAmbiguous is a timeout: the request outcome is unknown.
	ErrAmbiguous = errors.New("psp: outcome unknown")
)

// AuthorizeRequest asks the processor to authorize a payout.
type AuthorizeRequest struct {
	IdempotencyKey string
	Reference      string
	MerchantRef    string
	AmountMinor    int64
	Currency       string
}

// AuthorizeResponse carries the processor authorization identifier.
type AuthorizeResponse struct {
	AuthorizationID string
}

// CaptureResponse carries the processor capture identifier.
type CaptureResponse struct {
	CaptureID string
}

// Processor is the payment processor interface.
type Processor interface {
	Authorize(ctx context.Context, req AuthorizeRequest) (AuthorizeResponse, error)
	Void(ctx context.Context, idempotencyKey, authorizationID string) error
	Capture(ctx context.Context, idempotencyKey, authorizationID string, amountMinor int64) (CaptureResponse, error)
	Refund(ctx context.Context, idempotencyKey, captureID string, amountMinor int64) error
}

// Classify reports whether an error is retryable.
func Classify(err error) (retryable bool) {
	switch {
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrAmbiguous):
		return true
	default:
		return false
	}
}

// Declinedf builds a terminal decline error with a formatted reason.
func Declinedf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrDeclined, fmt.Sprintf(format, args...))
}
