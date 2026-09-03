package money

// Allocate splits m across len(ratios) recipients in proportion to the given
// ratios, without losing or inventing a single minor unit.
// Example: Allocate(100, [1, 1, 1]) == [34, 33, 33]
// NOTE: I didn't write this function. I copied from
// https://github.com/Rhymond/go-money/blob/master/money.go
func (m Money) Allocate(ratios []int64) ([]Money, error) {
	if len(ratios) == 0 {
		return nil, ErrInvalidRatios
	}

	var total int64
	for _, r := range ratios {
		if r < 0 {
			return nil, ErrInvalidRatios
		}
		if addOverflows(total, r) {
			return nil, ErrOverflow
		}
		total += r
	}
	if total == 0 {
		return nil, ErrInvalidRatios
	}

	parts := make([]Money, len(ratios))
	remainder := m.amount

	for i, r := range ratios {
		if mulOverflows(m.amount, r) {
			return nil, ErrOverflow
		}
		share := m.amount * r / total
		parts[i] = Money{amount: share, currency: m.currency}
		remainder -= share
	}

	step := int64(1)
	if remainder < 0 {
		step = -1
	}
	for i := 0; remainder != 0; i++ {
		parts[i].amount += step
		remainder -= step
	}

	return parts, nil
}
