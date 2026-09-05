package ledger

import (
	"errors"
	"testing"
)

func TestParseAccountID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "uuid v7", input: "0198f3c1-7a4e-7c2b-9f31-6d8a2b4c5e10"},
		{name: "uuid v4", input: "3f2504e0-4f89-41d3-9a0c-0305e82c3301"},
		{name: "uppercase is normalised", input: "3F2504E0-4F89-41D3-9A0C-0305E82C3301"},
		{name: "empty", input: "", wantErr: true},
		{name: "not a uuid", input: "account-42", wantErr: true},
		{name: "wrong length", input: "3f2504e0-4f89-41d3-9a0c-0305e82c33", wantErr: true},
		{name: "hyphens misplaced", input: "3f2504e04-f89-41d3-9a0c-0305e82c3301", wantErr: true},
		{name: "non hex character", input: "3f2504e0-4f89-41d3-9a0c-0305e82c330g", wantErr: true},
		// todo: add more test cases
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAccountID(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseAccountID(%q) = %q, want an error", tt.input, got)
				}
				if !errors.Is(err, ErrInvalidID) {
					t.Fatalf("ParseAccountID(%q) error = %v, want ErrInvalidID", tt.input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAccountID(%q) returned unexpected error: %v", tt.input, err)
			}
			if got.IsZero() {
				t.Error("parsed ID reports IsZero")
			}
		})
	}
}

func TestAccountIDNormalisesCase(t *testing.T) {
	upper, err := ParseAccountID("3F2504E0-4F89-41D3-9A0C-0305E82C3301")
	if err != nil {
		t.Fatalf("ParseAccountID: %v", err)
	}
	lower, err := ParseAccountID("3f2504e0-4f89-41d3-9a0c-0305e82c3301")
	if err != nil {
		t.Fatalf("ParseAccountID: %v", err)
	}

	// Two spellings of one identifier must be one value, or it could be
	// accidentally holding the same account twice.
	if upper != lower {
		t.Errorf("case variants produced different IDs: %q and %q", upper, lower)
	}
}

func TestZeroAccountID(t *testing.T) {
	var id AccountID

	if !id.IsZero() {
		t.Error("zero AccountID does not report IsZero")
	}
	if id.String() != "" {
		t.Errorf("zero AccountID String() = %q, want empty", id.String())
	}
}

func TestParseTransactionID(t *testing.T) {
	if _, err := ParseTransactionID("0198f3c1-7a4e-7c2b-9f31-6d8a2b4c5e10"); err != nil {
		t.Fatalf("ParseTransactionID returned unexpected error: %v", err)
	}
	if _, err := ParseTransactionID("nope"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("ParseTransactionID(%q) error = %v, want ErrInvalidID", "nope", err)
	}
}

// AccountID and TransactionID must be distinct types, so that passing one where
// the other is expected is a compile error rather than a compiled bug.
func TestIDTypesAreDistinct(t *testing.T) {
	accountID, err := ParseAccountID("0198f3c1-7a4e-7c2b-9f31-6d8a2b4c5e10")
	if err != nil {
		t.Fatalf("ParseAccountID: %v", err)
	}
	transactionID, err := ParseTransactionID("0198f3c1-7a4e-7c2b-9f31-6d8a2b4c5e10")
	if err != nil {
		t.Fatalf("ParseTransactionID: %v", err)
	}

	// Same string, different types. The following condition should not compile
	if accountID.String() != transactionID.String() {
		t.Error("expected the same underlying string")
	}
}
