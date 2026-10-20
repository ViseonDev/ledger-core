package ledger

import (
	"errors"
	"testing"

	"github.com/ViseonDev/ledger-core/internal/domain/money"
)

const (
	pendingIDText  = "0198f3c1-7a4e-7c2b-9f31-6d8a2b4c5e11"
	merchantIDText = "0198f3c1-7a4e-7c2b-9f31-6d8a2b4c5e12"
	revenueIDText  = "0198f3c1-7a4e-7c2b-9f31-6d8a2b4c5e13"
)

func TestNewEntry(t *testing.T) {
	accountID := mustAccountID(t, walletIDText)
	amount := mustEUR(t, 25000)

	entry, err := NewEntry(accountID, Debit, amount)
	if err != nil {
		t.Fatalf("NewEntry returned unexpected error: %v", err)
	}

	if entry.AccountID() != accountID {
		t.Errorf("AccountID() = %q, want %q", entry.AccountID(), accountID)
	}
	if entry.Direction() != Debit {
		t.Errorf("Direction() = %v, want Debit", entry.Direction())
	}
	if entry.Amount() != amount {
		t.Errorf("Amount() = %v, want %v", entry.Amount(), amount)
	}
	if entry.Currency() != money.EUR {
		t.Errorf("Currency() = %v, want EUR", entry.Currency())
	}
}

func TestNewEntryValidation(t *testing.T) {
	accountID := mustAccountID(t, walletIDText)

	tests := []struct {
		name      string
		accountID AccountID
		direction Direction
		amount    money.Money
		wantErr   error
	}{
		{
			name:      "missing account",
			accountID: "",
			direction: Debit,
			amount:    mustEUR(t, 100),
			wantErr:   ErrInvalidID,
		},
		{
			name:      "invalid direction",
			accountID: accountID,
			direction: Direction(0),
			amount:    mustEUR(t, 100),
			wantErr:   ErrInvalidDirection,
		},
		{
			// The sign is carried by Direction (Debit or Credit), so a negative amount makes no sense
			// and would create ambiguous double negatives.
			name:      "negative amount",
			accountID: accountID,
			direction: Debit,
			amount:    mustEUR(t, -100),
			wantErr:   ErrNonPositiveAmount,
		},
		{
			// A 0 amount entry moves nothing and balances trivially.
			// Rejecting it prevents silly bugs where an uninitialized variable is used.
			name:      "zero amount",
			accountID: accountID,
			direction: Debit,
			amount:    mustEUR(t, 0),
			wantErr:   ErrNonPositiveAmount,
		},
		{
			name:      "invalid currency",
			accountID: accountID,
			direction: Debit,
			amount:    money.Money{},
			wantErr:   money.ErrInvalidCurrency,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewEntry(tt.accountID, tt.direction, tt.amount)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewEntry() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestEntryReverse(t *testing.T) {
	entry, err := NewEntry(mustAccountID(t, walletIDText), Debit, mustEUR(t, 25000))
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}

	reversed := entry.Reverse()

	if reversed.Direction() != Credit {
		t.Errorf("Reverse().Direction() = %v, want Credit", reversed.Direction())
	}
	if reversed.AccountID() != entry.AccountID() {
		t.Errorf("Reverse() changed the account: %q", reversed.AccountID())
	}
	if reversed.Amount() != entry.Amount() {
		t.Errorf("Reverse() changed the amount: %v", reversed.Amount())
	}
	if entry.Direction() != Debit {
		t.Error("Reverse() mutated the original entry")
	}
	if reversed.Reverse() != entry {
		t.Error("reversing twice did not return the original entry")
	}
}

func TestEntrySignedAmount(t *testing.T) {
	tests := []struct {
		name        string
		accountType AccountType
		direction   Direction
		amount      int64
		want        int64
	}{
		{name: "debit on an asset increases it", accountType: Asset, direction: Debit, amount: 500, want: 500},
		{name: "credit on an asset decreases it", accountType: Asset, direction: Credit, amount: 500, want: -500},
		{name: "debit on a liability decreases it", accountType: Liability, direction: Debit, amount: 500, want: -500},
		{name: "credit on a liability increases it", accountType: Liability, direction: Credit, amount: 500, want: 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry, err := NewEntry(mustAccountID(t, walletIDText), tt.direction, mustEUR(t, tt.amount))
			if err != nil {
				t.Fatalf("NewEntry: %v", err)
			}

			got, err := entry.SignedAmount(tt.accountType)
			if err != nil {
				t.Fatalf("SignedAmount returned unexpected error: %v", err)
			}
			if got.Amount() != tt.want {
				t.Errorf("SignedAmount(%v) = %v, want %d", tt.accountType, got, tt.want)
			}
		})
	}
}

// Entry needs to be comparable so == works directly and we can use it as a map key later when grouping entries.
func TestEntryIsComparable(t *testing.T) {
	a, err := NewEntry(mustAccountID(t, walletIDText), Debit, mustEUR(t, 100))
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	b, err := NewEntry(mustAccountID(t, walletIDText), Debit, mustEUR(t, 100))
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	if a != b {
		t.Error("identical entries compare unequal")
	}
}
