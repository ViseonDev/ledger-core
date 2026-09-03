// Package money provides the Money value object used throughout the ledger.
package money

import (
	"fmt"
	"strings"
)

// Currency is an ISO 4217 alphabetic code together with the number of decimal
// digits in its minor unit.
type Currency struct {
	code     string
	exponent int
}

// The currencies this ledger supports. The set is deliberately small.
var (
	EUR = Currency{code: "EUR", exponent: 2}
	CHF = Currency{code: "CHF", exponent: 2}
	GBP = Currency{code: "GBP", exponent: 2}
	USD = Currency{code: "USD", exponent: 2}
	AUD = Currency{code: "AUD", exponent: 2}
	JPY = Currency{code: "JPY", exponent: 0}
	KRW = Currency{code: "KRW", exponent: 0}
	BHD = Currency{code: "BHD", exponent: 3}
	KWD = Currency{code: "KWD", exponent: 3}
)

// registry indexes the supported currencies by code. It is built from the
// variables above so the two can never drift apart.
var registry = func() map[string]Currency {
	supported := []Currency{EUR, CHF, GBP, USD, AUD, JPY, KRW, BHD, KWD}
	m := make(map[string]Currency, len(supported))
	for _, c := range supported {
		m[c.code] = c
	}
	return m
}()

// ParseCurrency parses a currency code from a string. It accepts:
// - ISO 4217 codes in uppercase or lowercase
// - optional surrounding whitespace
// If the code is not recognized, ParseCurrency returns a zero Currency
// and an error.
// Invalid values will fail the IsValid() check.
func ParseCurrency(code string) (Currency, error) {
	normalised := strings.ToUpper(strings.TrimSpace(code))

	c, ok := registry[normalised]
	if !ok {
		return Currency{}, fmt.Errorf("%w: %q", ErrInvalidCurrency, code)
	}
	return c, nil
}

// Code returns the ISO 4217 alphabetic code, or the empty string for the zero
// Currency (which is not a valid currency).
func (c Currency) Code() string { return c.code }

// Exponent returns the number of decimal digits in the currency's minor unit
// Ex: 2 for EUR, 0 for JPY, 3 for BHD.
func (c Currency) Exponent() int { return c.exponent }

// IsValid checks if the currency is valid (non-zero and known).
// The zero Currency is not valid.
func (c Currency) IsValid() bool { return c.code != "" }

// String returns the currency code or "<invalid currency>" if the currency is not valid.
func (c Currency) String() string {
	if !c.IsValid() {
		return "<invalid currency>"
	}
	return c.code
}
