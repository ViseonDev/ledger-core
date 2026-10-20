package ledger

import "testing"

// TestStandardChartIsWellFormed checks that the standard chart accounts are valid.
func TestStandardChartIsWellFormed(t *testing.T) {
	seen := make(map[string]bool)

	for _, entry := range StandardChart() {
		t.Run(entry.Code, func(t *testing.T) {
			if seen[entry.Code] {
				t.Fatalf("duplicate account code %q in the standard chart", entry.Code)
			}
			seen[entry.Code] = true

			code, err := ParseAccountCode(entry.Code)
			if err != nil {
				t.Fatalf("ParseAccountCode(%q): %v", entry.Code, err)
			}
			if !entry.Type.IsValid() {
				t.Fatalf("account %q has an invalid type", entry.Code)
			}
			if !entry.Type.MatchesCodeRoot(code.Root()) {
				t.Errorf("account %q has root %q but type %v", entry.Code, code.Root(), entry.Type)
			}
			if entry.Name == "" {
				t.Errorf("account %q has no name", entry.Code)
			}
			if entry.Description == "" {
				t.Errorf("account %q has no description", entry.Code)
			}
		})
	}
}

func TestStandardChartCoversEveryAccountType(t *testing.T) {
	present := make(map[AccountType]bool)
	for _, entry := range StandardChart() {
		present[entry.Type] = true
	}

	for _, accountType := range AllAccountTypes() {
		if !present[accountType] {
			t.Errorf("the standard chart has no %v account", accountType)
		}
	}
}

func TestPerEntityCodes(t *testing.T) {
	wallet, err := CustomerWalletCode("cus_demo_0001")
	if err != nil {
		t.Fatalf("CustomerWalletCode: %v", err)
	}
	if got, want := wallet.String(), "liabilities:customer:wallet:cus_demo_0001"; got != want {
		t.Errorf("CustomerWalletCode = %q, want %q", got, want)
	}
	if !wallet.HasPrefix(CustomerWalletPrefix) {
		t.Errorf("%q does not have prefix %q", wallet, CustomerWalletPrefix)
	}

	payable, err := MerchantPayableCode("mer_demo_01")
	if err != nil {
		t.Fatalf("MerchantPayableCode: %v", err)
	}
	if got, want := payable.String(), "liabilities:merchant:payable:mer_demo_01"; got != want {
		t.Errorf("MerchantPayableCode = %q, want %q", got, want)
	}

	if _, err := CustomerWalletCode("cus demo/0001"); err == nil {
		t.Error("CustomerWalletCode accepted a reference with a space and a slash")
	}
}
