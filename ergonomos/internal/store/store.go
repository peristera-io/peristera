// Package store holds the Postgres implementations of the lib ports the
// task domain depends on (task.Repo, pii.PseudonymStore, audit.Sink,
// search.Index), plus goose migrations. Each store is a thin type over a
// shared *sql.DB — separate types because several share the method name
// Delete with different meanings.
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

// DB owns the connection and vends the typed stores.
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

// Migrate runs the embedded goose migrations up (ADR-0014). Called on boot
// before serving.
func (d *DB) Migrate() error {
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.Up(d.sql, "migrations")
}

// Close closes the pool.
func (d *DB) Close() error { return d.sql.Close() }

func (d *DB) Tasks() *TaskRepo            { return &TaskRepo{db: d.sql} }
func (d *DB) Pseudonyms() *PseudonymStore { return &PseudonymStore{db: d.sql} }
func (d *DB) Audit() *AuditSink           { return &AuditSink{db: d.sql} }
func (d *DB) Search() *SearchIndex        { return &SearchIndex{db: d.sql} }
