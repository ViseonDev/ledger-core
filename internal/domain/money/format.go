package money

import (
	"math"
	"strconv"
	"strings"
)

// String renders the amount with its currency code, using the currency's minor
// unit exponent to place the decimal point: "EUR 123.45", "JPY 12345",
// "BHD 1.234".
func (m Money) String() string {
	if !m.currency.IsValid() {
		return "<invalid money>"
	}

	exponent := m.currency.exponent
	if exponent == 0 {
		return m.currency.code + " " + strconv.FormatInt(m.amount, 10)
	}

	digits := strconv.FormatUint(absToUint64(m.amount), 10)
	if len(digits) <= exponent {
		digits = strings.Repeat("0", exponent-len(digits)+1) + digits
	}
	split := len(digits) - exponent

	// Builder is a efficient way to build strings.
	var b strings.Builder
	// Reserving capacity reduces the number of memory allocations, improving performance.
	b.Grow(len(m.currency.code) + len(digits) + 3)
	b.WriteString(m.currency.code)
	b.WriteByte(' ')
	if m.amount < 0 {
		b.WriteByte('-')
	}
	// Write the integer part of the amount.
	b.WriteString(digits[:split])
	b.WriteByte('.')
	// Write the fractional part of the amount.
	b.WriteString(digits[split:])
	return b.String()
}

// absToUint64 returns the absolute value of v as a uint64.
func absToUint64(v int64) uint64 {
	if v >= 0 {
		return uint64(v)
	}
	// math.MinInt64 (-1 << 63) cannot be negated as an int64 without overflowing,
	// so return its exact unsigned representation directly.
	if v == math.MinInt64 {
		return 1 << 63
	}
	// Avoid -uint64(v) because casting a negative int64 directly to uint64
	// triggers gosec G115 (integer overflow conversion). Negating first keeps
	// the value positive, guarded by an explicit >= 0 check before converting.
	u := -v
	if u >= 0 {
		return uint64(u)
	}
	return 0
}
