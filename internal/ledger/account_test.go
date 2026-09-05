package ledger

import (
	"errors"
	"testing"

	"github.com/ViseonDev/ledger-core/internal/domain/money"
)

func mustAccountID(t *testing.T, s string) AccountID {
	t.Helper()
	id, err := ParseAccountID(s)
	if err != nil {
		t.Fatalf("ParseAccountID(%q): %v", s, err)
	}
	return id
}

func mustAccountCode(t *testing.T, s string) AccountCode {
	t.Helper()
	code, err := ParseAccountCode(s)
	if err != nil {
		t.Fatalf("ParseAccountCode(%q): %v", s, err)
	}
	return code
}

const (
	walletIDText   = "0198f3c1-7a4e-7c2b-9f31-6d8a2b4c5e10"
	walletCodeText = "liabilities:customer:wallet:cus_demo_0001"
)

func newWallet(t *testing.T) Account {
	t.Helper()
	account, err := NewAccount(AccountParams{
		ID:            mustAccountID(t, walletIDText),
		Code:          mustAccountCode(t, walletCodeText),
		Name:          "Demo customer wallet",
		Type:          Liability,
		Currency:      money.EUR,
		AllowNegative: false,
	})
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	return account
}

func TestNewAccount(t *testing.T) {
	account := newWallet(t)

	if account.ID().String() != walletIDText {
		t.Errorf("ID() = %q, want %q", account.ID(), walletIDText)
	}
	if account.Code().String() != walletCodeText {
		t.Errorf("Code() = %q, want %q", account.Code(), walletCodeText)
	}
	if account.Type() != Liability {
		t.Errorf("Type() = %v, want Liability", account.Type())
	}
	if account.Currency() != money.EUR {
		t.Errorf("Currency() = %v, want EUR", account.Currency())
	}
	if account.NormalBalance() != Credit {
		t.Errorf("NormalBalance() = %v, want Credit", account.NormalBalance())
	}
	if account.Status() != Active {
		t.Errorf("Status() = %v, want Active", account.Status())
	}
	if account.AllowsNegative() {
		t.Error("AllowsNegative() = true, want false")
	}
}

func TestNewAccountValidation(t *testing.T) {
	valid := AccountParams{
		ID:       mustAccountID(t, walletIDText),
		Code:     mustAccountCode(t, walletCodeText),
		Name:     "Demo customer wallet",
		Type:     Liability,
		Currency: money.EUR,
	}

	tests := []struct {
		name    string
		mutate  func(p *AccountParams)
		wantErr error
	}{
		{
			name:    "missing id",
			mutate:  func(p *AccountParams) { p.ID = "" },
			wantErr: ErrInvalidID,
		},
		{
			name:    "missing code",
			mutate:  func(p *AccountParams) { p.Code = "" },
			wantErr: ErrInvalidAccountCode,
		},
		{
			name:    "missing name",
			mutate:  func(p *AccountParams) { p.Name = "   " },
			wantErr: ErrInvalidAccountName,
		},
		{
			name:    "invalid type",
			mutate:  func(p *AccountParams) { p.Type = AccountType(99) },
			wantErr: ErrInvalidAccountType,
		},
		{
			name:    "invalid currency",
			mutate:  func(p *AccountParams) { p.Currency = money.Currency{} },
			wantErr: money.ErrInvalidCurrency,
		},
		{
			name: "code root disagrees with type",
			// A liability account should not be able to have an asset code
			mutate:  func(p *AccountParams) { p.Code = mustAccountCode(t, "assets:customer:wallet:cus_demo_0001") },
			wantErr: ErrAccountCodeTypeMismatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := valid
			tt.mutate(&params)

			_, err := NewAccount(params)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewAccount() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestAccountClose(t *testing.T) {
	account := newWallet(t)

	closed, err := account.Close()
	if err != nil {
		t.Fatalf("Close() returned unexpected error: %v", err)
	}
	if closed.Status() != Closed {
		t.Errorf("Status() = %v, want Closed", closed.Status())
	}

	// Ensures that the original account is not modified by the close operation.
	if account.Status() != Active {
		t.Errorf("original account was mutated: Status() = %v, want Active", account.Status())
	}

	if _, err := closed.Close(); !errors.Is(err, ErrAccountClosed) {
		t.Fatalf("closing twice error = %v, want ErrAccountClosed", err)
	}
}

func TestAccountAcceptsEntry(t *testing.T) {
	wallet := newWallet(t)

	t.Run("accepts matching currency", func(t *testing.T) {
		amount, err := money.New(100, money.EUR)
		if err != nil {
			t.Fatalf("money.New: %v", err)
		}
		if err := wallet.AcceptsAmount(amount); err != nil {
			t.Errorf("AcceptsAmount returned unexpected error: %v", err)
		}
	})

	t.Run("rejects mismatched currency", func(t *testing.T) {
		amount, err := money.New(100, money.CHF)
		if err != nil {
			t.Fatalf("money.New: %v", err)
		}
		if err := wallet.AcceptsAmount(amount); !errors.Is(err, money.ErrCurrencyMismatch) {
			t.Errorf("AcceptsAmount error = %v, want ErrCurrencyMismatch", err)
		}
	})

	t.Run("rejects postings to a closed account", func(t *testing.T) {
		closed, err := wallet.Close()
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
		amount, err := money.New(100, money.EUR)
		if err != nil {
			t.Fatalf("money.New: %v", err)
		}
		if err := closed.AcceptsAmount(amount); !errors.Is(err, ErrAccountClosed) {
			t.Errorf("AcceptsAmount error = %v, want ErrAccountClosed", err)
		}
	})
}

func TestAccountRename(t *testing.T) {
	account := newWallet(t)

	renamed, err := account.Rename("Primary wallet")
	if err != nil {
		t.Fatalf("Rename returned unexpected error: %v", err)
	}
	if renamed.Name() != "Primary wallet" {
		t.Errorf("Name() = %q, want %q", renamed.Name(), "Primary wallet")
	}
	if account.Name() != "Demo customer wallet" {
		t.Errorf("original was mutated: Name() = %q", account.Name())
	}

	if _, err := account.Rename("  "); !errors.Is(err, ErrInvalidAccountName) {
		t.Fatalf("Rename(blank) error = %v, want ErrInvalidAccountName", err)
	}
}
