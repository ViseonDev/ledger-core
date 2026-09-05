package ledger

import (
	"errors"
	"testing"
)

func TestAccountTypeNormalBalance(t *testing.T) {
	tests := []struct {
		accountType AccountType
		want        Direction
	}{
		{accountType: Asset, want: Debit},
		{accountType: Expense, want: Debit},
		{accountType: Liability, want: Credit},
		{accountType: Equity, want: Credit},
		{accountType: Revenue, want: Credit},
	}

	for _, tt := range tests {
		t.Run(tt.accountType.String(), func(t *testing.T) {
			if got := tt.accountType.NormalBalance(); got != tt.want {
				t.Errorf("%v.NormalBalance() = %v, want %v", tt.accountType, got, tt.want)
			}
		})
	}
}

// TestAccountTypeNormalBalanceIsExhaustive fails if someone adds a sixth
// account type without deciding its normal balance. Without this, a new type
// would silently inherit whatever the default branch returns, and every balance
// on it would have the wrong sign.
func TestAccountTypeNormalBalanceIsExhaustive(t *testing.T) {
	for _, accountType := range AllAccountTypes() {
		normal := accountType.NormalBalance()
		if !normal.IsValid() {
			t.Errorf("%v has no normal balance defined", accountType)
		}
	}
}

func TestAccountTypeSignOf(t *testing.T) {
	tests := []struct {
		name        string
		accountType AccountType
		direction   Direction
		want        int64
	}{
		{name: "debit increases an asset", accountType: Asset, direction: Debit, want: 1},
		{name: "credit decreases an asset", accountType: Asset, direction: Credit, want: -1},
		{name: "debit decreases a liability", accountType: Liability, direction: Debit, want: -1},
		{name: "credit increases a liability", accountType: Liability, direction: Credit, want: 1},
		{name: "debit increases an expense", accountType: Expense, direction: Debit, want: 1},
		{name: "credit increases revenue", accountType: Revenue, direction: Credit, want: 1},
		{name: "debit decreases equity", accountType: Equity, direction: Debit, want: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.accountType.SignOf(tt.direction); got != tt.want {
				t.Errorf("%v.SignOf(%v) = %d, want %d", tt.accountType, tt.direction, got, tt.want)
			}
		})
	}
}

func TestParseAccountType(t *testing.T) {
	tests := []struct {
		input   string
		want    AccountType
		wantErr bool
	}{
		{input: "ASSET", want: Asset},
		{input: "asset", want: Asset},
		{input: " LIABILITY ", want: Liability},
		{input: "EQUITY", want: Equity},
		{input: "REVENUE", want: Revenue},
		{input: "EXPENSE", want: Expense},
		{input: "", wantErr: true},
		{input: "ASSETS", wantErr: true},
		{input: "INCOME", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseAccountType(tt.input)

			if tt.wantErr {
				if !errors.Is(err, ErrInvalidAccountType) {
					t.Fatalf("ParseAccountType(%q) error = %v, want ErrInvalidAccountType", tt.input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAccountType(%q) returned unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseAccountType(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestAccountTypeRoundTrip proves String and ParseAccountType agree, so a value
// written to the database and read back is the same value.
func TestAccountTypeRoundTrip(t *testing.T) {
	for _, accountType := range AllAccountTypes() {
		parsed, err := ParseAccountType(accountType.String())
		if err != nil {
			t.Errorf("ParseAccountType(%q): %v", accountType.String(), err)
			continue
		}
		if parsed != accountType {
			t.Errorf("round trip of %v produced %v", accountType, parsed)
		}
	}
}

func TestAccountTypeMatchesCodeRoot(t *testing.T) {
	tests := []struct {
		accountType AccountType
		root        string
		want        bool
	}{
		{accountType: Asset, root: "assets", want: true},
		{accountType: Liability, root: "liabilities", want: true},
		{accountType: Equity, root: "equity", want: true},
		{accountType: Revenue, root: "revenue", want: true},
		{accountType: Expense, root: "expense", want: true},
		{accountType: Asset, root: "liabilities", want: false},
		{accountType: Revenue, root: "income", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.accountType.String()+"/"+tt.root, func(t *testing.T) {
			if got := tt.accountType.MatchesCodeRoot(tt.root); got != tt.want {
				t.Errorf("%v.MatchesCodeRoot(%q) = %v, want %v", tt.accountType, tt.root, got, tt.want)
			}
		})
	}
}
