// Package postgres implements the ledger's persistence ports against
// PostgreSQL using pgx v5.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX is the subset of pgx used by this package's queries.
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// PoolConfig holds the tunable parts of the connection pool.
type PoolConfig struct {
	DSN             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
}

// Open creates and verifies a connection pool.
func Open(ctx context.Context, cfg PoolConfig) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parsing database url: %w", err)
	}

	if cfg.MaxConns > 0 {
		poolConfig.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		poolConfig.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime
	}

	// Cache statements in extended protocol mode for speed and pooler compatibility.
	poolConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	return pool, nil
}

// Store owns the connection pool and provides the transaction boundary.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store over the given pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Pool exposes the underlying pool for health checks and reads that do not require transactions.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// maxTxAttempts bounds the retry loop in InTx.
const maxTxAttempts = 4

// InTx runs fn inside a database transaction, committing if it returns nil and
// rolling back otherwise. It retries serialization failures and deadlocks with backoff.
func (s *Store) InTx(ctx context.Context, fn func(context.Context, DBTX) error) error {
	var lastErr error

	for attempt := range maxTxAttempts {
		if attempt > 0 {
			if err := sleepWithBackoff(ctx, attempt); err != nil {
				return err
			}
		}

		err := s.runOnce(ctx, fn)
		if err == nil {
			return nil
		}
		if !isRetryable(err) {
			return err
		}
		lastErr = err
	}

	return fmt.Errorf("transaction failed after %d attempts: %w", maxTxAttempts, lastErr)
}

func (s *Store) runOnce(ctx context.Context, fn func(context.Context, DBTX) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}

	// Rollback after a successful Commit is a no-op returning pgx.ErrTxClosed,
	// so this defer is always safe and prevents leaking connections on panics or early returns.
	defer func() {
		_ = tx.Rollback(ctx) //nolint:errcheck // Rollback after Commit is a safe no-op that returns ErrTxClosed
	}()

	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}

// isRetryable reports whether an error is a transient concurrency conflict that a retry can resolve.
func isRetryable(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	switch pgErr.Code {
	case "40001": // serialization_failure
		return true
	case "40P01": // deadlock_detected
		return true
	default:
		return false
	}
}

// sleepWithBackoff waits before a retry, using full jitter.
func sleepWithBackoff(ctx context.Context, attempt int) error {
	const base = 5 * time.Millisecond

	capDuration := base << (attempt - 1)
	if capDuration > 200*time.Millisecond {
		capDuration = 200 * time.Millisecond
	}
	delay := time.Duration(rand.Int64N(int64(capDuration)) + 1) // #nosec G404 -- backoff jitter does not need crypto rand

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
