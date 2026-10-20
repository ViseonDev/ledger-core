package ledger

import (
	"errors"
	"fmt"
	"strings"
)

// ErrAccountClosed means an operation was attempted on a closed account.
var ErrAccountClosed = errors.New("ledger: account is closed")

// AccountStatus is the lifecycle state of an account.
type AccountStatus int

const (
	// Active accounts accept new entries.
	Active AccountStatus = iota + 1 // 1 - Active

	// Closed accounts reject new entries but retain their full history and remain readable.
	Closed // 2 - Closed
)

// IsValid reports whether s is a defined status.
func (s AccountStatus) IsValid() bool { return s == Active || s == Closed }

// String implements fmt.Stringer using the wire representation.
func (s AccountStatus) String() string {
	switch s {
	case Active:
		return "ACTIVE"
	case Closed:
		return "CLOSED"
	default:
		return "INVALID"
	}
}

// ParseAccountStatus resolves the wire representation.
func ParseAccountStatus(s string) (AccountStatus, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ACTIVE":
		return Active, nil
	case "CLOSED":
		return Closed, nil
	default:
		return 0, fmt.Errorf("ledger: invalid account status: %q", s)
	}
}
