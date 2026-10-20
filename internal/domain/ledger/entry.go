package ledger

import (
	"errors"
	"fmt"

	"github.com/ViseonDev/ledger-core/internal/domain/money"
)

// ErrNonPositiveAmount means an entry had a zero or negative amount.
// In double-entry bookkeeping, amounts are always positive because the direction (Debit/Credit)
// specifies which way the money moves.
var ErrNonPositiveAmount = errors.New("ledger: entry amount must be positive")

// Entry represents a single debit or credit of a positive amount against one account.
// It is a value object that never exists alone outside of a transaction.
type Entry struct {
	accountID AccountID
	direction Direction
	amount    money.Money
}

// NewEntry validates the inputs and returns an immutable entry.
func NewEntry(accountID AccountID, direction Direction, amount money.Money) (Entry, error) {
	if accountID.IsZero() {
		return Entry{}, fmt.Errorf("%w: entry requires an account id", ErrInvalidID)
	}
	if !direction.IsValid() {
		return Entry{}, fmt.Errorf("%w: %d", ErrInvalidDirection, direction)
	}
	if !amount.Currency().IsValid() {
		return Entry{}, fmt.Errorf("%w: entry requires a currency", money.ErrInvalidCurrency)
	}
	if !amount.IsPositive() {
		return Entry{}, fmt.Errorf("%w: got %v", ErrNonPositiveAmount, amount)
	}

	return Entry{accountID: accountID, direction: direction, amount: amount}, nil
}

// AccountID returns the account this entry posts to.
func (e Entry) AccountID() AccountID { return e.accountID }

// Direction returns whether this entry is a Debit or Credit.
func (e Entry) Direction() Direction { return e.direction }

// Amount returns the positive amount of money.
func (e Entry) Amount() money.Money { return e.amount }

// Currency returns the entry's currency.
func (e Entry) Currency() money.Currency { return e.amount.Currency() }

// IsDebit reports whether this entry is a debit.
func (e Entry) IsDebit() bool { return e.direction == Debit }

// Reverse returns a mirror entry: same account, same amount, but opposite direction.
// Can't fail, the entry was validated on creation.
func (e Entry) Reverse() Entry {
	e.direction = e.direction.Opposite()
	return e
}

// SignedAmount returns how this entry affects the balance of an account of the given type:
// positive if it increases the account, negative if it decreases it.
func (e Entry) SignedAmount(accountType AccountType) (money.Money, error) {
	if !accountType.IsValid() {
		return money.Money{}, fmt.Errorf("%w: %d", ErrInvalidAccountType, accountType)
	}
	return e.amount.Mul(accountType.SignOf(e.direction))
}

// String implements fmt.Stringer for readable logs and clear test error output.
func (e Entry) String() string {
	return fmt.Sprintf("%s %s %s", e.direction, e.amount, e.accountID)
}
