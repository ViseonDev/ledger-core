package ledger

import (
	"errors"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/ViseonDev/ledger-core/internal/domain/money"
)

const txIDText = "0198f3c1-7a4e-7c2b-9f31-6d8a2b4c5e20"

func mustTransactionID(t *testing.T, s string) TransactionID {
	t.Helper()
	id, err := ParseTransactionID(s)
	if err != nil {
		t.Fatalf("ParseTransactionID(%q): %v", s, err)
	}
	return id
}

func mustEntry(t *testing.T, accountIDText string, direction Direction, amount int64) Entry {
	t.Helper()
	entry, err := NewEntry(mustAccountID(t, accountIDText), direction, mustEUR(t, amount))
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	return entry
}

var postedAt = time.Date(2026, time.August, 17, 9, 30, 0, 0, time.UTC)

// A 250.00 EUR payment with a 7.50 EUR fee split:
// - Customer wallet is debited 250.00 EUR (25000 cents)
// - Merchant account is credited 242.50 EUR (24250 cents)
// - Fee revenue account is credited 7.50 EUR (750 cents)
// Total debits (25000) == Total credits (24250 + 750 = 25000) -> balanced
func balancedEntries(t *testing.T) []Entry {
	t.Helper()
	return []Entry{
		mustEntry(t, walletIDText, Debit, 25000),
		mustEntry(t, merchantIDText, Credit, 24250),
		mustEntry(t, revenueIDText, Credit, 750),
	}
}

func TestNewTransaction(t *testing.T) {
	tx, err := NewTransaction(TransactionParams{
		ID:        mustTransactionID(t, txIDText),
		Reference: "order_4471",
		Entries:   balancedEntries(t),
		PostedAt:  postedAt,
		Metadata:  map[string]string{"source": "checkout"},
	})
	if err != nil {
		t.Fatalf("NewTransaction returned unexpected error: %v", err)
	}

	if tx.ID().String() != txIDText {
		t.Errorf("ID() = %q, want %q", tx.ID(), txIDText)
	}
	if tx.Reference() != "order_4471" {
		t.Errorf("Reference() = %q, want %q", tx.Reference(), "order_4471")
	}
	if tx.Currency() != money.EUR {
		t.Errorf("Currency() = %v, want EUR", tx.Currency())
	}
	if tx.EntryCount() != 3 {
		t.Errorf("EntryCount() = %d, want 3", tx.EntryCount())
	}
	if !tx.PostedAt().Equal(postedAt) {
		t.Errorf("PostedAt() = %v, want %v", tx.PostedAt(), postedAt)
	}
	if tx.IsReversal() {
		t.Error("IsReversal() = true for a fresh transaction")
	}

	total, err := tx.TotalDebits()
	if err != nil {
		t.Fatalf("TotalDebits: %v", err)
	}
	if total.Amount() != 25000 {
		t.Errorf("TotalDebits() = %v, want EUR 250.00", total)
	}
}

// The error should show both totals.
func TestNewTransactionRejectsUnbalanced(t *testing.T) {
	_, err := NewTransaction(TransactionParams{
		ID:       mustTransactionID(t, txIDText),
		PostedAt: postedAt,
		Entries: []Entry{
			mustEntry(t, walletIDText, Debit, 25000),
			mustEntry(t, merchantIDText, Credit, 24000), // missing 1000 cents
		},
	})

	if !errors.Is(err, ErrUnbalanced) {
		t.Fatalf("NewTransaction() error = %v, want ErrUnbalanced", err)
	}
	if !strings.Contains(err.Error(), "25000") || !strings.Contains(err.Error(), "24000") {
		t.Errorf("error message does not report both totals: %v", err)
	}
}

func TestNewTransactionValidation(t *testing.T) {
	tests := []struct {
		name    string
		params  TransactionParams
		wantErr error
	}{
		{
			name: "no id",
			params: TransactionParams{
				Entries:  balancedEntries(t),
				PostedAt: postedAt,
			},
			wantErr: ErrInvalidID,
		},
		{
			name: "no entries",
			params: TransactionParams{
				ID:       mustTransactionID(t, txIDText),
				PostedAt: postedAt,
			},
			wantErr: ErrTooFewEntries,
		},
		{
			// One entry can never balance with anything else (unless it's zero, which NewEntry already rejects).
			name: "one entry",
			params: TransactionParams{
				ID:       mustTransactionID(t, txIDText),
				PostedAt: postedAt,
				Entries:  []Entry{mustEntry(t, walletIDText, Debit, 100)},
			},
			wantErr: ErrTooFewEntries,
		},
		{
			name: "zero posted time",
			params: TransactionParams{
				ID:      mustTransactionID(t, txIDText),
				Entries: balancedEntries(t),
			},
			wantErr: ErrInvalidPostedAt,
		},
		{
			// Both debits: sum of debits = 200, sum of credits = 0
			name: "all debits",
			params: TransactionParams{
				ID:       mustTransactionID(t, txIDText),
				PostedAt: postedAt,
				Entries: []Entry{
					mustEntry(t, walletIDText, Debit, 100),
					mustEntry(t, merchantIDText, Debit, 100),
				},
			},
			wantErr: ErrUnbalanced,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTransaction(tt.params)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewTransaction() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// Every entry in a transaction must share the same currency. Cross-currency operations
// are handled by two separate transactions linked via an exchange account.
func TestNewTransactionRejectsMixedCurrency(t *testing.T) {
	// Gotta use USD, Macron and friends fkd EUR economy on this oct 17th :'( #MacronDestitution
	usd, err := money.New(25000, money.USD)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}
	usdEntry, err := NewEntry(mustAccountID(t, merchantIDText), Credit, usd)
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}

	_, err = NewTransaction(TransactionParams{
		ID:       mustTransactionID(t, txIDText),
		PostedAt: postedAt,
		Entries: []Entry{
			mustEntry(t, walletIDText, Debit, 25000), // in EUR
			usdEntry,                                 // in USD
		},
	})

	if !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("NewTransaction() error = %v, want ErrCurrencyMismatch", err)
	}
}

// Changing the slice passed in, or the one returned, must not change the transaction.
func TestTransactionEntriesAreDefensivelyCopied(t *testing.T) {
	entries := balancedEntries(t)

	tx, err := NewTransaction(TransactionParams{
		ID:       mustTransactionID(t, txIDText),
		PostedAt: postedAt,
		Entries:  entries,
	})
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}

	// Mutating the slice we passed in should not affect the transaction
	entries[0] = mustEntry(t, walletIDText, Credit, 999999)
	if tx.Entries()[0].Direction() != Debit {
		t.Error("mutating the caller's slice changed the transaction entries")
	}

	// Mutating the slice returned by Entries() should not affect internal state
	got := tx.Entries()
	got[0] = mustEntry(t, walletIDText, Credit, 999999)
	if tx.Entries()[0].Direction() != Debit {
		t.Error("mutating the returned slice changed the transaction entries")
	}
}

func TestTransactionMetadata(t *testing.T) {
	t.Run("is copied on the way in and out", func(t *testing.T) {
		metadata := map[string]string{"source": "checkout"}

		tx, err := NewTransaction(TransactionParams{
			ID:       mustTransactionID(t, txIDText),
			PostedAt: postedAt,
			Entries:  balancedEntries(t),
			Metadata: metadata,
		})
		if err != nil {
			t.Fatalf("NewTransaction: %v", err)
		}

		metadata["source"] = "tampered"
		if tx.Metadata()["source"] != "checkout" {
			t.Error("mutating the caller's map changed the transaction")
		}

		tx.Metadata()["source"] = "tampered"
		if tx.Metadata()["source"] != "checkout" {
			t.Error("mutating the returned map changed the transaction")
		}
	})

	t.Run("is size limited", func(t *testing.T) {
		tooMany := make(map[string]string, MaxMetadataKeys+1)
		for i := 0; i <= MaxMetadataKeys; i++ {
			tooMany[string(rune('a'+i))+"key"] = "value"
		}

		_, err := NewTransaction(TransactionParams{
			ID:       mustTransactionID(t, txIDText),
			PostedAt: postedAt,
			Entries:  balancedEntries(t),
			Metadata: tooMany,
		})
		if !errors.Is(err, ErrInvalidMetadata) {
			t.Fatalf("NewTransaction() error = %v, want ErrInvalidMetadata", err)
		}
	})

	t.Run("rejects an oversized value", func(t *testing.T) {
		_, err := NewTransaction(TransactionParams{
			ID:       mustTransactionID(t, txIDText),
			PostedAt: postedAt,
			Entries:  balancedEntries(t),
			Metadata: map[string]string{"blob": strings.Repeat("x", MaxMetadataValueLength+1)},
		})
		if !errors.Is(err, ErrInvalidMetadata) {
			t.Fatalf("NewTransaction() error = %v, want ErrInvalidMetadata", err)
		}
	})
}

func TestTransactionPostedAtIsNormalisedToUTC(t *testing.T) {
	paris := time.FixedZone("CEST", 2*60*60)
	local := time.Date(2026, time.August, 17, 11, 30, 0, 0, paris)

	tx, err := NewTransaction(TransactionParams{
		ID:       mustTransactionID(t, txIDText),
		PostedAt: local,
		Entries:  balancedEntries(t),
	})
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}

	if got := tx.PostedAt().Location(); got != time.UTC {
		t.Errorf("PostedAt() location = %v, want UTC", got)
	}
	if !tx.PostedAt().Equal(local) {
		t.Errorf("normalising to UTC changed the instant: %v vs %v", tx.PostedAt(), local)
	}
}

func TestTransactionAccountIDs(t *testing.T) {
	tx, err := NewTransaction(TransactionParams{
		ID:       mustTransactionID(t, txIDText),
		PostedAt: postedAt,
		Entries: []Entry{
			mustEntry(t, walletIDText, Debit, 300),
			mustEntry(t, merchantIDText, Credit, 100),
			// Touching the same account multiple times is valid (e.g. separate fee components).
			// It must still appear only once in AccountIDs().
			mustEntry(t, merchantIDText, Credit, 200),
		},
	})
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}

	ids := tx.AccountIDs()
	if len(ids) != 2 {
		t.Fatalf("AccountIDs() returned %d ids, want 2: %v", len(ids), ids)
	}
	// Must be sorted, see AccountIDs.
	for i := 1; i < len(ids); i++ {
		if ids[i-1] >= ids[i] {
			t.Errorf("AccountIDs() is not sorted ascending: %v", ids)
		}
	}
}

// Random property test: 2000 entry sets, half balanced, half not.
func TestTransactionBalanceInvariant(t *testing.T) {
	// Fixed seed so failures are reproducible.
	rng := rand.New(rand.NewPCG(0x5EEDC0FFEE, 0x1ED6E12)) // #nosec G404 -- pseudo-random generator with fixed seed for property test

	const iterations = 2000
	accounts := []string{walletIDText, pendingIDText, merchantIDText, revenueIDText}

	balancedAccepted, unbalancedRejected := 0, 0

	for i := range iterations {
		entryCount := 2 + rng.IntN(5) // between 2 and 6 entries

		entries := make([]Entry, 0, entryCount)
		var debitTotal, creditTotal int64

		// Generate all entries except the last one randomly.
		for j := 0; j < entryCount-1; j++ {
			amount := int64(1 + rng.IntN(100_000))
			direction := Debit
			if rng.IntN(2) == 0 {
				direction = Credit
			}
			if direction == Debit {
				debitTotal += amount
			} else {
				creditTotal += amount
			}
			entries = append(entries, mustEntry(t, accounts[rng.IntN(len(accounts))], direction, amount))
		}

		// Calculate what is needed to balance the entries.
		gap := debitTotal - creditTotal
		balancingDirection := Credit
		if gap < 0 {
			balancingDirection = Debit
			gap = -gap
		}
		if gap == 0 {
			// By luck, random entries balanced. Since 0 amount entries are illegal,
			// add an offsetting pair of 1 cent.
			entries = append(entries,
				mustEntry(t, accounts[0], Debit, 1),
				mustEntry(t, accounts[1], Credit, 1),
			)
		} else {
			entries = append(entries, mustEntry(t, accounts[rng.IntN(len(accounts))], balancingDirection, gap))
		}

		shouldBalance := i%2 == 0
		if !shouldBalance {
			// Intentionally break the balance by adding 1..999 cents to the last entry.
			delta := int64(1 + rng.IntN(999))
			last := entries[len(entries)-1]
			entries[len(entries)-1] = mustEntry(t,
				last.AccountID().String(),
				last.Direction(),
				last.Amount().Amount()+delta,
			)
		}

		_, err := NewTransaction(TransactionParams{
			ID:       mustTransactionID(t, txIDText),
			PostedAt: postedAt,
			Entries:  entries,
		})

		if shouldBalance {
			if err != nil {
				t.Fatalf("iteration %d: balanced entries were rejected: %v\nentries: %v", i, err, entries)
			}
			balancedAccepted++
		} else {
			if !errors.Is(err, ErrUnbalanced) {
				t.Fatalf("iteration %d: unbalanced entries were accepted (err = %v)\nentries: %v", i, err, entries)
			}
			unbalancedRejected++
		}
	}

	if balancedAccepted == 0 || unbalancedRejected == 0 {
		t.Fatalf("property test degenerate: %d balanced, %d unbalanced", balancedAccepted, unbalancedRejected)
	}
	t.Logf("balance invariant held over %d cases (%d balanced, %d unbalanced)", iterations, balancedAccepted, unbalancedRejected)
}
