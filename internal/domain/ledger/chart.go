package ledger

// ChartEntry describes one account in the standard chart of accounts.
type ChartEntry struct {
	Code          string
	Name          string
	Type          AccountType
	AllowNegative bool
	Description   string
}

// StandardChart returns the core platform accounts used to simulate real-world
// balances (such as bank funds and PSP holdings) and track the flow of money
// through pending settlements, fees, and revenue.
func StandardChart() []ChartEntry {
	return []ChartEntry{
		{
			Code:          "assets:cash:operating",
			Name:          "Operating bank account",
			Type:          Asset,
			AllowNegative: false,
			Description:   "Real money in the platform's bank. Every customer wallet balance is backed by a share of this.",
		},
		{
			Code:          "assets:psp:stripe",
			Name:          "Funds held at payment processor",
			Type:          Asset,
			AllowNegative: false,
			Description:   "Money captured by the processor but not yet paid out to the platform's bank.",
		},
		{
			Code:          "liabilities:settlement:pending",
			Name:          "Pending settlement",
			Type:          Liability,
			AllowNegative: false,
			Description:   "Value reserved from a customer wallet but not yet allocated to a merchant. A payment saga in flight lives here.",
		},
		{
			Code:          "liabilities:fees:payable",
			Name:          "Fees payable to third parties",
			Type:          Liability,
			AllowNegative: true,
			Description:   "Processor fees accrued but not yet settled.",
		},
		{
			Code:          "revenue:fees:processing",
			Name:          "Processing fee revenue",
			Type:          Revenue,
			AllowNegative: true,
			Description:   "The platform's cut. Negative is possible during a refund-heavy period and is not an error.",
		},
		{
			Code:          "expense:psp:fees",
			Name:          "Payment processor fees",
			Type:          Expense,
			AllowNegative: true,
			Description:   "What the processor charges the platform.",
		},
		{
			Code:          "equity:retained",
			Name:          "Retained earnings",
			Type:          Equity,
			AllowNegative: true,
			Description:   "The residual. Balancing account for the opening balances in seed data.",
		},
	}
}

// Code template prefixes for the per-entity accounts created on demand.
const (
	// CustomerWalletPrefix is the code prefix for customer wallet accounts.
	CustomerWalletPrefix = "liabilities:customer:wallet"

	// MerchantPayablePrefix is the code prefix for merchant payable accounts.
	MerchantPayablePrefix = "liabilities:merchant:payable"
)

// CustomerWalletCode builds the account code for a customer's wallet.
func CustomerWalletCode(customerRef string) (AccountCode, error) {
	return ParseAccountCode(CustomerWalletPrefix + ":" + customerRef)
}

// MerchantPayableCode builds the account code for a merchant's payable account.
func MerchantPayableCode(merchantRef string) (AccountCode, error) {
	return ParseAccountCode(MerchantPayablePrefix + ":" + merchantRef)
}
