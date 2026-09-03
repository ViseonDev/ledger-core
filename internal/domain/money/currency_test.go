package money

import "testing"

// TestParseCurrency tests the ParseCurrency function.
func TestParseCurrency(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantCode     string
		wantExponent int
		wantErr      bool
	}{
		{name: "well known currency", input: "EUR", wantCode: "EUR", wantExponent: 2},
		{name: "lowercase is normalised", input: "usd", wantCode: "USD", wantExponent: 2},
		{name: "surrounding space is trimmed", input: "  gbp  ", wantCode: "GBP", wantExponent: 2},
		{name: "zero decimal currency", input: "JPY", wantCode: "JPY", wantExponent: 0},
		{name: "three decimal currency", input: "BHD", wantCode: "BHD", wantExponent: 3},
		{name: "unknown code is rejected", input: "XYZ", wantErr: true},
		{name: "empty string is rejected", input: "", wantErr: true},
		{name: "not a currency at all", input: "banana", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCurrency(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseCurrency(%q) = %v, want an error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCurrency(%q) returned unexpected error: %v", tt.input, err)
			}
			if got.Code() != tt.wantCode {
				t.Errorf("Code() = %q, want %q", got.Code(), tt.wantCode)
			}
			if got.Exponent() != tt.wantExponent {
				t.Errorf("Exponent() = %d, want %d", got.Exponent(), tt.wantExponent)
			}
		})
	}
}

// TestZeroCurrencyIsInvalid tests that the zero Currency is not valid.
func TestZeroCurrencyIsInvalid(t *testing.T) {
	var c Currency

	if c.IsValid() {
		t.Error("the zero Currency reports itself valid; it must not")
	}
	if c.Code() != "" {
		t.Errorf("zero Currency Code() = %q, want empty string", c.Code())
	}
}
