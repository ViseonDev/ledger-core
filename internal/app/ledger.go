// Package app contains the application services: the use cases that orchestrate
// the domain, the database and idempotency.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ViseonDev/ledger-core/internal/adapters/postgres"
	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
	"github.com/ViseonDev/ledger-core/internal/domain/money"
	"github.com/ViseonDev/ledger-core/internal/ports"
)

// Ledger is the application service for accounts and the journal.
type Ledger struct {
	store  *postgres.Store
	ids    ports.IDGenerator
	clock  ports.Clock
	logger *slog.Logger
}

// NewLedger wires an application service.
func NewLedger(store *postgres.Store, ids ports.IDGenerator, clk ports.Clock, logger *slog.Logger) *Ledger {
	return &Ledger{store: store, ids: ids, clock: clk, logger: logger}
}

// Views: what crosses the boundary out of this package.

// AccountView is an account as the API presents it.
type AccountView struct {
	ID            string `json:"id"`
	Code          string `json:"code"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	Currency      string `json:"currency"`
	AllowNegative bool   `json:"allow_negative"`
	Status        string `json:"status"`
}

// EntryView is one entry as the API presents it.
type EntryView struct {
	AccountID   string `json:"account_id"`
	AccountCode string `json:"account_code,omitempty"`
	Direction   string `json:"direction"`
	Amount      int64  `json:"amount"`
}

// TransactionView is a transaction as the API presents it.
type TransactionView struct {
	ID         string            `json:"id"`
	Reference  string            `json:"reference,omitempty"`
	Currency   string            `json:"currency"`
	PostedAt   time.Time         `json:"posted_at"`
	ReversalOf string            `json:"reversal_of,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Entries    []EntryView       `json:"entries"`
}

// BalanceView is an account balance as the API presents it.
type BalanceView struct {
	AccountID string `json:"account_id"`
	Currency  string `json:"currency"`
	Debits    int64  `json:"debits"`
	Credits   int64  `json:"credits"`
	// Balance is the net amount in the account's normal direction: positive
	// means the account holds what it is supposed to hold, whatever its type.
	Balance int64 `json:"balance"`
	// Authoritative reports whether this was recomputed from the journal rather
	// than read from the cache.
	Authoritative bool `json:"authoritative"`
}

// Commands: what crosses the boundary in.

// OpenAccountCommand opens a new account.
type OpenAccountCommand struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	Currency      string `json:"currency"`
	AllowNegative bool   `json:"allow_negative"`
}

// EntryInput is one requested entry. Accounts are named by code, which is what
// a caller writing a request has to hand.
type EntryInput struct {
	AccountCode string `json:"account_code"`
	Direction   string `json:"direction"`
	Amount      int64  `json:"amount"`
}

// PostTransactionCommand posts a balanced transaction.
type PostTransactionCommand struct {
	Reference string            `json:"reference"`
	Currency  string            `json:"currency"`
	Metadata  map[string]string `json:"metadata"`
	Entries   []EntryInput      `json:"entries"`
}

// ReverseTransactionCommand reverses a posted transaction.
type ReverseTransactionCommand struct {
	Reason string `json:"reason"`
}

// Use cases

// OpenAccount creates an account, idempotently.
func (l *Ledger) OpenAccount(ctx context.Context, idem postgres.IdempotentRequest, cmd OpenAccountCommand) (postgres.Outcome[AccountView], error) {
	return postgres.Execute(ctx, l.store, idem, func(ctx context.Context, db postgres.DBTX) (AccountView, error) {
		code, err := ledger.ParseAccountCode(cmd.Code)
		if err != nil {
			return AccountView{}, err
		}
		accountType, err := ledger.ParseAccountType(cmd.Type)
		if err != nil {
			return AccountView{}, err
		}
		currency, err := money.ParseCurrency(cmd.Currency)
		if err != nil {
			return AccountView{}, err
		}
		id, err := l.ids.NewAccountID()
		if err != nil {
			return AccountView{}, err
		}

		account, err := ledger.NewAccount(ledger.AccountParams{
			ID:            id,
			Code:          code,
			Name:          cmd.Name,
			Type:          accountType,
			Currency:      currency,
			AllowNegative: cmd.AllowNegative,
		})
		if err != nil {
			return AccountView{}, err
		}
		if insErr := postgres.InsertAccount(ctx, db, account); insErr != nil {
			return AccountView{}, insErr
		}

		l.logger.InfoContext(ctx, "account opened",
			slog.String("account_id", account.ID().String()),
			slog.String("code", account.Code().String()),
			slog.String("type", account.Type().String()),
		)
		return accountView(account), nil
	})
}

// PostTransaction posts a balanced transaction, idempotently.
func (l *Ledger) PostTransaction(ctx context.Context, idem postgres.IdempotentRequest, cmd PostTransactionCommand) (postgres.Outcome[TransactionView], error) {
	return postgres.Execute(ctx, l.store, idem, func(ctx context.Context, db postgres.DBTX) (TransactionView, error) {
		tx, codes, err := l.buildTransaction(ctx, db, cmd)
		if err != nil {
			return TransactionView{}, err
		}
		if postErr := postgres.PostTransaction(ctx, db, tx); postErr != nil {
			return TransactionView{}, postErr
		}

		event, eventErr := tx.PostedEvent()
		if eventErr != nil {
			return TransactionView{}, eventErr
		}
		l.logger.InfoContext(ctx, "transaction posted",
			slog.String("transaction_id", event.TransactionID.String()),
			slog.String("reference", event.Reference),
			slog.String("currency", event.Currency),
			slog.Int64("amount", event.Amount),
			slog.Int("entries", event.EntryCount),
		)
		return transactionView(tx, codes), nil
	})
}

// ReverseTransaction posts a reversal of an existing transaction, idempotently.
func (l *Ledger) ReverseTransaction(ctx context.Context, idem postgres.IdempotentRequest, id string, cmd ReverseTransactionCommand) (postgres.Outcome[TransactionView], error) {
	return postgres.Execute(ctx, l.store, idem, func(ctx context.Context, db postgres.DBTX) (TransactionView, error) {
		originalID, err := ledger.ParseTransactionID(id)
		if err != nil {
			return TransactionView{}, err
		}
		original, err := postgres.LoadTransaction(ctx, db, originalID)
		if err != nil {
			return TransactionView{}, err
		}
		reversalID, err := l.ids.NewTransactionID()
		if err != nil {
			return TransactionView{}, err
		}

		reversal, err := ledger.NewReversal(original, reversalID, l.clock.Now(), cmd.Reason)
		if err != nil {
			return TransactionView{}, err
		}
		if postErr := postgres.PostTransaction(ctx, db, reversal); postErr != nil {
			return TransactionView{}, postErr
		}

		l.logger.InfoContext(ctx, "transaction reversed",
			slog.String("reversal_id", reversal.ID().String()),
			slog.String("original_id", original.ID().String()),
			slog.String("reason", cmd.Reason),
		)
		return transactionView(reversal, nil), nil
	})
}

// AccountByCode fetches one account.
func (l *Ledger) AccountByCode(ctx context.Context, code string) (AccountView, error) {
	parsed, err := ledger.ParseAccountCode(code)
	if err != nil {
		return AccountView{}, err
	}
	account, err := l.store.AccountByCode(ctx, parsed)
	if err != nil {
		return AccountView{}, err
	}
	return accountView(account), nil
}

// Balance reads an account's balance, either from the cache or recomputed from
// the journal.
func (l *Ledger) Balance(ctx context.Context, accountID string, authoritative bool) (BalanceView, error) {
	id, err := ledger.ParseAccountID(accountID)
	if err != nil {
		return BalanceView{}, err
	}

	var balance ledger.Balance
	if authoritative {
		balance, err = l.store.RecomputeBalance(ctx, id)
	} else {
		balance, err = l.store.Balance(ctx, id)
	}
	if err != nil {
		return BalanceView{}, err
	}

	normal, err := balance.Normal()
	if err != nil {
		return BalanceView{}, err
	}

	return BalanceView{
		AccountID:     id.String(),
		Currency:      balance.Currency().Code(),
		Debits:        balance.Debits().Amount(),
		Credits:       balance.Credits().Amount(),
		Balance:       normal.Amount(),
		Authoritative: authoritative,
	}, nil
}

// TransactionByID fetches one transaction with its entries.
func (l *Ledger) TransactionByID(ctx context.Context, id string) (TransactionView, error) {
	parsed, err := ledger.ParseTransactionID(id)
	if err != nil {
		return TransactionView{}, err
	}
	tx, err := l.store.TransactionByID(ctx, parsed)
	if err != nil {
		return TransactionView{}, err
	}
	return transactionView(tx, nil), nil
}

// buildTransaction resolves account codes and constructs the aggregate.
func (l *Ledger) buildTransaction(ctx context.Context, db postgres.DBTX, cmd PostTransactionCommand) (*ledger.Transaction, map[ledger.AccountID]ledger.AccountCode, error) {
	if len(cmd.Entries) == 0 {
		return nil, nil, fmt.Errorf("%w: at least two entries are required", ledger.ErrTooFewEntries)
	}

	currency, err := money.ParseCurrency(cmd.Currency)
	if err != nil {
		return nil, nil, err
	}

	wanted := make([]string, 0, len(cmd.Entries))
	for _, in := range cmd.Entries {
		wanted = append(wanted, in.AccountCode)
	}
	accounts, err := postgres.AccountsByCodes(ctx, db, wanted)
	if err != nil {
		return nil, nil, err
	}

	codes := make(map[ledger.AccountID]ledger.AccountCode, len(accounts))
	entries := make([]ledger.Entry, 0, len(cmd.Entries))

	for i, in := range cmd.Entries {
		account, ok := accounts[in.AccountCode]
		if !ok {
			return nil, nil, fmt.Errorf("%w: entry %d references %q", ledger.ErrAccountNotFound, i, in.AccountCode)
		}
		direction, dirErr := ledger.ParseDirection(in.Direction)
		if dirErr != nil {
			return nil, nil, fmt.Errorf("entry %d: %w", i, dirErr)
		}
		amount, amtErr := money.New(in.Amount, currency)
		if amtErr != nil {
			return nil, nil, fmt.Errorf("entry %d: %w", i, amtErr)
		}
		entry, entryErr := ledger.NewEntry(account.ID(), direction, amount)
		if entryErr != nil {
			return nil, nil, fmt.Errorf("entry %d: %w", i, entryErr)
		}

		codes[account.ID()] = account.Code()
		entries = append(entries, entry)
	}

	id, err := l.ids.NewTransactionID()
	if err != nil {
		return nil, nil, err
	}

	tx, err := ledger.NewTransaction(ledger.TransactionParams{
		ID:        id,
		Reference: cmd.Reference,
		Entries:   entries,
		PostedAt:  l.clock.Now(),
		Metadata:  cmd.Metadata,
	})
	if err != nil {
		return nil, nil, err
	}
	return tx, codes, nil
}

func accountView(a ledger.Account) AccountView {
	return AccountView{
		ID:            a.ID().String(),
		Code:          a.Code().String(),
		Name:          a.Name(),
		Type:          a.Type().String(),
		Currency:      a.Currency().Code(),
		AllowNegative: a.AllowsNegative(),
		Status:        a.Status().String(),
	}
}

func transactionView(tx *ledger.Transaction, codes map[ledger.AccountID]ledger.AccountCode) TransactionView {
	entries := make([]EntryView, 0, tx.EntryCount())
	for _, e := range tx.Entries() {
		view := EntryView{
			AccountID: e.AccountID().String(),
			Direction: e.Direction().String(),
			Amount:    e.Amount().Amount(),
		}
		if code, ok := codes[e.AccountID()]; ok {
			view.AccountCode = code.String()
		}
		entries = append(entries, view)
	}

	view := TransactionView{
		ID:        tx.ID().String(),
		Reference: tx.Reference(),
		Currency:  tx.Currency().Code(),
		PostedAt:  tx.PostedAt(),
		Metadata:  tx.Metadata(),
		Entries:   entries,
	}
	if tx.IsReversal() {
		view.ReversalOf = tx.ReversalOf().String()
	}
	return view
}
