// Package config loads service configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the fully-resolved service configuration.
type Config struct {
	HTTPAddr  string
	GRPCAddr  string
	LogLevel  string
	LogFormat string

	PostgresURL             string
	PostgresMaxConns        int32
	PostgresMinConns        int32
	PostgresMaxConnLifetime time.Duration

	ShutdownTimeout time.Duration
	RequestTimeout  time.Duration
}

// Load reads configuration from the environment, applying defaults where safe.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:                envString("LEDGER_HTTP_ADDR", ":8080"),
		GRPCAddr:                envString("LEDGER_GRPC_ADDR", ":9090"),
		LogLevel:                envString("LEDGER_LOG_LEVEL", "info"),
		LogFormat:               envString("LEDGER_LOG_FORMAT", "text"),
		PostgresURL:             os.Getenv("LEDGER_POSTGRES_URL"),
		PostgresMaxConns:        envInt32("LEDGER_POSTGRES_MAX_CONNS", 20),
		PostgresMinConns:        envInt32("LEDGER_POSTGRES_MIN_CONNS", 2),
		PostgresMaxConnLifetime: envDuration("LEDGER_POSTGRES_MAX_CONN_LIFETIME", time.Hour),
		ShutdownTimeout:         envDuration("LEDGER_SHUTDOWN_TIMEOUT", 20*time.Second),
		RequestTimeout:          envDuration("LEDGER_REQUEST_TIMEOUT", 15*time.Second),
	}

	return cfg, cfg.validate()
}

func (c Config) validate() error {
	var problems []string

	if c.PostgresURL == "" {
		problems = append(problems, "LEDGER_POSTGRES_URL is required")
	}
	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		problems = append(problems, fmt.Sprintf("LEDGER_LOG_LEVEL %q is not one of debug, info, warn, error", c.LogLevel))
	}
	switch strings.ToLower(c.LogFormat) {
	case "text", "json":
	default:
		problems = append(problems, fmt.Sprintf("LEDGER_LOG_FORMAT %q is not one of text, json", c.LogFormat))
	}
	if c.PostgresMaxConns < 1 {
		problems = append(problems, "LEDGER_POSTGRES_MAX_CONNS must be at least 1")
	}

	if len(problems) > 0 {
		// Report all problems together so we don't play whack-a-mole on restart.
		return errors.New("invalid configuration:\n  - " + strings.Join(problems, "\n  - "))
	}
	return nil
}

func envString(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}

func envInt32(key string, fallback int32) int32 {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return fallback
	}
	return int32(parsed)
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value, ok := os.LookupEnv(key)
	if !ok || value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
