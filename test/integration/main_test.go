package integration

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/ViseonDev/ledger-core/internal/adapters/postgres"
)

const (
	postgresImage = "postgres:18.6-alpine"
	templateDB    = "ledger_template"
	adminDB       = "postgres"
	dbUser        = "ledger"
	dbPassword    = "ledger_integration_password" // #nosec G101 throwaway container
)

var (
	// adminDSN points at the container's default database and is used to CREATE and DROP per-test databases.
	adminDSN string

	// dbCounter makes per-test database names unique.
	dbCounter atomic.Int64
)

// TestMain starts one PostgreSQL container for the whole package, migrates a
// template database, and tears the container down at the end.
func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		log.Println("integration tests skipped: -short")
		os.Exit(0)
	}

	code, err := run(m)
	if err != nil {
		log.Printf("integration setup failed: %v", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func run(m *testing.M) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	container, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase(adminDB),
		tcpostgres.WithUsername(dbUser),
		tcpostgres.WithPassword(dbPassword),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(container); termErr != nil {
			log.Printf("terminating postgres container: %v", termErr)
		}
	}()
	if err != nil {
		return 0, fmt.Errorf("starting postgres: %w", err)
	}

	adminDSN, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return 0, fmt.Errorf("building connection string: %w", err)
	}

	if err = buildTemplate(ctx); err != nil {
		return 0, fmt.Errorf("building template database: %w", err)
	}

	return m.Run(), nil
}

// buildTemplate creates the template database and migrates it to the latest schema once.
func buildTemplate(ctx context.Context) error {
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return fmt.Errorf("connecting as admin: %w", err)
	}
	defer func() { _ = admin.Close(ctx) }()

	if _, execErr := admin.Exec(ctx, "CREATE DATABASE "+templateDB); execErr != nil {
		return fmt.Errorf("creating template: %w", execErr)
	}

	if err = postgres.Migrate(dsnFor(templateDB)); err != nil {
		return fmt.Errorf("migrating template: %w", err)
	}
	return nil
}

// newTestDB creates a fresh database for one test and returns a Store over it.
func newTestDB(t *testing.T) *postgres.Store {
	t.Helper()

	ctx := t.Context()
	name := fmt.Sprintf("ledger_test_%d", dbCounter.Add(1))

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connecting as admin: %v", err)
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()

	if _, execErr := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", name, templateDB)); execErr != nil {
		t.Fatalf("creating test database %s: %v", name, execErr)
	}

	pool, err := postgres.Open(ctx, postgres.PoolConfig{
		DSN:      dsnFor(name),
		MaxConns: 16,
	})
	if err != nil {
		t.Fatalf("opening pool for %s: %v", name, err)
	}

	t.Cleanup(func() {
		pool.Close()
		dropDatabase(name)
	})

	return postgres.NewStore(pool)
}

func dropDatabase(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		log.Printf("dropping %s: connect: %v", name, err)
		return
	}
	defer func() { _ = admin.Close(ctx) }()

	if _, execErr := admin.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", name)); execErr != nil {
		log.Printf("dropping %s: %v", name, execErr)
	}
}

func dsnFor(database string) string {
	base, query, _ := strings.Cut(adminDSN, "?")
	trimmed := base[:strings.LastIndex(base, "/")]
	if query == "" {
		return trimmed + "/" + database
	}
	return trimmed + "/" + database + "?" + query
}

// rawPool opens a second pool to the same database as a Store.
//
//nolint:unused // available for tests that need to observe state through an unmanaged connection
func rawPool(t *testing.T, database string) *pgxpool.Pool {
	t.Helper()

	pool, err := pgxpool.New(t.Context(), dsnFor(database))
	if err != nil {
		t.Fatalf("opening raw pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
