package ledger

import (
	"testing"
	"time"
)

func TestPostedEvent(t *testing.T) {
	tx := newPayment(t)

	event, err := tx.PostedEvent()
	if err != nil {
		t.Fatalf("PostedEvent returned unexpected error: %v", err)
	}

	if event.EventName() != "ledger.transaction.posted" {
		t.Errorf("EventName() = %q", event.EventName())
	}
	if event.TransactionID != tx.ID() {
		t.Errorf("TransactionID = %q, want %q", event.TransactionID, tx.ID())
	}
	if event.Amount != 25000 {
		t.Errorf("Amount = %d, want 25000", event.Amount)
	}
	if event.EntryCount != 3 {
		t.Errorf("EntryCount = %d, want 3", event.EntryCount)
	}
	if event.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", event.Currency)
	}
	if len(event.AccountIDs) != 3 {
		t.Errorf("AccountIDs has %d entries, want 3", len(event.AccountIDs))
	}
	if !event.OccurredAt().Equal(tx.PostedAt()) {
		t.Errorf("OccurredAt() = %v, want %v", event.OccurredAt(), tx.PostedAt())
	}
}

func TestReversedEvent(t *testing.T) {
	original := newPayment(t)

	if _, ok := original.ReversedEvent(); ok {
		t.Error("a non-reversal reported a ReversedEvent")
	}

	reversal, err := NewReversal(original, mustTransactionID(t, reversalIDText), postedAt.Add(time.Hour), "duplicate charge")
	if err != nil {
		t.Fatalf("NewReversal: %v", err)
	}

	event, ok := reversal.ReversedEvent()
	if !ok {
		t.Fatal("a reversal did not report a ReversedEvent")
	}
	if event.OriginalID != original.ID() {
		t.Errorf("OriginalID = %q, want %q", event.OriginalID, original.ID())
	}
	if event.Reason != "duplicate charge" {
		t.Errorf("Reason = %q, want %q", event.Reason, "duplicate charge")
	}
	if event.EventName() != "ledger.transaction.reversed" {
		t.Errorf("EventName() = %q", event.EventName())
	}
}

// Every event type must satisfy the Event interface.
// Checking this in tests catches any missing interface methods right away at test time.
func TestEventsImplementEventInterface(t *testing.T) {
	events := []Event{
		TransactionPosted{},
		TransactionReversed{},
		AccountOpened{},
		AccountClosed{},
	}

	for _, event := range events {
		if event.EventName() == "" {
			t.Errorf("%T has an empty EventName", event)
		}
	}
}
