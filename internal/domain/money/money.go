package money

// Money is an exact amount in a single currency, stored as a count of the
// currency's minor unit: 12345 EUR is 123,45€, 12345 JPY is ¥12345.
type Money struct {
	amount   int64
	currency Currency
}

// New returns an amount of minor units in the given currency.
func New(amount int64, currency Currency) (Money, error) {
	if !currency.IsValid() {
		return Money{}, ErrInvalidCurrency
	}
	return Money{amount: amount, currency: currency}, nil
}

// Zero returns the zero amount in the given currency.
func Zero(currency Currency) (Money, error) {
	return New(0, currency)
}

// Amount returns the number of minor units.
func (m Money) Amount() int64 { return m.amount }

// Currency returns the currency.
func (m Money) Currency() Currency { return m.currency }

// IsZero reports whether the amount is exactly zero. It says nothing about the
// currency: Zero(EUR) and Zero(USD) are both zero.
func (m Money) IsZero() bool { return m.amount == 0 }

// IsPositive reports whether the amount is strictly greater than zero.
func (m Money) IsPositive() bool { return m.amount > 0 }

// IsNegative reports whether the amount is strictly less than zero.
func (m Money) IsNegative() bool { return m.amount < 0 }
