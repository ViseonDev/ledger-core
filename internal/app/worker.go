package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/ViseonDev/ledger-core/internal/adapters/postgres"
)

// Leaser defines an optional distributed lease.
type Leaser interface {
	Acquire(ctx context.Context, key, holder string, ttl time.Duration) (bool, error)
	Release(ctx context.Context, key, holder string) error
}

// Worker continuously polls for due payments and advances them step-by-step.
type Worker struct {
	store        *postgres.Store
	orchestrator *Orchestrator
	leaser       Leaser
	logger       *slog.Logger

	// PollInterval is the idle sleep duration when no payments are due.
	PollInterval time.Duration
	// LeaseDuration is how long a claimed payment remains invisible to other workers.
	LeaseDuration time.Duration
	// Holder identifies this worker instance in lease storage for diagnostics.
	Holder string
}

// NewWorker constructs a saga worker with default polling and lease intervals.
func NewWorker(store *postgres.Store, orchestrator *Orchestrator, leaser Leaser, logger *slog.Logger, holder string) *Worker {
	return &Worker{
		store:         store,
		orchestrator:  orchestrator,
		leaser:        leaser,
		logger:        logger,
		PollInterval:  500 * time.Millisecond,
		LeaseDuration: 30 * time.Second,
		Holder:        holder,
	}
}

// Run executes the polling loop until the context is canceled.
func (w *Worker) Run(ctx context.Context) error {
	w.logger.Info("saga worker started",
		slog.String("holder", w.Holder),
		slog.Duration("poll", w.PollInterval),
		slog.Duration("lease", w.LeaseDuration),
	)

	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()

	for {
		// Drain due payments first before waiting for the next tick.
		for {
			worked, err := w.Tick(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				w.logger.Error("saga worker tick failed", slog.String("error", err.Error()))
				break
			}
			if !worked {
				break
			}
		}

		select {
		case <-ctx.Done():
			w.logger.Info("saga worker stopped")
			return nil
		case <-ticker.C:
		}
	}
}

// Tick claims and runs at most one payment step. Reports whether work was done.
func (w *Worker) Tick(ctx context.Context) (bool, error) {
	id, err := w.store.ClaimDuePayment(ctx, w.LeaseDuration)
	if errors.Is(err, postgres.ErrNoPaymentDue) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if w.leaser != nil {
		acquired, leaseErr := w.leaser.Acquire(ctx, id.String(), w.Holder, w.LeaseDuration)
		if leaseErr != nil {
			// Best-effort lock: if Redis fails, PostgreSQL SKIP LOCKED still guarantees exclusivity.
			w.logger.Warn("lease unavailable; proceeding on the database claim alone",
				slog.String("payment_id", id.String()),
				slog.String("error", leaseErr.Error()),
			)
		} else if !acquired {
			w.logger.Debug("payment held by a peer", slog.String("payment_id", id.String()))
			return true, nil
		} else {
			defer func() {
				if relErr := w.leaser.Release(context.WithoutCancel(ctx), id.String(), w.Holder); relErr != nil {
					w.logger.Warn("releasing lease", slog.String("error", relErr.Error()))
				}
			}()
		}
	}

	if advErr := w.orchestrator.Advance(ctx, id); advErr != nil {
		w.logger.Error("advancing payment failed",
			slog.String("payment_id", id.String()),
			slog.String("error", advErr.Error()),
		)
		// Return true so we don't abort the entire drain loop; payment retry state is already saved.
		return true, nil
	}
	return true, nil
}
