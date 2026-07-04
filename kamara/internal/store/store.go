// Package store holds Kamara's Postgres schema (goose migrations) and — in
// a later session — the object/version/chunk repositories built on the
// transactional-storage helper (root ADR-0015). This session lands the
// schema and the boot-time migration entry point.
//
// Wiring discipline for the repositories (next session): blob writes are a
// filesystem side effect OUTSIDE the DB transaction, so a chunk's blob must
// be durably stored (blob.Put, which fsyncs) BEFORE the transaction that
// commits its manifest/ref_count. A crash between the two then orphans a
// blob (harmless, GC-collectable) rather than dangling a manifest reference
// (corrupt).
package store

import (
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // "pgx" database/sql driver
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB owns the connection.
type DB struct{ sql *sql.DB }

// Open connects to Postgres (a pgx DSN) and pings it.
func Open(dsn string) (*DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &DB{sql: db}, nil
}

// Migrate runs the embedded goose migrations up (root ADR-0014).
func (d *DB) Migrate() error {
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(d.sql, "migrations")
}

// Close closes the pool.
func (d *DB) Close() error { return d.sql.Close() }

// SQL exposes the underlying handle (transitional — the repositories that
// use it via the ADR-0015 unit-of-work land next session).
func (d *DB) SQL() *sql.DB { return d.sql }
