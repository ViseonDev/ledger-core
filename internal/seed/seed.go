package seed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/ViseonDev/ledger-core/internal/adapters/postgres"
	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
	"github.com/ViseonDev/ledger-core/internal/domain/money"
)

// Config controls the volume and determinism of generated seed data.
type Config struct {
	// Seed fixes the RNG so runs produce identical datasets.
	Seed uint64

	// Anchor is the cutoff time for generated transactions (defaults to midnight UTC today).
	Anchor time.Time

	// HistoryDays determines how many days back to spread historical activity.
	HistoryDays int

	Customers int
	Merchants int
}

// DefaultConfig provides standard settings for local development and demos.
func DefaultConfig() Config {
	return Config{
		Seed:        20260817,
		Anchor:      time.Now().UTC().Truncate(24 * time.Hour),
		HistoryDays: 180,
		Customers:   40,
		Merchants:   8,
	}
}

// Result tallies how many entities were created vs skipped.
type Result struct {
	AccountsCreated     int
	AccountsSkipped     int
	TransactionsCreated int
	TransactionsSkipped int
}

// Seeder creates synthetic accounts and journal history deterministically.
type Seeder struct {
	store  *postgres.Store
	logger *slog.Logger
}

// New constructs a Seeder over the target database store.
func New(store *postgres.Store, logger *slog.Logger) *Seeder {
	return &Seeder{store: store, logger: logger}
}

// Run populates the chart of accounts, opening balances, transaction activity, and reversals.
// It is fully idempotent because all IDs are deterministically derived.
func (s *Seeder) Run(ctx context.Context, cfg Config) (Result, error) {
	if cfg.Anchor.IsZero() {
		cfg.Anchor = time.Now().UTC().Truncate(24 * time.Hour)
	}
	if cfg.HistoryDays <= 0 {
		cfg.HistoryDays = 180
	}

	var result Result

	// Track wallets in memory so the output only depends on cfg.
	wallets := walletBook{}

	if err := s.seedChart(ctx, cfg, &result); err != nil {
		return result, fmt.Errorf("seeding chart of accounts: %w", err)
	}
	if err := s.seedOpeningBalances(ctx, cfg, wallets, &result); err != nil {
		return result, fmt.Errorf("seeding opening balances: %w", err)
	}
	if err := s.seedActivity(ctx, cfg, wallets, &result); err != nil {
		return result, fmt.Errorf("seeding activity: %w", err)
	}
	if err := s.seedReversals(ctx, cfg, wallets, &result); err != nil {
		return result, fmt.Errorf("seeding reversals: %w", err)
	}

	s.logger.Info("seed complete",
		slog.Int("accounts_created", result.AccountsCreated),
		slog.Int("accounts_skipped", result.AccountsSkipped),
		slog.Int("transactions_created", result.TransactionsCreated),
		slog.Int("transactions_skipped", result.TransactionsSkipped),
	)
	return result, nil
}

// seedChart opens standard platform accounts plus customer wallets and merchant payable accounts.
func (s *Seeder) seedChart(ctx context.Context, cfg Config, result *Result) error {
	type spec struct {
		code          string
		name          string
		accountType   ledger.AccountType
		allowNegative bool
	}

	specs := make([]spec, 0, len(ledger.StandardChart())+cfg.Customers+cfg.Merchants)

	for _, entry := range ledger.StandardChart() {
		specs = append(specs, spec{entry.Code, entry.Name, entry.Type, entry.AllowNegative})
	}
	for i := 1; i <= cfg.Customers; i++ {
		code, err := ledger.CustomerWalletCode(customerRef(i))
		if err != nil {
			return err
		}
		specs = append(specs, spec{
			code:        code.String(),
			name:        fmt.Sprintf("Demo customer %04d wallet", i),
			accountType: ledger.Liability,
		})
	}
	for i := 1; i <= cfg.Merchants; i++ {
		code, err := ledger.MerchantPayableCode(merchantRef(i))
		if err != nil {
			return err
		}
		specs = append(specs, spec{
			code:        code.String(),
			name:        fmt.Sprintf("Demo merchant %02d payable", i),
			accountType: ledger.Liability,
		})
	}

	for _, sp := range specs {
		code, parseErr := ledger.ParseAccountCode(sp.code)
		if parseErr != nil {
			return parseErr
		}
		id, idErr := accountID(sp.code)
		if idErr != nil {
			return idErr
		}

		account, accErr := ledger.NewAccount(ledger.AccountParams{
			ID:            id,
			Code:          code,
			Name:          sp.name,
			Type:          sp.accountType,
			Currency:      money.EUR,
			AllowNegative: sp.allowNegative,
		})
		if accErr != nil {
			return fmt.Errorf("building account %s: %w", sp.code, accErr)
		}

		openErr := s.store.OpenAccount(ctx, account)
		switch {
		case openErr == nil:
			result.AccountsCreated++
		case errors.Is(openErr, ledger.ErrAccountExists):
			// Idempotent skip if the account already exists.
			result.AccountsSkipped++
		default:
			return fmt.Errorf("opening account %s: %w", sp.code, openErr)
		}
	}
	return nil
}

// seedOpeningBalances funds each customer wallet from retained earnings.
func (s *Seeder) seedOpeningBalances(ctx context.Context, cfg Config, wallets walletBook, result *Result) error {
	rng := rand.New(rand.NewPCG(cfg.Seed, 0x09E217)) //nolint:gosec // deterministic seed data generation
	start := cfg.Anchor.AddDate(0, 0, -cfg.HistoryDays)

	for i := 1; i <= cfg.Customers; i++ {
		walletCode, err := ledger.CustomerWalletCode(customerRef(i))
		if err != nil {
			return err
		}

		// Amount between 50.00 and 2,500.00 EUR in whole units.
		amount := int64(5_000 + rng.IntN(245_001)/100*100)

		posted, postErr := s.post(ctx, postSpec{
			name:      fmt.Sprintf("opening:%s", customerRef(i)),
			reference: "opening_balance",
			at:        start.Add(time.Duration(i) * time.Minute),
			metadata:  map[string]string{"kind": "opening_balance", "synthetic": "true"},
			legs: []leg{
				{code: "equity:retained", direction: ledger.Debit, amount: amount},
				{code: walletCode.String(), direction: ledger.Credit, amount: amount},
			},
		})
		if postErr != nil {
			return postErr
		}
		wallets.credit(walletCode.String(), amount)
		countPost(result, posted)
	}
	return nil
}

// seedActivity generates wallet top-ups and merchant payments across the history window.
func (s *Seeder) seedActivity(ctx context.Context, cfg Config, wallets walletBook, result *Result) error {
	rng := rand.New(rand.NewPCG(cfg.Seed, 0xAC71717)) //nolint:gosec // deterministic seed data generation
	start := cfg.Anchor.AddDate(0, 0, -cfg.HistoryDays)
	window := cfg.Anchor.Sub(start)

	// Around ten operations per customer to give a realistic journal size.
	total := cfg.Customers * 10

	for n := range total {
		customer := 1 + rng.IntN(cfg.Customers)
		merchant := 1 + rng.IntN(cfg.Merchants)

		at := start.Add(time.Duration(rng.Int64N(int64(window))))

		walletCode, codeErr := ledger.CustomerWalletCode(customerRef(customer))
		if codeErr != nil {
			return codeErr
		}
		payableCode, codeErr := ledger.MerchantPayableCode(merchantRef(merchant))
		if codeErr != nil {
			return codeErr
		}

		var (
			posted  bool
			postErr error
		)
		if rng.IntN(100) < 30 {
			// Top-up: cash increases and platform wallet liability increases.
			amount := int64(1_000 + rng.IntN(50_000)/100*100)
			wallets.credit(walletCode.String(), amount)
			posted, postErr = s.post(ctx, postSpec{
				name:      fmt.Sprintf("topup:%d", n),
				reference: fmt.Sprintf("topup_%06d", n),
				at:        at,
				metadata:  map[string]string{"kind": "top_up", "synthetic": "true"},
				legs: []leg{
					{code: "assets:cash:operating", direction: ledger.Debit, amount: amount},
					{code: walletCode.String(), direction: ledger.Credit, amount: amount},
				},
			})
		} else {
			// Payment with fee split: 2.9% + 30 cents, integer minor units.
			amount := int64(500 + rng.IntN(30_000))
			fee := amount*29/1000 + 30
			if fee >= amount {
				fee = amount - 1
			}
			merchantAmount := amount - fee

			// Skip if the wallet can't cover it. RNG draws already happened, so runs stay identical.
			if !wallets.debit(walletCode.String(), amount) {
				continue
			}

			posted, postErr = s.post(ctx, postSpec{
				name:      fmt.Sprintf("payment:%d", n),
				reference: fmt.Sprintf("order_%06d", n),
				at:        at,
				metadata: map[string]string{
					"kind":      "payment",
					"merchant":  merchantRef(merchant),
					"synthetic": "true",
				},
				legs: []leg{
					{code: walletCode.String(), direction: ledger.Debit, amount: amount},
					{code: payableCode.String(), direction: ledger.Credit, amount: merchantAmount},
					{code: "revenue:fees:processing", direction: ledger.Credit, amount: fee},
				},
			})
		}
		if postErr != nil {
			return postErr
		}
		countPost(result, posted)
	}
	return nil
}

// seedReversals creates disputed payment reversal chains.
func (s *Seeder) seedReversals(ctx context.Context, cfg Config, wallets walletBook, result *Result) error {
	rng := rand.New(rand.NewPCG(cfg.Seed, 0x2E7E45A1)) //nolint:gosec // deterministic seed data generation
	start := cfg.Anchor.AddDate(0, 0, -cfg.HistoryDays)

	const chains = 6

	for n := range chains {
		customer := 1 + rng.IntN(cfg.Customers)
		merchant := 1 + rng.IntN(cfg.Merchants)
		amount := int64(1_000 + rng.IntN(20_000))

		walletCode, codeErr := ledger.CustomerWalletCode(customerRef(customer))
		if codeErr != nil {
			return codeErr
		}
		payableCode, codeErr := ledger.MerchantPayableCode(merchantRef(merchant))
		if codeErr != nil {
			return codeErr
		}

		at := start.AddDate(0, 0, 10+n*20)

		if !wallets.debit(walletCode.String(), amount) {
			continue
		}
		// The reversal below returns the funds to the wallet.
		wallets.credit(walletCode.String(), amount)

		originalName := fmt.Sprintf("disputed:%d", n)
		posted, postErr := s.post(ctx, postSpec{
			name:      originalName,
			reference: fmt.Sprintf("disputed_%06d", n),
			at:        at,
			metadata:  map[string]string{"kind": "payment", "synthetic": "true"},
			legs: []leg{
				{code: walletCode.String(), direction: ledger.Debit, amount: amount},
				{code: payableCode.String(), direction: ledger.Credit, amount: amount},
			},
		})
		if postErr != nil {
			return postErr
		}
		countPost(result, posted)

		originalID, idErr := transactionID(originalName)
		if idErr != nil {
			return idErr
		}
		reversalID, idErr := transactionID("reversal:" + originalName)
		if idErr != nil {
			return idErr
		}

		reversed, existsErr := s.posted(ctx, reversalID)
		if existsErr != nil {
			return existsErr
		}
		if reversed {
			result.TransactionsSkipped++
			continue
		}

		original, loadErr := postgres.LoadTransaction(ctx, s.store.Pool(), originalID)
		if loadErr != nil {
			return loadErr
		}

		reversal, revErr := ledger.NewReversal(original, reversalID, at.Add(48*time.Hour),
			"customer disputed the charge (synthetic demo data)")
		if revErr != nil {
			return revErr
		}

		postErr = s.store.Post(ctx, reversal)
		switch {
		case postErr == nil:
			result.TransactionsCreated++
		case errors.Is(postErr, ledger.ErrTransactionExists), errors.Is(postErr, ledger.ErrAlreadyReversed):
			result.TransactionsSkipped++
		default:
			return fmt.Errorf("posting reversal for %s: %w", originalName, postErr)
		}
	}
	return nil
}

// leg is one debit or credit leg of a planned transaction.
type leg struct {
	code      string
	direction ledger.Direction
	amount    int64
}

// postSpec describes a transaction to be constructed and committed.
type postSpec struct {
	name      string
	reference string
	at        time.Time
	metadata  map[string]string
	legs      []leg
}

// post builds and posts one transaction, reporting whether it was created or skipped.
func (s *Seeder) post(ctx context.Context, spec postSpec) (bool, error) {
	id, err := transactionID(spec.name)
	if err != nil {
		return false, err
	}

	if exists, existsErr := s.posted(ctx, id); existsErr != nil || exists {
		return false, existsErr
	}

	codes := make([]string, 0, len(spec.legs))
	for _, l := range spec.legs {
		codes = append(codes, l.code)
	}
	accounts, err := postgres.AccountsByCodes(ctx, s.store.Pool(), codes)
	if err != nil {
		return false, err
	}

	entries := make([]ledger.Entry, 0, len(spec.legs))
	for _, l := range spec.legs {
		account, ok := accounts[l.code]
		if !ok {
			return false, fmt.Errorf("%w: %s", ledger.ErrAccountNotFound, l.code)
		}
		amount, amtErr := money.New(l.amount, money.EUR)
		if amtErr != nil {
			return false, amtErr
		}
		entry, entErr := ledger.NewEntry(account.ID(), l.direction, amount)
		if entErr != nil {
			return false, entErr
		}
		entries = append(entries, entry)
	}

	tx, txErr := ledger.NewTransaction(ledger.TransactionParams{
		ID:        id,
		Reference: spec.reference,
		Entries:   entries,
		PostedAt:  spec.at,
		Metadata:  spec.metadata,
	})
	if txErr != nil {
		return false, fmt.Errorf("building %s: %w", spec.name, txErr)
	}

	postErr := s.store.Post(ctx, tx)
	switch {
	case postErr == nil:
		return true, nil
	case errors.Is(postErr, ledger.ErrTransactionExists):
		return false, nil
	default:
		return false, fmt.Errorf("posting %s: %w", spec.name, postErr)
	}
}

// posted checks if id is already in the journal. Needed because the store checks
// funds before duplicate ids, so a re-run could fail on funds instead.
func (s *Seeder) posted(ctx context.Context, id ledger.TransactionID) (bool, error) {
	var exists bool
	if err := s.store.Pool().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM transactions WHERE id = $1)`, id.String(),
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("checking transaction %s: %w", id, err)
	}
	return exists, nil
}

// walletBook holds wallet balances (minor units) while seeding.
type walletBook map[string]int64

func (w walletBook) credit(code string, amount int64) { w[code] += amount }

// debit removes amount from the wallet if it can cover it, reporting whether it did.
func (w walletBook) debit(code string, amount int64) bool {
	if w[code] < amount {
		return false
	}
	w[code] -= amount
	return true
}

func countPost(result *Result, created bool) {
	if created {
		result.TransactionsCreated++
	} else {
		result.TransactionsSkipped++
	}
}
