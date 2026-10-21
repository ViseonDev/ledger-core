package postgres

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
)

// PostgreSQL SQLSTATE codes this adapter reacts to.
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
	sqlStateCheckViolation      = "23514"
	sqlStateJournalAppendOnly   = "LG001" // raised by ledger_forbid_mutation()
)

// Constraint names matching migrations.
const (
	constraintAccountsCodeKey      = "accounts_code_key"
	constraintAccountsPkey         = "accounts_pkey"
	constraintTransactionsPkey     = "transactions_pkey"
	constraintTransactionsReversal = "transactions_reversal_of_key"
)

// translateError converts a driver error into a domain error.
func translateError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return err // callers decide which "not found" this is
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}

	switch pgErr.Code {
	case sqlStateJournalAppendOnly:
		return fmt.Errorf("%w: %s", ledger.ErrJournalImmutable, pgErr.Message)

	case sqlStateUniqueViolation:
		switch pgErr.ConstraintName {
		case constraintAccountsCodeKey, constraintAccountsPkey:
			return fmt.Errorf("%w: %s", ledger.ErrAccountExists, pgErr.Detail)
		case constraintTransactionsPkey:
			return fmt.Errorf("%w: %s", ledger.ErrTransactionExists, pgErr.Detail)
		case constraintTransactionsReversal:
			return fmt.Errorf("%w: %s", ledger.ErrAlreadyReversed, pgErr.Detail)
		}

	case sqlStateForeignKeyViolation:
		// An entry referencing a missing account is the main foreign key violation callers can trigger.
		if pgErr.ConstraintName == "entries_account_id_fkey" {
			return fmt.Errorf("%w: %s", ledger.ErrAccountNotFound, pgErr.Detail)
		}

	case sqlStateCheckViolation:
		return fmt.Errorf("database rejected the write (%s): %w", pgErr.ConstraintName, err)
	}

	return err
}
