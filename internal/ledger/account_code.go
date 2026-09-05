package ledger

import (
	"errors"
	"fmt"
	"strings"
)

// Account Code is basically a string that represents an account. It's used by double entry bookkeeping software to track transactions.
// Format is colon separated values (for example "assets:cash:wallet:EUR")
// The first part is the account type (assets, liabilities, equity, revenue, expense)
// The second part is the account category (cash, wallet, etc...)
// The third part is the account name (EUR, USD, BTC, etc...)
// Being new to ledger system I'm uncertain that this is the best way to represent an account code but that's how I understood it.

// ErrInvalidAccountCode means a code was not a well-formed colon-delimited
// account path.
var ErrInvalidAccountCode = errors.New("ledger: invalid account code")

// AccountCode is the human-readable, hierarchical name of an account, written
// as colon-delimited lower-case segments:
type AccountCode string

// ParseAccountCode validates and normalises an account code.
func ParseAccountCode(s string) (AccountCode, error) {
	normalised := strings.ToLower(strings.TrimSpace(s))
	if normalised == "" {
		return "", fmt.Errorf("%w: empty", ErrInvalidAccountCode)
	}

	segments := strings.Split(normalised, ":")
	if len(segments) < 2 {
		return "", fmt.Errorf("%w: %q has %d segment(s), need at least 2", ErrInvalidAccountCode, s, len(segments))
	}
	for i, segment := range segments {
		if segment == "" {
			return "", fmt.Errorf("%w: %q has an empty segment at position %d", ErrInvalidAccountCode, s, i)
		}
		for j := 0; j < len(segment); j++ {
			if !isCodeChar(segment[j]) {
				return "", fmt.Errorf("%w: %q contains %q, which is not allowed", ErrInvalidAccountCode, s, segment[j])
			}
		}
	}
	return AccountCode(normalised), nil
}

// String returns the code text.
func (c AccountCode) String() string { return string(c) }

// Segments splits the code into its colon-delimited parts.
func (c AccountCode) Segments() []string { return strings.Split(string(c), ":") }

// Root returns the first segment, which is the account's top-level category:
// "assets", "liabilities", "equity", "revenue" or "expense" by convention.
func (c AccountCode) Root() string {
	root, _, _ := strings.Cut(string(c), ":")
	return root
}

// HasPrefix reports whether the code sits at or beneath the given prefix,
// matching whole segments only.
// Ex: assets:cash:wallet:EUR has prefix assets:cash
// But it does not have prefix assets:cashback
func (c AccountCode) HasPrefix(prefix string) bool {
	normalised := strings.ToLower(strings.TrimSpace(prefix))
	if normalised == "" {
		return false
	}
	if string(c) == normalised {
		return true
	}
	return strings.HasPrefix(string(c), normalised+":")
}

// isCodeChar is basically checking if a character is allowed in an account code. Like RegExp: ^[a-z0-9_-]$
func isCodeChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '_' || b == '-':
		return true
	default:
		return false
	}
}
