package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadAppliesDefaults(t *testing.T) {
	t.Setenv("LEDGER_POSTGRES_URL", "postgres://localhost/ledger")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}

	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.PostgresMaxConns != 20 {
		t.Errorf("PostgresMaxConns = %d, want 20", cfg.PostgresMaxConns)
	}
	if cfg.ShutdownTimeout != 20*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 20s", cfg.ShutdownTimeout)
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("LEDGER_POSTGRES_URL", "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load succeeded without LEDGER_POSTGRES_URL, want an error")
	}
	if !strings.Contains(err.Error(), "LEDGER_POSTGRES_URL") {
		t.Errorf("error does not name the missing variable: %v", err)
	}
}

func TestLoadReportsEveryProblem(t *testing.T) {
	t.Setenv("LEDGER_POSTGRES_URL", "")
	t.Setenv("LEDGER_LOG_LEVEL", "chatty")
	t.Setenv("LEDGER_LOG_FORMAT", "yaml")

	_, err := Load()
	if err == nil {
		t.Fatal("Load succeeded with three invalid settings")
	}
	for _, want := range []string{"LEDGER_POSTGRES_URL", "LEDGER_LOG_LEVEL", "LEDGER_LOG_FORMAT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}

func TestInvalidDurationFallsBackToDefault(t *testing.T) {
	t.Setenv("LEDGER_POSTGRES_URL", "postgres://localhost/ledger")
	t.Setenv("LEDGER_REQUEST_TIMEOUT", "fifteen seconds")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}
	if cfg.RequestTimeout != 15*time.Second {
		t.Errorf("RequestTimeout = %v, want the 15s default", cfg.RequestTimeout)
	}
}
