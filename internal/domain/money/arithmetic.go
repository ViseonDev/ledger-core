package money

import "math"

// Add adds two money values.
func (m Money) Add(other Money) (Money, error) {
	if err := m.assertSameCurrency(other); err != nil {
		return Money{}, err
	}
	if addOverflows(m.amount, other.amount) {
		return Money{}, ErrOverflow
	}
	return Money{amount: m.amount + other.amount, currency: m.currency}, nil
}

// Sub subtracts two money values.
func (m Money) Sub(other Money) (Money, error) {
	if err := m.assertSameCurrency(other); err != nil {
		return Money{}, err
	}
	if subOverflows(m.amount, other.amount) {
		return Money{}, ErrOverflow
	}
	return Money{amount: m.amount - other.amount, currency: m.currency}, nil
}

// Mul multiplies m by an integer factor.
func (m Money) Mul(factor int64) (Money, error) {
	if mulOverflows(m.amount, factor) {
		return Money{}, ErrOverflow
	}
	return Money{amount: m.amount * factor, currency: m.currency}, nil
}

// Neg negates m.
func (m Money) Neg() (Money, error) {
	if m.amount == math.MinInt64 {
		return Money{}, ErrOverflow
	}
	return Money{amount: -m.amount, currency: m.currency}, nil
}

// Abs returns the absolute value of m.
func (m Money) Abs() (Money, error) {
	if m.amount >= 0 {
		return m, nil
	}
	return m.Neg()
}

// Compare compares m to other.
// Returns -1 if m < other,
// 0 if m == other,
// 1 if m > other.
func (m Money) Compare(other Money) (int, error) {
	if err := m.assertSameCurrency(other); err != nil {
		return 0, err
	}
	switch {
	case m.amount < other.amount:
		return -1, nil
	case m.amount > other.amount:
		return 1, nil
	default:
		return 0, nil
	}
}

// Sum adds any number of values, all of which must be in the given currency.
func Sum(currency Currency, values ...Money) (Money, error) {
	total, err := Zero(currency)
	if err != nil {
		return Money{}, err
	}
	for _, v := range values {
		total, err = total.Add(v)
		if err != nil {
			return Money{}, err
		}
	}
	return total, nil
}

// assertSameCurrency checks if two money values have the same currency.
func (m Money) assertSameCurrency(other Money) error {
	if !m.currency.IsValid() || !other.currency.IsValid() {
		return ErrInvalidCurrency
	}
	if m.currency != other.currency {
		return CurrencyMismatchError{Left: m.currency, Right: other.currency}
	}
	return nil
}

// addOverflows reports whether a+b leaves the int64 range.
func addOverflows(a, b int64) bool {
	if b > 0 && a > math.MaxInt64-b {
		return true
	}
	if b < 0 && a < math.MinInt64-b {
		return true
	}
	return false
}

// subOverflows reports whether a-b leaves the int64 range.
func subOverflows(a, b int64) bool {
	if b < 0 && a > math.MaxInt64+b {
		return true
	}
	if b > 0 && a < math.MinInt64+b {
		return true
	}
	return false
}

// mulOverflows reports whether a*b leaves the int64 range.
func mulOverflows(a, b int64) bool {
	if a == 0 || b == 0 {
		return false
	}
	if (a == math.MinInt64 && b == -1) || (b == math.MinInt64 && a == -1) {
		return true
	}
	return (a*b)/b != a
}
