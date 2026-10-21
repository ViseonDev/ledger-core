// Command ledgerd runs the ledger service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/ViseonDev/ledger-core/internal/adapters/httpapi"
	"github.com/ViseonDev/ledger-core/internal/adapters/postgres"
	"github.com/ViseonDev/ledger-core/internal/adapters/uuidgen"
	"github.com/ViseonDev/ledger-core/internal/app"
	"github.com/ViseonDev/ledger-core/internal/platform/clock"
	"github.com/ViseonDev/ledger-core/internal/platform/config"
	"github.com/ViseonDev/ledger-core/internal/platform/observability"
)

func main() {
	if err := run(); err != nil {
		// slog is not configured yet if config loading failed, so this one
		// message goes to stderr directly.
		_, _ = fmt.Fprintf(os.Stderr, "ledgerd: %v\n", err) //nolint:errcheck
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := observability.NewLogger(cfg.LogLevel, cfg.LogFormat)
	metrics := observability.NewMetrics()

	// Graceful shutdown on SIGINT/SIGTERM (docker stop).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if migErr := postgres.Migrate(cfg.PostgresURL); migErr != nil {
		return fmt.Errorf("migrating database: %w", migErr)
	}
	logger.Info("database migrated")

	pool, poolErr := postgres.Open(ctx, postgres.PoolConfig{
		DSN:             cfg.PostgresURL,
		MaxConns:        cfg.PostgresMaxConns,
		MinConns:        cfg.PostgresMinConns,
		MaxConnLifetime: cfg.PostgresMaxConnLifetime,
	})
	if poolErr != nil {
		return fmt.Errorf("connecting to database: %w", poolErr)
	}
	defer pool.Close()

	store := postgres.NewStore(pool)
	ledgerService := app.NewLedger(store, uuidgen.New(), clock.New(), logger)

	server := httpapi.NewServer(httpapi.Config{
		Ledger:         ledgerService,
		Logger:         logger,
		Metrics:        metrics,
		RequestTimeout: cfg.RequestTimeout,
		Ready: func() error {
			pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			return pool.Ping(pingCtx)
		},
	})

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           server.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	group, groupCtx := errgroup.WithContext(ctx)

	group.Go(func() error {
		logger.Info("http server listening", slog.String("addr", cfg.HTTPAddr))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	})

	group.Go(func() error {
		// groupCtx: if another component dies, shut down too.
		<-groupCtx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()

		logger.Info("shutting down", slog.Duration("grace", cfg.ShutdownTimeout))
		return httpServer.Shutdown(shutdownCtx)
	})

	if err := group.Wait(); err != nil {
		return err
	}
	logger.Info("shutdown complete")
	return nil
}
