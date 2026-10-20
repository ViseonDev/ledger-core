package ledger

import "testing"

func TestDirectionOpposite(t *testing.T) {
	if got := Debit.Opposite(); got != Credit {
		t.Errorf("Debit.Opposite() = %v, want Credit", got)
	}
	if got := Credit.Opposite(); got != Debit {
		t.Errorf("Credit.Opposite() = %v, want Debit", got)
	}
}

func TestDirectionOppositeIsInvolution(t *testing.T) {
	// Reversing a reversal must return the original direction, or a reversal
	// chain would drift.
	for _, d := range []Direction{Debit, Credit} {
		if got := d.Opposite().Opposite(); got != d {
			t.Errorf("%v.Opposite().Opposite() = %v, want %v", d, got, d)
		}
	}
}

func TestParseDirection(t *testing.T) {
	tests := []struct {
		input   string
		want    Direction
		wantErr bool
	}{
		{input: "DEBIT", want: Debit},
		{input: "debit", want: Debit},
		{input: " CREDIT ", want: Credit},
		{input: "credit", want: Credit},
		{input: "", wantErr: true},
		{input: "DR", wantErr: true},
		{input: "sideways", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseDirection(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseDirection(%q) = %v, want an error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDirection(%q) returned unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseDirection(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestDirectionString(t *testing.T) {
	if got := Debit.String(); got != "DEBIT" {
		t.Errorf("Debit.String() = %q, want %q", got, "DEBIT")
	}
	if got := Credit.String(); got != "CREDIT" {
		t.Errorf("Credit.String() = %q, want %q", got, "CREDIT")
	}
	if got := Direction(0).String(); got != "INVALID" {
		t.Errorf("Direction(0).String() = %q, want %q", got, "INVALID")
	}
}
