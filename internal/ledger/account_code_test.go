package ledger

import (
	"errors"
	"testing"
)

func TestParseAccountCode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "two segments", input: "equity:retained", want: "equity:retained"},
		{name: "deep hierarchy", input: "liabilities:customer:wallet:cus_demo_0001", want: "liabilities:customer:wallet:cus_demo_0001"},
		{name: "digits and underscores", input: "assets:psp:stripe_2024", want: "assets:psp:stripe_2024"},
		{name: "hyphens", input: "assets:psp:stripe-eu", want: "assets:psp:stripe-eu"},
		{name: "uppercase is normalised", input: "Equity:Retained", want: "equity:retained"},
		{name: "surrounding space is trimmed", input: "  equity:retained  ", want: "equity:retained"},

		{name: "empty", input: "", wantErr: true},
		{name: "single segment", input: "equity", wantErr: true},
		{name: "empty segment", input: "equity::retained", wantErr: true},
		{name: "leading colon", input: ":equity:retained", wantErr: true},
		{name: "trailing colon", input: "equity:retained:", wantErr: true},
		{name: "space inside", input: "equity:retained earnings", wantErr: true},
		{name: "slash", input: "equity/retained", wantErr: true},
		{name: "sql injection attempt", input: "equity:retained'; DROP TABLE accounts;--", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAccountCode(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseAccountCode(%q) = %q, want an error", tt.input, got)
				}
				if !errors.Is(err, ErrInvalidAccountCode) {
					t.Fatalf("error = %v, want ErrInvalidAccountCode", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAccountCode(%q) returned unexpected error: %v", tt.input, err)
			}
			if got.String() != tt.want {
				t.Errorf("ParseAccountCode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestAccountCodeSegments(t *testing.T) {
	code, err := ParseAccountCode("liabilities:customer:wallet:cus_demo_0001")
	if err != nil {
		t.Fatalf("ParseAccountCode: %v", err)
	}

	segments := code.Segments()
	want := []string{"liabilities", "customer", "wallet", "cus_demo_0001"}

	if len(segments) != len(want) {
		t.Fatalf("Segments() returned %d segments, want %d", len(segments), len(want))
	}
	for i := range want {
		if segments[i] != want[i] {
			t.Errorf("Segments()[%d] = %q, want %q", i, segments[i], want[i])
		}
	}

	if got := code.Root(); got != "liabilities" {
		t.Errorf("Root() = %q, want %q", got, "liabilities")
	}
}

func TestAccountCodeHasPrefix(t *testing.T) {
	code, err := ParseAccountCode("liabilities:customer:wallet:cus_demo_0001")
	if err != nil {
		t.Fatalf("ParseAccountCode: %v", err)
	}

	tests := []struct {
		prefix string
		want   bool
	}{
		{prefix: "liabilities", want: true},
		{prefix: "liabilities:customer", want: true},
		{prefix: "liabilities:customer:wallet", want: true},
		{prefix: "liabilities:customer:wallet:cus_demo_0001", want: true},
		{prefix: "assets", want: false},
		// The trap: a naive strings.HasPrefix would say true here, because
		// "liabilities:customer:w" is a textual prefix of the code. It is not a
		// segment prefix, and treating it as one would make a balance rollup
		// include accounts it should not.
		{prefix: "liabilities:customer:w", want: false},
		{prefix: "liab", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			if got := code.HasPrefix(tt.prefix); got != tt.want {
				t.Errorf("HasPrefix(%q) = %v, want %v", tt.prefix, got, tt.want)
			}
		})
	}
}
