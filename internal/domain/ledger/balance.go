package ledger

import (
	"fmt"

	"github.com/ViseonDev/ledger-core/internal/domain/money"
)

// We used to spend weeks looking for missing cents in my previous job due to an incredibly messy implementation of the balance logic.
// I've done my best to avoid the same mistakes in this implementation.

// Balance is an account's position: the cumulative total of debits and credits
// posted to it, plus the account type needed to interpret them.
type Balance struct {
	accountID   AccountID
	accountType AccountType
	debits      money.Money
	credits     money.Money
}

// NewBalance constructs a balance from cumulative debit and credit totals.
func NewBalance(accountID AccountID, accountType AccountType, debits, credits money.Money) (Balance, error) {
	if accountID.IsZero() {
		return Balance{}, fmt.Errorf("%w: balance requires an account id", ErrInvalidID)
	}
	if !accountType.IsValid() {
		return Balance{}, fmt.Errorf("%w: %d", ErrInvalidAccountType, accountType)
	}
	if debits.Currency() != credits.Currency() {
		return Balance{}, money.CurrencyMismatchError{Left: debits.Currency(), Right: credits.Currency()}
	}
	if !debits.Currency().IsValid() {
		return Balance{}, money.ErrInvalidCurrency
	}
	if debits.IsNegative() {
		return Balance{}, fmt.Errorf("ledger: cumulative debits cannot be negative: %v", debits)
	}
	if credits.IsNegative() {
		return Balance{}, fmt.Errorf("ledger: cumulative credits cannot be negative: %v", credits)
	}

	return Balance{
		accountID:   accountID,
		accountType: accountType,
		debits:      debits,
		credits:     credits,
	}, nil
}

// AccountID returns the account this balance belongs to.
func (b Balance) AccountID() AccountID { return b.accountID }

// Debits returns the cumulative total of debit entries.
func (b Balance) Debits() money.Money { return b.debits }

// Credits returns the cumulative total of credit entries.
func (b Balance) Credits() money.Money { return b.credits }

// Currency returns the currency of the balance.
func (b Balance) Currency() money.Currency { return b.debits.Currency() }

// Normal returns the balance in the account's normal direction: debits minus
// credits for an asset or expense, credits minus debits for a liability, equity
// or revenue account.
func (b Balance) Normal() (money.Money, error) {
	if b.accountType.NormalBalance() == Debit {
		return b.debits.Sub(b.credits)
	}
	return b.credits.Sub(b.debits)
}

// Apply returns the balance that would result from posting an entry.
func (b Balance) Apply(direction Direction, amount money.Money) (Balance, error) {
	if !direction.IsValid() {
		return Balance{}, fmt.Errorf("%w: %d", ErrInvalidDirection, direction)
	}
	if amount.IsNegative() {
		return Balance{}, fmt.Errorf("ledger: entry amount cannot be negative: %v", amount)
	}

	var err error
	if direction == Debit {
		b.debits, err = b.debits.Add(amount)
	} else {
		b.credits, err = b.credits.Add(amount)
	}
	if err != nil {
		return Balance{}, err
	}
	return b, nil
}

// WouldGoNegative reports whether posting the given entry would take the
// balance below zero in the account's normal direction.
func (b Balance) WouldGoNegative(direction Direction, amount money.Money) (bool, error) {
	after, err := b.Apply(direction, amount)
	if err != nil {
		return false, err
	}
	normal, err := after.Normal()
	if err != nil {
		return false, err
	}
	return normal.IsNegative(), nil
}
