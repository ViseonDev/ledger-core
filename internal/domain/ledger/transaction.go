package ledger

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/ViseonDev/ledger-core/internal/domain/money"
)

// Errors returned when constructing a transaction.
var (
	// ErrUnbalanced means the total debits and total credits do not match.
	ErrUnbalanced = errors.New("ledger: transaction does not balance")

	// ErrTooFewEntries means a transaction had fewer than two entries.
	// In double-entry, you need at least a debit and a credit.
	ErrTooFewEntries = errors.New("ledger: transaction requires at least two entries")

	// ErrInvalidPostedAt means the posting timestamp was not provided.
	ErrInvalidPostedAt = errors.New("ledger: transaction requires a posting time")

	// ErrInvalidMetadata means the metadata map exceeded limits.
	ErrInvalidMetadata = errors.New("ledger: invalid transaction metadata")
)

// Metadata limits to prevent storing huge blobs and blowing up memory.
const (
	MaxMetadataKeys        = 16
	MaxMetadataKeyLength   = 64
	MaxMetadataValueLength = 256
)

// Transaction represents a balanced set of entries.
// Aggregate root. NewTransaction rejects unbalanced entries.
type Transaction struct {
	id         TransactionID
	reference  string
	currency   money.Currency
	entries    []Entry
	postedAt   time.Time
	reversalOf TransactionID
	metadata   map[string]string
}

// TransactionParams contains the parameters needed to create a new transaction.
type TransactionParams struct {
	ID        TransactionID
	Reference string
	Entries   []Entry
	PostedAt  time.Time
	Metadata  map[string]string
}

// NewTransaction validates entries and builds an immutable, balanced transaction.
func NewTransaction(p TransactionParams) (*Transaction, error) {
	if p.ID.IsZero() {
		return nil, fmt.Errorf("%w: transaction id is required", ErrInvalidID)
	}
	if len(p.Entries) < 2 {
		return nil, fmt.Errorf("%w: got %d", ErrTooFewEntries, len(p.Entries))
	}
	if p.PostedAt.IsZero() {
		return nil, ErrInvalidPostedAt
	}

	currency, err := uniformCurrency(p.Entries)
	if err != nil {
		return nil, err
	}
	if err = assertBalanced(currency, p.Entries); err != nil {
		return nil, err
	}
	metadata, err := validatedMetadata(p.Metadata)
	if err != nil {
		return nil, err
	}

	return &Transaction{
		id:        p.ID,
		reference: p.Reference,
		currency:  currency,
		// Slices are reference types in Go, so cloning prevents callers from modifying entries after validation.
		entries:  slices.Clone(p.Entries),
		postedAt: p.PostedAt.UTC(),
		metadata: metadata,
	}, nil
}

// RehydrateTransaction reconstructs a transaction previously saved in PostgreSQL,
// preserving the reversal link if this transaction reversed an earlier one.
// Stored rows are still checked for balance.
func RehydrateTransaction(p TransactionParams, reversalOf TransactionID) (*Transaction, error) {
	tx, err := NewTransaction(p)
	if err != nil {
		return nil, err
	}
	if !reversalOf.IsZero() {
		if reversalOf == p.ID {
			return nil, fmt.Errorf("%w: stored transaction reverses itself", ErrInvalidID)
		}
		tx.reversalOf = reversalOf
	}
	return tx, nil
}

// ID returns the transaction's identifier.
func (t *Transaction) ID() TransactionID { return t.id }

// Reference returns an external reference like an order ID or payment intent ID.
func (t *Transaction) Reference() string { return t.reference }

// Currency returns the currency shared by all entries in this transaction.
func (t *Transaction) Currency() money.Currency { return t.currency }

// PostedAt returns the posting timestamp in UTC.
func (t *Transaction) PostedAt() time.Time { return t.postedAt }

// EntryCount returns the number of entries.
func (t *Transaction) EntryCount() int { return len(t.entries) }

// Entries returns a defensive copy of the transaction's entries.
func (t *Transaction) Entries() []Entry { return slices.Clone(t.entries) }

// Metadata returns a defensive copy of the metadata map.
func (t *Transaction) Metadata() map[string]string { return maps.Clone(t.metadata) }

// IsReversal reports whether this transaction is reversing a previous one.
func (t *Transaction) IsReversal() bool { return !t.reversalOf.IsZero() }

// ReversalOf returns the ID of the transaction being reversed (or empty if not a reversal).
func (t *Transaction) ReversalOf() TransactionID { return t.reversalOf }

// TotalDebits calculates the sum of all debit entries.
func (t *Transaction) TotalDebits() (money.Money, error) {
	return t.totalFor(Debit)
}

// TotalCredits calculates the sum of all credit entries.
func (t *Transaction) TotalCredits() (money.Money, error) {
	return t.totalFor(Credit)
}

// AccountIDs returns the unique accounts touched by this transaction, sorted in ascending order.
// Postgres locks rows in this order, which avoids deadlocks.
func (t *Transaction) AccountIDs() []AccountID {
	ids := make([]AccountID, 0, len(t.entries))
	for _, entry := range t.entries {
		ids = append(ids, entry.AccountID())
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// EntriesFor returns all entries affecting a specific account.
// An account might appear multiple times in one transaction (e.g. detailed fee breakdowns).
func (t *Transaction) EntriesFor(accountID AccountID) []Entry {
	var out []Entry
	for _, entry := range t.entries {
		if entry.AccountID() == accountID {
			out = append(out, entry)
		}
	}
	return out
}

func (t *Transaction) totalFor(direction Direction) (money.Money, error) {
	amounts := make([]money.Money, 0, len(t.entries))
	for _, entry := range t.entries {
		if entry.Direction() == direction {
			amounts = append(amounts, entry.Amount())
		}
	}
	return money.Sum(t.currency, amounts...)
}

// uniformCurrency checks that every single entry uses the same valid currency.
func uniformCurrency(entries []Entry) (money.Currency, error) {
	currency := entries[0].Currency()
	if !currency.IsValid() {
		return money.Currency{}, money.ErrInvalidCurrency
	}
	for _, entry := range entries[1:] {
		if entry.Currency() != currency {
			return money.Currency{}, money.CurrencyMismatchError{Left: currency, Right: entry.Currency()}
		}
	}
	return currency, nil
}

// assertBalanced checks that sum(debits) == sum(credits).
func assertBalanced(currency money.Currency, entries []Entry) error {
	debits, err := money.Zero(currency)
	if err != nil {
		return err
	}
	credits := debits

	for _, entry := range entries {
		if entry.IsDebit() {
			debits, err = debits.Add(entry.Amount())
		} else {
			credits, err = credits.Add(entry.Amount())
		}
		if err != nil {
			return fmt.Errorf("summing entries: %w", err)
		}
	}

	if debits != credits {
		return fmt.Errorf("%w: debits total %d %s, credits total %d %s",
			ErrUnbalanced,
			debits.Amount(), currency,
			credits.Amount(), currency,
		)
	}
	return nil
}

// validatedMetadata validates keys and values, and returns a cloned non-nil map.
func validatedMetadata(in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return map[string]string{}, nil
	}
	if len(in) > MaxMetadataKeys {
		return nil, fmt.Errorf("%w: %d keys, limit is %d", ErrInvalidMetadata, len(in), MaxMetadataKeys)
	}
	for key, value := range in {
		if key == "" {
			return nil, fmt.Errorf("%w: empty key", ErrInvalidMetadata)
		}
		if len(key) > MaxMetadataKeyLength {
			return nil, fmt.Errorf("%w: key %q exceeds %d bytes", ErrInvalidMetadata, key, MaxMetadataKeyLength)
		}
		if len(value) > MaxMetadataValueLength {
			return nil, fmt.Errorf("%w: value for key %q exceeds %d bytes", ErrInvalidMetadata, key, MaxMetadataValueLength)
		}
	}
	return maps.Clone(in), nil
}
