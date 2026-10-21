package ledger

import "errors"

// Errors describing the outcome of an operation against the ledger, as opposed
// to the validation errors that live beside the type they validate.
var (
	// ErrAccountNotFound means no account exists with the given id or code.
	ErrAccountNotFound = errors.New("ledger: account not found")

	// ErrTransactionNotFound means no transaction exists with the given id.
	ErrTransactionNotFound = errors.New("ledger: transaction not found")

	// ErrAccountExists means an account with that code is already open.
	ErrAccountExists = errors.New("ledger: account already exists")

	// ErrTransactionExists means a transaction with that id is already posted.
	ErrTransactionExists = errors.New("ledger: transaction already posted")

	// ErrInsufficientFunds means a posting would take an account that does not
	// allow negative balances below zero in its normal direction.
	ErrInsufficientFunds = errors.New("ledger: insufficient funds")

	// ErrAlreadyReversed means the transaction already has a reversal. This is
	// detected by the unique index on transactions.reversal_of.
	ErrAlreadyReversed = errors.New("ledger: transaction already reversed")

	// ErrJournalImmutable means something attempted to UPDATE, DELETE or
	// TRUNCATE the journal. It should be impossible from application code and
	// indicates a bug rather than a user error.
	ErrJournalImmutable = errors.New("ledger: the journal is append-only")
)
