package ledger

import (
	"errors"
	"fmt"
	"strings"
)

// All account types are: ASSETS, LIABILITIES, EQUITY, REVENUE, EXPENSE

// ErrInvalidAccountType means a value was not one of the five account types.
var ErrInvalidAccountType = errors.New("ledger: invalid account type")

// AccountType is the classification that determines which direction increases an account.
// Ex: ASSETS -> assets (lower case) is the root of all asset accounts.
// Ex: LIABILITIES -> liabilities (lower case) is the root of all liability accounts.
type AccountType int

const (
	// Asset is something the platform owns or is owed: bank balances, funds
	// held at a payment processor, receivables. Increases with a debit.
	Asset AccountType = iota + 1

	// Liability is something the platform owes: customer wallet balances,
	// merchant payables, taxes due. Increases with a credit.
	Liability

	// Equity is the residual interest: retained earnings, contributed capital.
	// Increases with a credit.
	Equity

	// Revenue is income earned: processing fees the platform charges.
	// Increases with a credit.
	Revenue

	// Expense is cost incurred: fees the payment processor charges the
	// platform. Increases with a debit.
	Expense
)

// AllAccountTypes returns every account type, in declaration order.
func AllAccountTypes() []AccountType {
	return []AccountType{Asset, Liability, Equity, Revenue, Expense}
}

// NormalBalance returns the direction in which this type of account increases.
func (t AccountType) NormalBalance() Direction {
	switch t {
	case Asset, Expense:
		return Debit
	case Liability, Equity, Revenue:
		return Credit
	default:
		return 0 // invalid; Direction(0) reports IsValid() == false
	}
}

// SignOf returns +1 if an entry in the given direction increases this type of account
// and -1 if it decreases it.
func (t AccountType) SignOf(d Direction) int64 {
	if d == t.NormalBalance() {
		return 1
	}
	return -1
}

// IsValid reports whether t is one of the five defined types.
func (t AccountType) IsValid() bool {
	return t >= Asset && t <= Expense
}

// MatchesCodeRoot reports whether the given account-code root segment is the
// conventional one for this type.
func (t AccountType) MatchesCodeRoot(root string) bool {
	return strings.EqualFold(root, t.codeRoot())
}

func (t AccountType) codeRoot() string {
	switch t {
	case Asset:
		return "assets"
	case Liability:
		return "liabilities"
	case Equity:
		return "equity"
	case Revenue:
		return "revenue"
	case Expense:
		return "expense"
	default:
		return ""
	}
}

// String implements fmt.Stringer using the wire representation.
func (t AccountType) String() string {
	switch t {
	case Asset:
		return "ASSET"
	case Liability:
		return "LIABILITY"
	case Equity:
		return "EQUITY"
	case Revenue:
		return "REVENUE"
	case Expense:
		return "EXPENSE"
	default:
		return "INVALID"
	}
}

// ParseAccountType resolves the wire representation.
func ParseAccountType(s string) (AccountType, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ASSET":
		return Asset, nil
	case "LIABILITY":
		return Liability, nil
	case "EQUITY":
		return Equity, nil
	case "REVENUE":
		return Revenue, nil
	case "EXPENSE":
		return Expense, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrInvalidAccountType, s)
	}
}
