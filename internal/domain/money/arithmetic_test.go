package money

import (
	"errors"
	"math"
	"testing"
)

/*
	int64 holds 2^63 - 1 up to -2^63 ~ 9 quintillion, it's safe to assume that it will never overflow
	until funny guy tests with math.MaxInt64 +1
	In banking we assume nothing is safe, overflows should fail loudly rather than wrap silently.
*/

// mustMoney is a test helper. Marking it with t.Helper() means a failure inside
// it is reported at the caller's line, which is where the useful context is.
func mustMoney(t *testing.T, amount int64, c Currency) Money {
	t.Helper()
	m, err := New(amount, c)
	if err != nil {
		t.Fatalf("New(%d, %v): %v", amount, c, err)
	}
	return m
}

func TestAdd(t *testing.T) {
	t.Run("same currency", func(t *testing.T) {
		a := mustMoney(t, 25000, EUR)
		b := mustMoney(t, 750, EUR)

		got, err := a.Add(b)
		if err != nil {
			t.Fatalf("Add returned unexpected error: %v", err)
		}
		want := mustMoney(t, 25750, EUR)
		if got != want {
			t.Errorf("Add() = %v, want %v", got, want)
		}
	})

	t.Run("mixed currency is rejected", func(t *testing.T) {
		a := mustMoney(t, 100, EUR)
		b := mustMoney(t, 100, CHF)

		_, err := a.Add(b)
		if !errors.Is(err, ErrCurrencyMismatch) {
			t.Fatalf("Add() error = %v, want ErrCurrencyMismatch", err)
		}

		var mismatch CurrencyMismatchError
		if !errors.As(err, &mismatch) {
			t.Fatalf("Add() error is not a CurrencyMismatchError: %v", err)
		}
		if mismatch.Left != EUR || mismatch.Right != CHF {
			t.Errorf("mismatch = {%v, %v}, want {EUR, CHF}", mismatch.Left, mismatch.Right)
		}
	})

	t.Run("positive overflow is rejected", func(t *testing.T) {
		a := mustMoney(t, math.MaxInt64, EUR)
		b := mustMoney(t, 1, EUR)

		if _, err := a.Add(b); !errors.Is(err, ErrOverflow) {
			t.Fatalf("Add() error = %v, want ErrOverflow", err)
		}
	})

	t.Run("negative overflow is rejected", func(t *testing.T) {
		a := mustMoney(t, math.MinInt64, EUR)
		b := mustMoney(t, -1, EUR)

		if _, err := a.Add(b); !errors.Is(err, ErrOverflow) {
			t.Fatalf("Add() error = %v, want ErrOverflow", err)
		}
	})
}

func TestSub(t *testing.T) {
	t.Run("same currency", func(t *testing.T) {
		a := mustMoney(t, 25000, EUR)
		b := mustMoney(t, 750, EUR)

		got, err := a.Sub(b)
		if err != nil {
			t.Fatalf("Sub returned unexpected error: %v", err)
		}
		want := mustMoney(t, 24250, EUR)
		if got != want {
			t.Errorf("Sub() = %v, want %v", got, want)
		}
	})

	t.Run("result may be negative", func(t *testing.T) {
		a := mustMoney(t, 100, EUR)
		b := mustMoney(t, 250, EUR)

		got, err := a.Sub(b)
		if err != nil {
			t.Fatalf("Sub returned unexpected error: %v", err)
		}
		if got.Amount() != -150 {
			t.Errorf("Sub() = %v, want EUR -1.50", got)
		}
	})

	t.Run("mixed currency is rejected", func(t *testing.T) {
		a := mustMoney(t, 100, EUR)
		b := mustMoney(t, 100, JPY)

		if _, err := a.Sub(b); !errors.Is(err, ErrCurrencyMismatch) {
			t.Fatalf("Sub() error = %v, want ErrCurrencyMismatch", err)
		}
	})

	t.Run("overflow is rejected", func(t *testing.T) {
		a := mustMoney(t, math.MinInt64, EUR)
		b := mustMoney(t, 1, EUR)

		if _, err := a.Sub(b); !errors.Is(err, ErrOverflow) {
			t.Fatalf("Sub() error = %v, want ErrOverflow", err)
		}
	})
}

func TestMul(t *testing.T) {
	tests := []struct {
		name    string
		amount  int64
		factor  int64
		want    int64
		wantErr bool
	}{
		{name: "by zero", amount: 999, factor: 0, want: 0},
		{name: "by one", amount: 999, factor: 1, want: 999},
		{name: "positive", amount: 250, factor: 4, want: 1000},
		{name: "negative factor", amount: 250, factor: -2, want: -500},
		{name: "overflow", amount: math.MaxInt64 / 2, factor: 3, wantErr: true},
		{name: "min int by minus one overflows", amount: math.MinInt64, factor: -1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := mustMoney(t, tt.amount, EUR)

			got, err := m.Mul(tt.factor)
			if tt.wantErr {
				if !errors.Is(err, ErrOverflow) {
					t.Fatalf("Mul(%d) error = %v, want ErrOverflow", tt.factor, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Mul(%d) returned unexpected error: %v", tt.factor, err)
			}
			if got.Amount() != tt.want {
				t.Errorf("Mul(%d) = %d, want %d", tt.factor, got.Amount(), tt.want)
			}
		})
	}
}

func TestNeg(t *testing.T) {
	t.Run("flips sign", func(t *testing.T) {
		m := mustMoney(t, 750, EUR)

		got, err := m.Neg()
		if err != nil {
			t.Fatalf("Neg returned unexpected error: %v", err)
		}
		if got.Amount() != -750 {
			t.Errorf("Neg() = %d, want -750", got.Amount())
		}
	})

	t.Run("min int cannot be negated", func(t *testing.T) {
		// -math.MinInt64 is not representable in int64. This is the one case
		// where negation is not total, and pretending otherwise wraps silently.
		m := mustMoney(t, math.MinInt64, EUR)

		if _, err := m.Neg(); !errors.Is(err, ErrOverflow) {
			t.Fatalf("Neg() error = %v, want ErrOverflow", err)
		}
	})
}

func TestCompare(t *testing.T) {
	tests := []struct {
		name  string
		left  int64
		right int64
		want  int
	}{
		{name: "less", left: 100, right: 250, want: -1},
		{name: "equal", left: 250, right: 250, want: 0},
		{name: "greater", left: 250, right: 100, want: 1},
		{name: "negative less than positive", left: -1, right: 1, want: -1},
		{name: "extremes do not overflow", left: math.MinInt64, right: math.MaxInt64, want: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := mustMoney(t, tt.left, EUR)
			b := mustMoney(t, tt.right, EUR)

			got, err := a.Compare(b)
			if err != nil {
				t.Fatalf("Compare returned unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Compare(%d, %d) = %d, want %d", tt.left, tt.right, got, tt.want)
			}
		})
	}

	t.Run("mixed currency is rejected", func(t *testing.T) {
		a := mustMoney(t, 100, EUR)
		b := mustMoney(t, 100, CHF)

		if _, err := a.Compare(b); !errors.Is(err, ErrCurrencyMismatch) {
			t.Fatalf("Compare() error = %v, want ErrCurrencyMismatch", err)
		}
	})
}

func TestSum(t *testing.T) {
	t.Run("empty sum is zero in the given currency", func(t *testing.T) {
		got, err := Sum(EUR)
		if err != nil {
			t.Fatalf("Sum(USD) returned unexpected error: %v", err)
		}
		if !got.IsZero() || got.Currency() != EUR {
			t.Errorf("Sum(USD) = %v, want EUR 0.00", got)
		}
	})

	t.Run("adds several values", func(t *testing.T) {
		got, err := Sum(EUR,
			mustMoney(t, 100, EUR),
			mustMoney(t, 250, EUR),
			mustMoney(t, -50, EUR),
		)
		if err != nil {
			t.Fatalf("Sum returned unexpected error: %v", err)
		}
		if got.Amount() != 300 {
			t.Errorf("Sum() = %d, want 300", got.Amount())
		}
	})

	t.Run("rejects a value in another currency", func(t *testing.T) {
		_, err := Sum(EUR, mustMoney(t, 100, EUR), mustMoney(t, 100, CHF))
		if !errors.Is(err, ErrCurrencyMismatch) {
			t.Fatalf("Sum() error = %v, want ErrCurrencyMismatch", err)
		}
	})

	t.Run("rejects an invalid target currency", func(t *testing.T) {
		if _, err := Sum(Currency{}); !errors.Is(err, ErrInvalidCurrency) {
			t.Fatalf("Sum(Currency{}) error = %v, want ErrInvalidCurrency", err)
		}
	})
}
