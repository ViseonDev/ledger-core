// Package ledger implements double entry bookkeeping: accounts, entries,
// and the balanced transactions that move value between them.
package ledger

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidDirection means a value was not DEBIT or CREDIT.
var ErrInvalidDirection = errors.New("ledger: invalid direction")

// Direction is the side of the books an entry lands on.
type Direction int

const (
	// Debit increases assets and expenses, and decreases liabilities, equity
	// and revenue.
	Debit Direction = iota + 1 // iota = 0, and iota + 1 = 1, so Debit = 1

	// Credit increases liabilities, equity and revenue, and decreases assets
	// and expenses.
	Credit // iota = 1, and iota + 1 = 2, so Credit = 2
)

// Opposite returns the other direction. Reversing a transaction flips every
// entry's direction, which is what makes a reversal cancel its original
// exactly.
//
// Ex:
// Debit.Opposite() == Credit
// Credit.Opposite() == Debit
func (d Direction) Opposite() Direction {
	if d == Debit {
		return Credit
	}
	return Debit
}

// IsValid reports whether d is Debit or Credit.
func (d Direction) IsValid() bool {
	return d == Debit || d == Credit
}

// String implements fmt.Stringer using the wire representation, so logs and API
// payloads agree.
func (d Direction) String() string {
	switch d {
	case Debit:
		return "DEBIT"
	case Credit:
		return "CREDIT"
	default:
		return "INVALID"
	}
}

// ParseDirection resolves the wire representation. Input is trimmed and
// upper-cased.
func ParseDirection(s string) (Direction, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBIT":
		return Debit, nil
	case "CREDIT":
		return Credit, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrInvalidDirection, s)
	}
}
