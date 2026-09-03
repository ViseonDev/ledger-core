package money

import (
	"errors"
	"fmt"
)

// Sentinel errors for this package. Callers match on these with errors.Is
var (
	// ErrInvalidCurrency means a currency code was not recognised, or a zero
	// Currency was used in an operation that requires a real one.
	ErrInvalidCurrency = errors.New("money: invalid currency")

	// ErrCurrencyMismatch means an operation combined two different currencies.
	ErrCurrencyMismatch = errors.New("money: currency mismatch")

	// ErrOverflow means an operation's result does not fit in int64.
	ErrOverflow = errors.New("money: arithmetic overflow")

	// ErrInvalidRatios means an allocation was asked for with ratios that
	// cannot describe a split.
	ErrInvalidRatios = errors.New("money: invalid allocation ratios")
)

// CurrencyMismatchError reports an attempt to combine two currencies, and
// carries both so the message can name them.
type CurrencyMismatchError struct {
	Left  Currency
	Right Currency
}

func (e CurrencyMismatchError) Error() string {
	return fmt.Sprintf("money: currency mismatch: %s and %s", e.Left, e.Right)
}

// Is makes errors.Is(err, ErrCurrencyMismatch) report true for this type, so
// callers can match the general case without knowing about the struct, while
// code that wants the detail can still use errors.As.
func (e CurrencyMismatchError) Is(target error) bool {
	return target == ErrCurrencyMismatch
}
