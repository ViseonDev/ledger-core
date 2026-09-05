package ledger

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidID means an identifier was not a well formet UUID.
var ErrInvalidID = errors.New("ledger: invalid identifier")

// AccountID identifies an account.
type AccountID string

// TransactionID identifies a transaction.
type TransactionID string

// ParseAccountID validates and normalises an account identifier.
func ParseAccountID(s string) (AccountID, error) {
	normalised, err := normaliseUUID(s)
	if err != nil {
		return "", fmt.Errorf("account id: %w", err)
	}
	return AccountID(normalised), nil
}

// ParseTransactionID validates and normalises a transaction identifier.
func ParseTransactionID(s string) (TransactionID, error) {
	normalised, err := normaliseUUID(s)
	if err != nil {
		return "", fmt.Errorf("transaction id: %w", err)
	}
	return TransactionID(normalised), nil
}

// IsZero reports whether the identifier is unset.
func (id AccountID) IsZero() bool { return id == "" }

// String returns the canonical lower-case text form.
func (id AccountID) String() string { return string(id) }

// IsZero reports whether the identifier is unset.
func (id TransactionID) IsZero() bool { return id == "" }

// String returns the canonical lower-case text form.
func (id TransactionID) String() string { return string(id) }

// uuidHyphenPositions is an slice to list the index position of hyphens (tiret du 6 tmtc) "-" in a canonical UUID.
var uuidHyphenPositions = [4]int{8, 13, 18, 23}

// normaliseUUID normalises a UUID string,
// It trims whitespace and converts it to lowercase,
// It also checks for the correct format (length and hyphens).
func normaliseUUID(s string) (string, error) {
	const canonicalLength = 36

	trimmed := strings.TrimSpace(s)
	if len(trimmed) != canonicalLength {
		return "", fmt.Errorf("%w: expected %d characters, got %d", ErrInvalidID, canonicalLength, len(trimmed))
	}
	for _, pos := range uuidHyphenPositions {
		if trimmed[pos] != '-' {
			return "", fmt.Errorf("%w: expected '-' at position %d", ErrInvalidID, pos)
		}
	}
	lowered := strings.ToLower(trimmed)
	for i := 0; i < len(lowered); i++ {
		if isHyphenPosition(i) {
			continue
		}
		if !isHexDigit(lowered[i]) {
			return "", fmt.Errorf("%w: non-hex character %q at position %d", ErrInvalidID, lowered[i], i)
		}
	}
	return lowered, nil
}

func isHyphenPosition(i int) bool {
	for _, pos := range uuidHyphenPositions {
		if i == pos {
			return true
		}
	}
	return false
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}
