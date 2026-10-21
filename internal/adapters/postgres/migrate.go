package postgres

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/ViseonDev/ledger-core/migrations"
)

// Migrate applies every pending migration.
func Migrate(dsn string) error {
	m, closeFn, err := newMigrator(dsn)
	if err != nil {
		return err
	}
	defer closeFn()

	// ErrNoChange means the database is already at the latest version, which is
	// expected on restarts and is not an error.
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("applying migrations: %w", err)
	}
	return nil
}

// MigrateDown rolls every migration back. It exists for tests and for local
// development. Never call in deployment: down migration on a ledger destroys the journal.
func MigrateDown(dsn string) error {
	m, closeFn, err := newMigrator(dsn)
	if err != nil {
		return err
	}
	defer closeFn()

	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("reverting migrations: %w", err)
	}
	return nil
}

// MigrationVersion reports the current schema version and whether the last
// migration left the database dirty. A dirty database needs manual intervention.
func MigrationVersion(dsn string) (version uint, dirty bool, err error) {
	m, closeFn, err := newMigrator(dsn)
	if err != nil {
		return 0, false, err
	}
	defer closeFn()

	version, dirty, err = m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	return version, dirty, err
}

// newMigrator wires the embedded SQL files to a database handle.
func newMigrator(dsn string) (*migrate.Migrate, func(), error) {
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, nil, fmt.Errorf("reading embedded migrations: %w", err)
	}

	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing database url: %w", err)
	}

	db := stdlib.OpenDB(*config)

	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		_ = db.Close() //nolint:errcheck // best-effort cleanup on failure
		return nil, nil, fmt.Errorf("creating migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		_ = db.Close() //nolint:errcheck // best-effort cleanup on failure
		return nil, nil, fmt.Errorf("creating migrator: %w", err)
	}

	return m, func() { closeMigrator(m, db) }, nil
}

func closeMigrator(m *migrate.Migrate, db *sql.DB) {
	_, _ = m.Close() //nolint:errcheck // best-effort close on shutdown
	_ = db.Close()   //nolint:errcheck // best-effort close on shutdown
}
