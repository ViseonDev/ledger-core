package ledger

import (
	"errors"
	"testing"
	"time"

	"github.com/ViseonDev/ledger-core/internal/domain/money"
)

const reversalIDText = "0198f3c1-7a4e-7c2b-9f31-6d8a2b4c5e21"

func newPayment(t *testing.T) *Transaction {
	t.Helper()
	tx, err := NewTransaction(TransactionParams{
		ID:        mustTransactionID(t, txIDText),
		Reference: "order_4471",
		Entries:   balancedEntries(t),
		PostedAt:  postedAt,
		Metadata:  map[string]string{"source": "checkout"},
	})
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
	return tx
}

func TestNewReversalMirrorsEntries(t *testing.T) {
	original := newPayment(t)
	reversedAt := postedAt.Add(2 * time.Hour)

	reversal, err := NewReversal(original, mustTransactionID(t, reversalIDText), reversedAt, "customer disputed the charge")
	if err != nil {
		t.Fatalf("NewReversal returned unexpected error: %v", err)
	}

	if !reversal.IsReversal() {
		t.Error("IsReversal() = false")
	}
	if reversal.ReversalOf() != original.ID() {
		t.Errorf("ReversalOf() = %q, want %q", reversal.ReversalOf(), original.ID())
	}
	if reversal.Reference() != original.Reference() {
		t.Errorf("Reference() = %q, want %q", reversal.Reference(), original.Reference())
	}
	if reversal.EntryCount() != original.EntryCount() {
		t.Fatalf("EntryCount() = %d, want %d", reversal.EntryCount(), original.EntryCount())
	}

	originalEntries := original.Entries()
	for i, entry := range reversal.Entries() {
		want := originalEntries[i]
		if entry.AccountID() != want.AccountID() {
			t.Errorf("entry %d account = %q, want %q", i, entry.AccountID(), want.AccountID())
		}
		if entry.Amount() != want.Amount() {
			t.Errorf("entry %d amount = %v, want %v", i, entry.Amount(), want.Amount())
		}
		if entry.Direction() != want.Direction().Opposite() {
			t.Errorf("entry %d direction = %v, want %v", i, entry.Direction(), want.Direction().Opposite())
		}
	}

	if reason := reversal.Metadata()[MetadataReversalReason]; reason != "customer disputed the charge" {
		t.Errorf("reversal reason = %q, want the supplied reason", reason)
	}
	if got := reversal.Metadata()[MetadataReversalOf]; got != original.ID().String() {
		t.Errorf("metadata %q = %q, want %q", MetadataReversalOf, got, original.ID())
	}
}

// A reversal is a full transaction, so it has to balance too.
func TestReversalIsBalanced(t *testing.T) {
	original := newPayment(t)

	reversal, err := NewReversal(original, mustTransactionID(t, reversalIDText), postedAt, "correction")
	if err != nil {
		t.Fatalf("NewReversal: %v", err)
	}

	debits, err := reversal.TotalDebits()
	if err != nil {
		t.Fatalf("TotalDebits: %v", err)
	}
	credits, err := reversal.TotalCredits()
	if err != nil {
		t.Fatalf("TotalCredits: %v", err)
	}
	if debits != credits {
		t.Errorf("reversal is unbalanced: debits %v, credits %v", debits, credits)
	}
}

// When you post a transaction and then its reversal, every account's net balance
// should return exactly to zero.
func TestReversalRestoresBalances(t *testing.T) {
	original := newPayment(t)
	reversal, err := NewReversal(original, mustTransactionID(t, reversalIDText), postedAt, "correction")
	if err != nil {
		t.Fatalf("NewReversal: %v", err)
	}

	for _, accountID := range original.AccountIDs() {
		balance, err := NewBalance(accountID, Liability, mustEUR(t, 0), mustEUR(t, 0))
		if err != nil {
			t.Fatalf("NewBalance: %v", err)
		}

		for _, entry := range original.EntriesFor(accountID) {
			balance, err = balance.Apply(entry.Direction(), entry.Amount())
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
		}
		for _, entry := range reversal.EntriesFor(accountID) {
			balance, err = balance.Apply(entry.Direction(), entry.Amount())
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
		}

		normal, err := balance.Normal()
		if err != nil {
			t.Fatalf("Normal: %v", err)
		}
		if !normal.IsZero() {
			t.Errorf("account %s has balance %v after reversal, want zero", accountID, normal)
		}
	}
}

func TestNewReversalValidation(t *testing.T) {
	original := newPayment(t)

	t.Run("nil original", func(t *testing.T) {
		if _, err := NewReversal(nil, mustTransactionID(t, reversalIDText), postedAt, "x"); err == nil {
			t.Fatal("NewReversal(nil) succeeded, want an error")
		}
	})

	t.Run("missing id", func(t *testing.T) {
		if _, err := NewReversal(original, "", postedAt, "x"); !errors.Is(err, ErrInvalidID) {
			t.Fatalf("error = %v, want ErrInvalidID", err)
		}
	})

	t.Run("reversal id must differ from the original", func(t *testing.T) {
		if _, err := NewReversal(original, original.ID(), postedAt, "x"); !errors.Is(err, ErrInvalidID) {
			t.Fatalf("error = %v, want ErrInvalidID", err)
		}
	})

	t.Run("reversal cannot predate the original", func(t *testing.T) {
		earlier := postedAt.Add(-time.Second)
		if _, err := NewReversal(original, mustTransactionID(t, reversalIDText), earlier, "x"); !errors.Is(err, ErrInvalidPostedAt) {
			t.Fatalf("error = %v, want ErrInvalidPostedAt", err)
		}
	})

	t.Run("reason is required", func(t *testing.T) {
		if _, err := NewReversal(original, mustTransactionID(t, reversalIDText), postedAt, "  "); !errors.Is(err, ErrReversalReasonRequired) {
			t.Fatalf("error = %v, want ErrReversalReasonRequired", err)
		}
	})
}

// Rehydrating from storage keeps the reversal link and still checks the balance.
func TestRehydrateTransaction(t *testing.T) {
	original := newPayment(t)

	rehydrated, err := RehydrateTransaction(TransactionParams{
		ID:        mustTransactionID(t, reversalIDText),
		Reference: "order_4471",
		Entries:   balancedEntries(t),
		PostedAt:  postedAt,
	}, original.ID())
	if err != nil {
		t.Fatalf("RehydrateTransaction returned unexpected error: %v", err)
	}

	if !rehydrated.IsReversal() {
		t.Error("IsReversal() = false after rehydrating a reversal")
	}
	if rehydrated.ReversalOf() != original.ID() {
		t.Errorf("ReversalOf() = %q, want %q", rehydrated.ReversalOf(), original.ID())
	}

	t.Run("still enforces balance", func(t *testing.T) {
		_, err := RehydrateTransaction(TransactionParams{
			ID:       mustTransactionID(t, txIDText),
			PostedAt: postedAt,
			Entries: []Entry{
				mustEntry(t, walletIDText, Debit, 25000),
				mustEntry(t, merchantIDText, Credit, 24000),
			},
		}, "")
		if !errors.Is(err, ErrUnbalanced) {
			t.Fatalf("error = %v, want ErrUnbalanced (stored rows are not trusted blindly)", err)
		}
	})

	t.Run("rejects a self-referential reversal", func(t *testing.T) {
		id := mustTransactionID(t, txIDText)
		_, err := RehydrateTransaction(TransactionParams{
			ID:       id,
			PostedAt: postedAt,
			Entries:  balancedEntries(t),
		}, id)
		if !errors.Is(err, ErrInvalidID) {
			t.Fatalf("error = %v, want ErrInvalidID", err)
		}
	})
}

func TestReversalCurrencyIsPreserved(t *testing.T) {
	original := newPayment(t)
	reversal, err := NewReversal(original, mustTransactionID(t, reversalIDText), postedAt, "correction")
	if err != nil {
		t.Fatalf("NewReversal: %v", err)
	}
	if reversal.Currency() != money.EUR {
		t.Errorf("Currency() = %v, want EUR", reversal.Currency())
	}
}
