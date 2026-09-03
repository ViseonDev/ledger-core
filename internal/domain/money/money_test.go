package money

import (
	"errors"
	"testing"
)

func TestNew(t *testing.T) {
	t.Run("valid currency", func(t *testing.T) {
		m, err := New(12345, EUR)
		if err != nil {
			t.Fatalf("New(12345, EUR) returned unexpected error: %v", err)
		}
		if m.Amount() != 12345 {
			t.Errorf("Amount() = %d, want 12345", m.Amount())
		}
		if m.Currency() != EUR {
			t.Errorf("Currency() = %v, want EUR", m.Currency())
		}
	})

	t.Run("negative amounts are allowed", func(t *testing.T) {
		// The ledger stores positive amounts and carries the sign in the entry
		// direction (Debit/Credit), but Money itself is a general numeric type
		// intermediate results during balance computation are legitimately negative.
		m, err := New(-500, EUR)
		if err != nil {
			t.Fatalf("New(-500, EUR) returned unexpected error: %v", err)
		}
		if !m.IsNegative() {
			t.Error("IsNegative() = false, want true")
		}
	})

	t.Run("zero currency is rejected", func(t *testing.T) {
		_, err := New(100, Currency{})
		if !errors.Is(err, ErrInvalidCurrency) {
			t.Fatalf("New(100, Currency{}) error = %v, want ErrInvalidCurrency", err)
		}
	})
}

func TestZero(t *testing.T) {
	m, err := Zero(JPY)
	if err != nil {
		t.Fatalf("Zero(JPY) returned unexpected error: %v", err)
	}
	if !m.IsZero() {
		t.Errorf("IsZero() = false for %v, want true", m)
	}
	if m.Currency() != JPY {
		t.Errorf("Currency() = %v, want JPY", m.Currency())
	}
}

func TestSignPredicates(t *testing.T) {
	tests := []struct {
		name                            string
		amount                          int64
		wantZero, wantPositive, wantNeg bool
	}{
		{name: "positive", amount: 1, wantPositive: true},
		{name: "zero", amount: 0, wantZero: true},
		{name: "negative", amount: -1, wantNeg: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := New(tt.amount, EUR)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := m.IsZero(); got != tt.wantZero {
				t.Errorf("IsZero() = %v, want %v", got, tt.wantZero)
			}
			if got := m.IsPositive(); got != tt.wantPositive {
				t.Errorf("IsPositive() = %v, want %v", got, tt.wantPositive)
			}
			if got := m.IsNegative(); got != tt.wantNeg {
				t.Errorf("IsNegative() = %v, want %v", got, tt.wantNeg)
			}
		})
	}
}

func TestMoneyIsComparable(t *testing.T) {
	// Money has only comparable fields, so == works and Money can be a map key.
	a := mustMoney(t, 100, EUR)
	b := mustMoney(t, 100, EUR)
	c := mustMoney(t, 100, CHF)
	d := mustMoney(t, 101, EUR)

	if a != b {
		t.Error("two Money values with the same amount and currency compare unequal")
	}
	if a == c {
		t.Error("EUR and CHF amounts compare equal")
	}
	if a == d {
		t.Error("different amounts compare equal")
	}

	counts := map[Money]int{a: 1}
	counts[b]++
	if counts[a] != 2 {
		t.Errorf("Money did not behave as a map key: got %d, want 2", counts[a])
	}
}
