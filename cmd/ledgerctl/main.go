// Command ledgerctl is the operator CLI: migrations, seeding, and reconciliation.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ViseonDev/ledger-core/internal/adapters/postgres"
	"github.com/ViseonDev/ledger-core/internal/platform/config"
	"github.com/ViseonDev/ledger-core/internal/platform/observability"
	"github.com/ViseonDev/ledger-core/internal/seed"
)

const usage = `ledgerctl: operator tools for the ledger

Usage:
  ledgerctl migrate up          Apply pending migrations
  ledgerctl migrate down        Revert every migration (destructive)
  ledgerctl migrate version     Print the current schema version
  ledgerctl seed [flags]        Populate with deterministic demo data
  ledgerctl reconcile           Check every cached balance against the journal
  ledgerctl purge-idempotency   Delete idempotency records older than the TTL

Seed flags:
  -seed uint      RNG seed (default 20260817)
  -anchor string  Most recent activity date, RFC 3339 (default: today)
  -customers int  Number of demo customers (default 40)
  -days int       Days of history (default 180)
`

func printOut(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stdout, format, args...) //nolint:errcheck // CLI stdout
}

func printOutLn(msg string) {
	_, _ = fmt.Fprintln(os.Stdout, msg) //nolint:errcheck // CLI stdout
}

func printErr(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format, args...) //nolint:errcheck // CLI stderr
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		printErr("ledgerctl: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printOut("%s", usage)
		return errors.New("no command given")
	}

	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printOut("%s", usage)
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := observability.NewLogger(cfg.LogLevel, cfg.LogFormat)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	switch args[0] {
	case "migrate":
		return runMigrate(args[1:], cfg.PostgresURL)
	case "seed":
		return runSeed(ctx, args[1:], cfg, logger)
	case "reconcile":
		return runReconcile(ctx, cfg)
	case "purge-idempotency":
		return runPurge(ctx, cfg)
	default:
		printOut("%s", usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runMigrate(args []string, dsn string) error {
	if len(args) == 0 {
		return errors.New("migrate needs a subcommand: up, down, or version")
	}

	switch args[0] {
	case "up":
		if err := postgres.Migrate(dsn); err != nil {
			return err
		}
		printOutLn("migrations applied")
		return nil

	case "down":
		// Down migrations destroy the journal. Pass confirmation flag to run.
		if len(args) < 2 || args[1] != "--yes-destroy-everything" {
			return errors.New("migrate down destroys the journal; pass --yes-destroy-everything to confirm")
		}
		if err := postgres.MigrateDown(dsn); err != nil {
			return err
		}
		printOutLn("migrations reverted")
		return nil

	case "version":
		version, dirty, err := postgres.MigrationVersion(dsn)
		if err != nil {
			return err
		}
		printOut("version=%d dirty=%t\n", version, dirty)
		if dirty {
			return errors.New("the schema is dirty: a migration failed partway and needs manual repair")
		}
		return nil

	default:
		return fmt.Errorf("unknown migrate subcommand %q", args[0])
	}
}

func runSeed(ctx context.Context, args []string, cfg config.Config, logger *slog.Logger) error {
	seedCfg := seed.DefaultConfig()

	flags := flag.NewFlagSet("seed", flag.ContinueOnError)
	rngSeed := flags.Uint64("seed", seedCfg.Seed, "RNG seed")
	anchor := flags.String("anchor", "", "most recent activity date, RFC 3339")
	customers := flags.Int("customers", seedCfg.Customers, "number of demo customers")
	days := flags.Int("days", seedCfg.HistoryDays, "days of history")
	if err := flags.Parse(args); err != nil {
		return err
	}

	seedCfg.Seed = *rngSeed
	seedCfg.Customers = *customers
	seedCfg.HistoryDays = *days
	if *anchor != "" {
		parsed, err := time.Parse(time.RFC3339, *anchor)
		if err != nil {
			return fmt.Errorf("parsing -anchor: %w", err)
		}
		seedCfg.Anchor = parsed.UTC()
	}

	store, closeStore, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeStore()

	result, err := seed.New(store, logger).Run(ctx, seedCfg)
	if err != nil {
		return err
	}

	printOut("accounts:     %d created, %d already present\n", result.AccountsCreated, result.AccountsSkipped)
	printOut("transactions: %d created, %d already present\n", result.TransactionsCreated, result.TransactionsSkipped)
	return nil
}

// runReconcile checks cached balances across all accounts against journal totals.
func runReconcile(ctx context.Context, cfg config.Config) error {
	store, closeStore, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeStore()

	accounts, err := store.ListAccounts(ctx, "", 1000)
	if err != nil {
		return err
	}

	var drifted int
	for _, account := range accounts {
		cached, bErr := store.Balance(ctx, account.ID())
		if bErr != nil {
			return bErr
		}
		authoritative, rErr := store.RecomputeBalance(ctx, account.ID())
		if rErr != nil {
			return rErr
		}

		if cached.Debits() != authoritative.Debits() || cached.Credits() != authoritative.Credits() {
			drifted++
			printOut("DRIFT %s: cached %v/%v, journal %v/%v\n",
				account.Code(),
				cached.Debits(), cached.Credits(),
				authoritative.Debits(), authoritative.Credits(),
			)
		}
	}

	printOut("checked %d accounts, %d drifted\n", len(accounts), drifted)
	if drifted > 0 {
		return fmt.Errorf("%d account balances disagree with the journal", drifted)
	}
	return nil
}

func runPurge(ctx context.Context, cfg config.Config) error {
	store, closeStore, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeStore()

	removed, err := store.PurgeIdempotencyRecords(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		return err
	}
	printOut("purged %d idempotency records\n", removed)
	return nil
}

func openStore(ctx context.Context, cfg config.Config) (*postgres.Store, func(), error) {
	pool, err := postgres.Open(ctx, postgres.PoolConfig{
		DSN:      cfg.PostgresURL,
		MaxConns: cfg.PostgresMaxConns,
	})
	if err != nil {
		return nil, nil, err
	}
	return postgres.NewStore(pool), pool.Close, nil
}
