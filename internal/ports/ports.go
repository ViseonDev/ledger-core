// Package ports declares the interfaces the application depends on and the
// errors that cross layer boundaries without belonging to the domain.
package ports

import (
	"context"
	"errors"
	"time"

	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
)

// Errors that cross layers but are not domain rules.
var (
	// ErrIdempotencyConflict means an idempotency key was reused with a
	// different request body.
	ErrIdempotencyConflict = errors.New("idempotency: key reused with a different request body")

	// ErrIdempotencyKeyRequired means a mutating request arrived without a key.
	ErrIdempotencyKeyRequired = errors.New("idempotency: key is required")

	// ErrIdempotencyKeyTooLong means the supplied key exceeded the storable length.
	ErrIdempotencyKeyTooLong = errors.New("idempotency: key is too long")
)

// AccountStore persists and retrieves accounts.
type AccountStore interface {
	OpenAccount(ctx context.Context, account ledger.Account) error
	CloseAccount(ctx context.Context, id ledger.AccountID) error
	AccountByID(ctx context.Context, id ledger.AccountID) (ledger.Account, error)
	AccountByCode(ctx context.Context, code ledger.AccountCode) (ledger.Account, error)
	ListAccounts(ctx context.Context, prefix string, limit int) ([]ledger.Account, error)
}

// Journal persists and retrieves the double-entry journal.
type Journal interface {
	Post(ctx context.Context, tx *ledger.Transaction) error
	TransactionByID(ctx context.Context, id ledger.TransactionID) (*ledger.Transaction, error)
	Balance(ctx context.Context, id ledger.AccountID) (ledger.Balance, error)
	RecomputeBalance(ctx context.Context, id ledger.AccountID) (ledger.Balance, error)
}

// IDGenerator mints new identifiers.
type IDGenerator interface {
	NewAccountID() (ledger.AccountID, error)
	NewTransactionID() (ledger.TransactionID, error)
}

// Clock reports the current time.
type Clock interface {
	Now() time.Time
}
