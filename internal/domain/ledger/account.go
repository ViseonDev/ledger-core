package ledger

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ViseonDev/ledger-core/internal/domain/money"
)

// Errors returned when constructing or modifying an account.
var (
	// ErrInvalidAccountName means a name was empty or only whitespace.
	ErrInvalidAccountName = errors.New("ledger: invalid account name")

	// ErrAccountCodeTypeMismatch means the code's root segment does not match the account type.
	// Ex: A liability account should not have an asset code.
	ErrAccountCodeTypeMismatch = errors.New("ledger: account code root does not match account type")
)

// Account is a named bucket in the chart of accounts.
type Account struct {
	id            AccountID
	code          AccountCode
	name          string
	accountType   AccountType
	currency      money.Currency
	allowNegative bool
	status        AccountStatus
}

// AccountParams carries the inputs to NewAccount.
type AccountParams struct {
	ID            AccountID
	Code          AccountCode
	Name          string
	Type          AccountType
	Currency      money.Currency
	AllowNegative bool
}

// NewAccount validates the parameters and returns an active account.
func NewAccount(p AccountParams) (Account, error) {
	if p.ID.IsZero() {
		return Account{}, fmt.Errorf("%w: account id is required", ErrInvalidID)
	}
	if p.Code == "" {
		return Account{}, fmt.Errorf("%w: account code is required", ErrInvalidAccountCode)
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return Account{}, fmt.Errorf("%w: account name is required", ErrInvalidAccountName)
	}
	if !p.Type.IsValid() {
		return Account{}, fmt.Errorf("%w: %d", ErrInvalidAccountType, p.Type)
	}
	if !p.Currency.IsValid() {
		return Account{}, fmt.Errorf("%w: account currency is required", money.ErrInvalidCurrency)
	}
	if !p.Type.MatchesCodeRoot(p.Code.Root()) {
		return Account{}, fmt.Errorf("%w: code %q has root %q but type is %v",
			ErrAccountCodeTypeMismatch, p.Code, p.Code.Root(), p.Type)
	}

	return Account{
		id:            p.ID,
		code:          p.Code,
		name:          name,
		accountType:   p.Type,
		currency:      p.Currency,
		allowNegative: p.AllowNegative,
		status:        Active,
	}, nil
}

// RehydrateAccount reconstructs an account from stored state, including its status.
// This is useful for loading accounts from a database, since the database
// may store a different status than the one returned by NewAccount.
func RehydrateAccount(p AccountParams, status AccountStatus) (Account, error) {
	account, err := NewAccount(p)
	if err != nil {
		return Account{}, err
	}
	if !status.IsValid() {
		return Account{}, fmt.Errorf("ledger: invalid stored account status: %d", status)
	}
	account.status = status
	return account, nil
}

// ID returns the account's identifier.
func (a Account) ID() AccountID { return a.id }

// Code returns the account's hierarchical code.
func (a Account) Code() AccountCode { return a.code }

// Name returns the human label.
func (a Account) Name() string { return a.name }

// Type returns the account's classification.
func (a Account) Type() AccountType { return a.accountType }

// Currency returns the account's currency. Every entry posted to this account must be in it.
func (a Account) Currency() money.Currency { return a.currency }

// Status returns the lifecycle state.
func (a Account) Status() AccountStatus { return a.status }

// AllowsNegative reports whether the balance may go below zero in the account's normal direction.
func (a Account) AllowsNegative() bool { return a.allowNegative }

// NormalBalance returns the direction in which this account increases.
func (a Account) NormalBalance() Direction { return a.accountType.NormalBalance() }

// IsActive reports whether the account accepts new entries.
func (a Account) IsActive() bool { return a.status == Active }

// Close marks the account closed. It returns an error if it is already closed,
// so that a double close is visible rather than silently idempotent.
//
// Note: Closing an account is an operational event and doing it twice usually
// means two processes disagree about who owns the account.
func (a Account) Close() (Account, error) {
	if a.status == Closed {
		return Account{}, fmt.Errorf("%w: %s", ErrAccountClosed, a.code)
	}
	a.status = Closed
	return a, nil
}

// Rename renames the account. The code, type and currency are immutable:
// changing any of them would invalidate every historical balance of the account.
func (a Account) Rename(name string) (Account, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return Account{}, fmt.Errorf("%w: account name is required", ErrInvalidAccountName)
	}
	a.name = trimmed
	return a, nil
}

// AcceptsAmount reports whether this account can receive an entry of the given
// amount, checking status and currency.
func (a Account) AcceptsAmount(amount money.Money) error {
	if a.status != Active {
		return fmt.Errorf("%w: %s", ErrAccountClosed, a.code)
	}
	if amount.Currency() != a.currency {
		return money.CurrencyMismatchError{Left: a.currency, Right: amount.Currency()}
	}
	return nil
}
