package ledger

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrReversalReasonRequired means someone tried to create a reversal without giving a reason.
// Reversals must always document why they were made for proper auditability.
var ErrReversalReasonRequired = errors.New("ledger: reversal requires a reason")

// Metadata keys written by NewReversal.
const (
	// MetadataReversalOf stores the ID of the transaction being reversed.
	// Having it in metadata makes it immediately visible alongside the reason in logs and debug dumps.
	MetadataReversalOf = "reversal_of"

	// MetadataReversalReason stores the human explanation for why the reversal was made.
	MetadataReversalReason = "reversal_reason"
)

// NewReversal creates a new transaction that completely cancels out the original.
// It flips the direction of every single entry (Debits become Credits, Credits become Debits)
// while keeping the exact same accounts and amounts.
// In our ledger, rows are never deleted or edited; corrections always happen through reversals.
func NewReversal(original *Transaction, id TransactionID, reversedAt time.Time, reason string) (*Transaction, error) {
	if original == nil {
		return nil, errors.New("ledger: cannot reverse a nil transaction")
	}
	if id.IsZero() {
		return nil, fmt.Errorf("%w: reversal id is required", ErrInvalidID)
	}
	if id == original.ID() {
		return nil, fmt.Errorf("%w: reversal id must differ from the original", ErrInvalidID)
	}
	if reversedAt.IsZero() {
		return nil, ErrInvalidPostedAt
	}
	// Reversal cannot predate the original transaction (avoids time travel / negative balance anomalies).
	if reversedAt.Before(original.PostedAt()) {
		return nil, fmt.Errorf("%w: reversal at %s predates the original at %s",
			ErrInvalidPostedAt, reversedAt.UTC(), original.PostedAt())
	}
	trimmedReason := strings.TrimSpace(reason)
	if trimmedReason == "" {
		return nil, ErrReversalReasonRequired
	}

	entries := make([]Entry, 0, original.EntryCount())
	for _, entry := range original.entries {
		entries = append(entries, entry.Reverse())
	}

	metadata := map[string]string{
		MetadataReversalOf:     original.ID().String(),
		MetadataReversalReason: trimmedReason,
	}
	if len(trimmedReason) > MaxMetadataValueLength {
		return nil, fmt.Errorf("%w: reason exceeds %d bytes", ErrInvalidMetadata, MaxMetadataValueLength)
	}

	// Go through NewTransaction so reversals get the same checks.
	reversal, err := NewTransaction(TransactionParams{
		ID:        id,
		Reference: original.Reference(),
		Entries:   entries,
		PostedAt:  reversedAt,
		Metadata:  metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("building reversal: %w", err)
	}
	reversal.reversalOf = original.ID()

	return reversal, nil
}
