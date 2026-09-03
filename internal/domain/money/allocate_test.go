package money

import (
	"errors"
	"testing"
)

func TestAllocate(t *testing.T) {
	tests := []struct {
		name   string
		amount int64
		ratios []int64
		want   []int64
	}{
		{
			name:   "even split with no remainder",
			amount: 900,
			ratios: []int64{1, 1, 1},
			want:   []int64{300, 300, 300},
		},
		{
			name:   "one cent remainder goes to the first party",
			amount: 100,
			ratios: []int64{1, 1, 1},
			want:   []int64{34, 33, 33},
		},
		{
			name:   "two cent remainder goes to the first two parties",
			amount: 101,
			ratios: []int64{1, 1, 1},
			want:   []int64{34, 34, 33},
		},
		{
			name:   "weighted split",
			amount: 10000,
			ratios: []int64{70, 20, 10},
			want:   []int64{7000, 2000, 1000},
		},
		{
			name:   "weighted split with remainder",
			amount: 1000,
			ratios: []int64{3, 3, 3},
			want:   []int64{334, 333, 333},
		},
		{
			name:   "single recipient gets everything",
			amount: 12345,
			ratios: []int64{1},
			want:   []int64{12345},
		},
		{
			name:   "zero ratio recipient gets nothing when the split is exact",
			amount: 1000,
			ratios: []int64{1, 0, 1},
			want:   []int64{500, 0, 500},
		},
		{
			name:   "negative amounts distribute the remainder downwards",
			amount: -100,
			ratios: []int64{1, 1, 1},
			want:   []int64{-34, -33, -33},
		},
		{
			name:   "zero amount",
			amount: 0,
			ratios: []int64{1, 2, 3},
			want:   []int64{0, 0, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := mustMoney(t, tt.amount, EUR)

			parts, err := m.Allocate(tt.ratios)
			if err != nil {
				t.Fatalf("Allocate(%v) returned unexpected error: %v", tt.ratios, err)
			}
			if len(parts) != len(tt.want) {
				t.Fatalf("Allocate returned %d parts, want %d", len(parts), len(tt.want))
			}

			var total int64
			for i, part := range parts {
				if part.Amount() != tt.want[i] {
					t.Errorf("part[%d] = %d, want %d", i, part.Amount(), tt.want[i])
				}
				if part.Currency() != EUR {
					t.Errorf("part[%d] currency = %v, want EUR", i, part.Currency())
				}
				total += part.Amount()
			}
			// The property that matters more than any individual case.
			if total != tt.amount {
				t.Errorf("parts sum to %d, want %d - allocation lost or invented money", total, tt.amount)
			}
		})
	}
}

func TestAllocateRejectsBadRatios(t *testing.T) {
	m := mustMoney(t, 1000, EUR)

	tests := []struct {
		name   string
		ratios []int64
	}{
		{name: "nil", ratios: nil},
		{name: "empty", ratios: []int64{}},
		{name: "all zero", ratios: []int64{0, 0}},
		{name: "contains a negative", ratios: []int64{1, -1, 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := m.Allocate(tt.ratios); !errors.Is(err, ErrInvalidRatios) {
				t.Fatalf("Allocate(%v) error = %v, want ErrInvalidRatios", tt.ratios, err)
			}
		})
	}
}

// TestAllocateNeverLosesMoney is the property test. It is the reason this
// function exists, expressed directly: for a wide range of amounts and splits,
// the parts must always sum to exactly the original amount.
func TestAllocateNeverLosesMoney(t *testing.T) {
	amounts := []int64{0, 1, 2, 3, 7, 99, 100, 101, 999, 1000, 123456789, -1, -7, -100, -999}
	ratioSets := [][]int64{
		{1, 1},
		{1, 1, 1},
		{1, 2},
		{1, 2, 3},
		{70, 20, 10},
		{1, 1, 1, 1, 1, 1, 1},
		{999, 1},
		{1, 0, 1},
	}

	for _, amount := range amounts {
		for _, ratios := range ratioSets {
			m := mustMoney(t, amount, EUR)

			parts, err := m.Allocate(ratios)
			if err != nil {
				t.Fatalf("Allocate(%d, %v): %v", amount, ratios, err)
			}

			var total int64
			for _, part := range parts {
				total += part.Amount()
			}
			if total != amount {
				t.Errorf("Allocate(%d, %v) parts sum to %d", amount, ratios, total)
			}
		}
	}
}
