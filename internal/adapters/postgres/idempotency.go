package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ViseonDev/ledger-core/internal/ports"
)

// Limits mirroring the CHECK constraints in migration 000004.
const (
	maxIdempotencyKeyLength      = 255
	maxIdempotencyEndpointLength = 128
)

// claimIdempotencyKeySQL atomically claims a key.
const claimIdempotencyKeySQL = `
INSERT INTO idempotency_records (key, endpoint, fingerprint, response)
VALUES ($1, $2, $3, 'null'::jsonb)
ON CONFLICT (key, endpoint) DO NOTHING
RETURNING key
`

const selectIdempotencyRecordSQL = `
SELECT fingerprint, response
FROM idempotency_records
WHERE key = $1 AND endpoint = $2
`

const completeIdempotencyRecordSQL = `
UPDATE idempotency_records
SET response = $3
WHERE key = $1 AND endpoint = $2
`

const purgeIdempotencyRecordsSQL = `
DELETE FROM idempotency_records
WHERE created_at < $1
`

// IdempotentRequest identifies one logical operation.
type IdempotentRequest struct {
	// Key is the caller-supplied idempotency key.
	Key string

	// Endpoint scopes the key, so that the same key used against two different
	// operations does not collide.
	Endpoint string

	// Body is the raw request body, fingerprinted so that reusing a key with a
	// different payload is rejected rather than replayed.
	Body []byte
}

func (r IdempotentRequest) validate() error {
	key := strings.TrimSpace(r.Key)
	switch {
	case key == "":
		return ports.ErrIdempotencyKeyRequired
	case len(key) > maxIdempotencyKeyLength:
		return fmt.Errorf("%w: %d bytes, limit is %d", ports.ErrIdempotencyKeyTooLong, len(key), maxIdempotencyKeyLength)
	case r.Endpoint == "":
		return errors.New("idempotency: endpoint is required")
	case len(r.Endpoint) > maxIdempotencyEndpointLength:
		return fmt.Errorf("idempotency: endpoint %q exceeds %d bytes", r.Endpoint, maxIdempotencyEndpointLength)
	default:
		return nil
	}
}

// Outcome is the result of running work under an idempotency key.
type Outcome[T any] struct {
	// Value is the result, freshly computed, or decoded from the stored response on a replay.
	Value T

	// Replayed reports whether this call returned a previously stored result
	// rather than doing fresh work.
	Replayed bool
}

// Execute runs work exactly once for a given (key, endpoint), inside a single
// database transaction that also records the key.
func Execute[T any](
	ctx context.Context,
	store *Store,
	req IdempotentRequest,
	work func(context.Context, DBTX) (T, error),
) (Outcome[T], error) {
	var outcome Outcome[T]

	if err := req.validate(); err != nil {
		return outcome, err
	}
	fingerprint, err := Fingerprint(req.Body)
	if err != nil {
		return outcome, err
	}

	err = store.InTx(ctx, func(ctx context.Context, db DBTX) error {
		claimed, claimErr := claimKey(ctx, db, req, fingerprint[:])
		if claimErr != nil {
			return claimErr
		}

		if !claimed {
			value, replayErr := replay[T](ctx, db, req, fingerprint[:])
			if replayErr != nil {
				return replayErr
			}
			outcome = Outcome[T]{Value: value, Replayed: true}
			return nil
		}

		value, workErr := work(ctx, db)
		if workErr != nil {
			// Returning an error rolls back the transaction, which removes the claim.
			return workErr
		}

		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			return fmt.Errorf("idempotency: encoding result for key %q: %w", req.Key, marshalErr)
		}
		if _, execErr := db.Exec(ctx, completeIdempotencyRecordSQL, req.Key, req.Endpoint, encoded); execErr != nil {
			return fmt.Errorf("idempotency: recording result for key %q: %w", req.Key, translateError(execErr))
		}

		outcome = Outcome[T]{Value: value}
		return nil
	})
	if err != nil {
		var zero Outcome[T]
		return zero, err
	}
	return outcome, nil
}

// claimKey attempts to take ownership of the key. It reports whether this caller won.
func claimKey(ctx context.Context, db DBTX, req IdempotentRequest, fingerprint []byte) (bool, error) {
	var claimedKey string

	err := db.QueryRow(ctx, claimIdempotencyKeySQL, req.Key, req.Endpoint, fingerprint).Scan(&claimedKey)
	if errors.Is(err, pgx.ErrNoRows) {
		// DO NOTHING fired: someone else owns the key.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("idempotency: claiming key %q: %w", req.Key, translateError(err))
	}
	return true, nil
}

// replay loads a stored result, rejecting a key reused with a different body.
func replay[T any](ctx context.Context, db DBTX, req IdempotentRequest, fingerprint []byte) (T, error) {
	var (
		zero           T
		storedPrint    []byte
		storedResponse []byte
	)

	err := db.QueryRow(ctx, selectIdempotencyRecordSQL, req.Key, req.Endpoint).Scan(&storedPrint, &storedResponse)
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, fmt.Errorf("idempotency: key %q vanished between claim and read", req.Key)
	}
	if err != nil {
		return zero, fmt.Errorf("idempotency: reading key %q: %w", req.Key, translateError(err))
	}

	if !bytes.Equal(storedPrint, fingerprint) {
		return zero, fmt.Errorf("%w: key %q", ports.ErrIdempotencyConflict, req.Key)
	}

	var value T
	if unmarshalErr := json.Unmarshal(storedResponse, &value); unmarshalErr != nil {
		return zero, fmt.Errorf("idempotency: decoding stored result for key %q: %w", req.Key, unmarshalErr)
	}
	return value, nil
}

// PurgeIdempotencyRecords deletes records older than the cutoff and returns how many were removed.
func (s *Store) PurgeIdempotencyRecords(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, purgeIdempotencyRecordsSQL, olderThan)
	if err != nil {
		return 0, fmt.Errorf("purging idempotency records: %w", translateError(err))
	}
	return tag.RowsAffected(), nil
}
