package money

import (
	"math"
	"testing"
)

func TestMoneyString(t *testing.T) {
	tests := []struct {
		name     string
		amount   int64
		currency Currency
		want     string
	}{
		{name: "two decimals", amount: 12345, currency: EUR, want: "EUR 123.45"},
		{name: "less than one unit", amount: 5, currency: EUR, want: "EUR 0.05"},
		{name: "exactly one unit", amount: 100, currency: EUR, want: "EUR 1.00"},
		{name: "zero", amount: 0, currency: EUR, want: "EUR 0.00"},
		{name: "negative", amount: -5, currency: EUR, want: "EUR -0.05"},
		{name: "large negative", amount: -123456, currency: EUR, want: "EUR -1234.56"},
		{name: "no decimals", amount: 12345, currency: JPY, want: "JPY 12345"},
		{name: "negative with no decimals", amount: -700, currency: KRW, want: "KRW -700"},
		{name: "three decimals", amount: 1234, currency: BHD, want: "BHD 1.234"},
		{name: "three decimals below one", amount: 7, currency: BHD, want: "BHD 0.007"},
		{name: "max int64", amount: math.MaxInt64, currency: EUR, want: "EUR 92233720368547758.07"},
		{name: "min int64", amount: math.MinInt64, currency: EUR, want: "EUR -92233720368547758.08"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := mustMoney(t, tt.amount, tt.currency)

			if got := m.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestZeroMoneyString(t *testing.T) {
	var m Money

	if got := m.String(); got != "<invalid money>" {
		t.Errorf("String() on zero Money = %q, want %q", got, "<invalid money>")
	}
}
