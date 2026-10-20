package ledger

import "time"

// Event is something that happened in the ledger, expressed in domain terms.
// Events are named in past tense and carry immutable facts.
type Event interface {
	// EventName returns a stable string identifier for routing (e.g. "ledger.transaction.posted").
	EventName() string

	// OccurredAt returns when this fact became true.
	OccurredAt() time.Time
}

// TransactionPosted records that a balanced transaction entered the journal.
type TransactionPosted struct {
	TransactionID TransactionID
	Reference     string
	Currency      string
	EntryCount    int
	AccountIDs    []AccountID
	Amount        int64 // total debits, in minor units (cents)
	PostedAt      time.Time
}

// EventName implements Event.
func (TransactionPosted) EventName() string { return "ledger.transaction.posted" }

// OccurredAt implements Event.
func (e TransactionPosted) OccurredAt() time.Time { return e.PostedAt }

// TransactionReversed records that an existing transaction was undone by a reversal.
type TransactionReversed struct {
	ReversalID TransactionID
	OriginalID TransactionID
	Reason     string
	ReversedAt time.Time
}

// EventName implements Event.
func (TransactionReversed) EventName() string { return "ledger.transaction.reversed" }

// OccurredAt implements Event.
func (e TransactionReversed) OccurredAt() time.Time { return e.ReversedAt }

// AccountOpened records that a new account was added to the chart of accounts.
type AccountOpened struct {
	AccountID AccountID
	Code      AccountCode
	Type      AccountType
	Currency  string
	OpenedAt  time.Time
}

// EventName implements Event.
func (AccountOpened) EventName() string { return "ledger.account.opened" }

// OccurredAt implements Event.
func (e AccountOpened) OccurredAt() time.Time { return e.OpenedAt }

// AccountClosed records that an account was closed and will no longer accept entries.
type AccountClosed struct {
	AccountID AccountID
	Code      AccountCode
	ClosedAt  time.Time
}

// EventName implements Event.
func (AccountClosed) EventName() string { return "ledger.account.closed" }

// OccurredAt implements Event.
func (e AccountClosed) OccurredAt() time.Time { return e.ClosedAt }

// PostedEvent builds the domain event describing this transaction entering the journal.
// Derived from the transaction's state, no pending events list.
func (t *Transaction) PostedEvent() (TransactionPosted, error) {
	amount, err := t.TotalDebits()
	if err != nil {
		return TransactionPosted{}, err
	}
	return TransactionPosted{
		TransactionID: t.id,
		Reference:     t.reference,
		Currency:      t.currency.Code(),
		EntryCount:    len(t.entries),
		AccountIDs:    t.AccountIDs(),
		Amount:        amount.Amount(),
		PostedAt:      t.postedAt,
	}, nil
}

// ReversedEvent returns the domain event describing this reversal,
// or false if the transaction is not a reversal.
func (t *Transaction) ReversedEvent() (TransactionReversed, bool) {
	if !t.IsReversal() {
		return TransactionReversed{}, false
	}
	return TransactionReversed{
		ReversalID: t.id,
		OriginalID: t.reversalOf,
		Reason:     t.metadata[MetadataReversalReason],
		ReversedAt: t.postedAt,
	}, true
}
