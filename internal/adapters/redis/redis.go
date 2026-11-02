// Package redis provides a Redis-backed lease helper to reduce duplicate processor calls.
package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Config configures the Redis client connection.
type Config struct {
	Addr     string
	Password string
	DB       int
}

// Open connects to Redis and verifies reachability with a ping.
func Open(ctx context.Context, cfg Config) (*goredis.Client, error) {
	client := goredis.NewClient(&goredis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close() //nolint:errcheck // best-effort cleanup on failure
		return nil, fmt.Errorf("pinging redis at %s: %w", cfg.Addr, err)
	}
	return client, nil
}

// Lease is a best-effort short-lived distributed lock.
type Lease struct {
	client *goredis.Client
	prefix string
}

// NewLease returns a new lease helper.
func NewLease(client *goredis.Client, prefix string) *Lease {
	return &Lease{client: client, prefix: prefix}
}

// Acquire tries to set the key if not exists (SETNX). Reports true if successfully acquired.
func (l *Lease) Acquire(ctx context.Context, key, holder string, ttl time.Duration) (bool, error) {
	ok, err := l.client.SetNX(ctx, l.prefix+key, holder, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("acquiring lease %q: %w", key, err)
	}
	return ok, nil
}

// releaseScript only deletes the key if its value matches the caller holder ID.
const releaseScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end
`

// Release frees the lease if still held by this holder.
func (l *Lease) Release(ctx context.Context, key, holder string) error {
	if err := l.client.Eval(ctx, releaseScript, []string{l.prefix + key}, holder).Err(); err != nil {
		if errors.Is(err, goredis.Nil) {
			return nil
		}
		return fmt.Errorf("releasing lease %q: %w", key, err)
	}
	return nil
}
