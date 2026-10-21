// Package uuidgen mints the identifiers the domain refuses to generate itself.
package uuidgen

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
)

// Generator produces UUIDv7 identifiers.
type Generator struct{}

// New returns a UUIDv7 generator.
func New() Generator { return Generator{} }

// NewAccountID implements ports.IDGenerator.
func (Generator) NewAccountID() (ledger.AccountID, error) {
	raw, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generating account id: %w", err)
	}
	return ledger.ParseAccountID(raw.String())
}

// NewTransactionID implements ports.IDGenerator.
func (Generator) NewTransactionID() (ledger.TransactionID, error) {
	raw, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generating transaction id: %w", err)
	}
	return ledger.ParseTransactionID(raw.String())
}
