package seed

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDeterministicUUIDIsStable(t *testing.T) {
	first := deterministicUUID("account:equity:retained")
	second := deterministicUUID("account:equity:retained")

	if first != second {
		t.Fatalf("the same name produced two UUIDs: %s and %s", first, second)
	}
	if first.Version() != 5 {
		t.Errorf("Version() = %d, want 5", first.Version())
	}

	other := deterministicUUID("account:equity:other")
	if first == other {
		t.Error("different names produced the same UUID")
	}
}

func TestDeterministicUUIDIsIndependentOfProcess(t *testing.T) {
	got := uuid.NewSHA1(namespace, []byte("account:equity:retained"))

	if got != deterministicUUID("account:equity:retained") {
		t.Errorf("deterministicUUID diverged from uuid.NewSHA1")
	}
}

func TestEntityReferencesAreObviouslySynthetic(t *testing.T) {
	for i := 1; i <= 3; i++ {
		if got := customerRef(i); got[:9] != "cus_demo_" {
			t.Errorf("customerRef(%d) = %q, which does not read as synthetic", i, got)
		}
		if got := merchantRef(i); got[:9] != "mer_demo_" {
			t.Errorf("merchantRef(%d) = %q, which does not read as synthetic", i, got)
		}
	}
}

func TestDefaultConfigIsSensible(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Seed == 0 {
		t.Error("Seed is zero; the data would not be reproducible")
	}
	if cfg.Customers < 20 {
		t.Errorf("Customers = %d: too few for a paged list to be visible", cfg.Customers)
	}
	if cfg.HistoryDays < 90 {
		t.Errorf("HistoryDays = %d: too short for a trend to have shape", cfg.HistoryDays)
	}
	if cfg.Anchor.IsZero() {
		t.Error("Anchor is zero")
	}
	if !cfg.Anchor.Equal(cfg.Anchor.Truncate(24 * time.Hour)) {
		t.Error("Anchor should be truncated to a day boundary so re-runs on one day agree")
	}
}

