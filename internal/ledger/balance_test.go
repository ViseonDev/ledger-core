package ledger

import (
	"errors"
	"testing"

	"github.com/ViseonDev/ledger-core/internal/domain/money"
)

func mustEUR(t *testing.T, amount int64) money.Money {
	t.Helper()
	m, err := money.New(amount, money.EUR)
	if err != nil {
		t.Fatalf("money.New(%d, EUR): %v", amount, err)
	}
	return m
}

func TestNewBalance(t *testing.T) {
	tests := []struct {
		name        string
		accountType AccountType
		debits      int64
		credits     int64
		wantNormal  int64
	}{
		// An asset increases with debits, so its balance is debits - credits.
		{name: "asset with net debits", accountType: Asset, debits: 1000, credits: 250, wantNormal: 750},
		{name: "asset overdrawn", accountType: Asset, debits: 100, credits: 400, wantNormal: -300},

		// A liability increases with credits, so its balance is credits - debits.
		{name: "liability with net credits", accountType: Liability, debits: 250, credits: 1000, wantNormal: 750},
		{name: "liability overdrawn", accountType: Liability, debits: 400, credits: 100, wantNormal: -300},

		{name: "revenue", accountType: Revenue, debits: 0, credits: 750, wantNormal: 750},
		{name: "expense", accountType: Expense, debits: 755, credits: 0, wantNormal: 755},
		{name: "empty account", accountType: Asset, debits: 0, credits: 0, wantNormal: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			balance, err := NewBalance(
				mustAccountID(t, walletIDText),
				tt.accountType,
				mustEUR(t, tt.debits),
				mustEUR(t, tt.credits),
			)
			if err != nil {
				t.Fatalf("NewBalance returned unexpected error: %v", err)
			}

			normal, err := balance.Normal()
			if err != nil {
				t.Fatalf("Normal returned unexpected error: %v", err)
			}
			if normal.Amount() != tt.wantNormal {
				t.Errorf("Normal() = %v, want %d minor units", normal, tt.wantNormal)
			}
			if balance.Debits().Amount() != tt.debits {
				t.Errorf("Debits() = %v, want %d", balance.Debits(), tt.debits)
			}
			if balance.Credits().Amount() != tt.credits {
				t.Errorf("Credits() = %v, want %d", balance.Credits(), tt.credits)
			}
		})
	}
}

func TestNewBalanceRejectsMixedCurrency(t *testing.T) {
	debits := mustEUR(t, 100)
	credits, err := money.New(100, money.CHF)
	if err != nil {
		t.Fatalf("money.New: %v", err)
	}

	if _, err := NewBalance(mustAccountID(t, walletIDText), Asset, debits, credits); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("NewBalance error = %v, want ErrCurrencyMismatch", err)
	}
}

func TestNewBalanceRejectsNegativeTotals(t *testing.T) {
	if _, err := NewBalance(mustAccountID(t, walletIDText), Asset, mustEUR(t, -1), mustEUR(t, 0)); err == nil {
		t.Fatal("NewBalance accepted negative debits, want an error")
	}
	if _, err := NewBalance(mustAccountID(t, walletIDText), Asset, mustEUR(t, 0), mustEUR(t, -1)); err == nil {
		t.Fatal("NewBalance accepted negative credits, want an error")
	}
}

func TestBalanceApply(t *testing.T) {
	balance, err := NewBalance(mustAccountID(t, walletIDText), Liability, mustEUR(t, 0), mustEUR(t, 10000))
	if err != nil {
		t.Fatalf("NewBalance: %v", err)
	}

	// A customer spends 25.00: debit the wallet liability.
	after, err := balance.Apply(Debit, mustEUR(t, 2500))
	if err != nil {
		t.Fatalf("Apply returned unexpected error: %v", err)
	}

	normal, err := after.Normal()
	if err != nil {
		t.Fatalf("Normal: %v", err)
	}
	if normal.Amount() != 7500 {
		t.Errorf("after Apply, Normal() = %v, want EUR 75.00", normal)
	}
	if after.Debits().Amount() != 2500 {
		t.Errorf("Debits() = %v, want EUR 25.00", after.Debits())
	}

	// The original is untouched.
	originalNormal, err := balance.Normal()
	if err != nil {
		t.Fatalf("Normal: %v", err)
	}
	if originalNormal.Amount() != 10000 {
		t.Errorf("original balance changed: %v", originalNormal)
	}
}

func TestBalanceWouldGoNegative(t *testing.T) {
	// A wallet holding 100.00.
	balance, err := NewBalance(mustAccountID(t, walletIDText), Liability, mustEUR(t, 0), mustEUR(t, 10000))
	if err != nil {
		t.Fatalf("NewBalance: %v", err)
	}

	tests := []struct {
		name      string
		direction Direction
		amount    int64
		want      bool
	}{
		{name: "spending less than the balance", direction: Debit, amount: 2500, want: false},
		{name: "spending exactly the balance", direction: Debit, amount: 10000, want: false},
		{name: "spending one cent more", direction: Debit, amount: 10001, want: true},
		{name: "receiving money", direction: Credit, amount: 999999, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := balance.WouldGoNegative(tt.direction, mustEUR(t, tt.amount))
			if err != nil {
				t.Fatalf("WouldGoNegative returned unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("WouldGoNegative(%v, %d) = %v, want %v", tt.direction, tt.amount, got, tt.want)
			}
		})
	}
}
