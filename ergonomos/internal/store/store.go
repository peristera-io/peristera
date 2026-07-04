// Package store holds the Postgres implementations of the lib ports the
// task domain depends on (task.Repo, pii.PseudonymStore, audit.Sink,
// search.Index), plus goose migrations. Each store is a thin type over a
// shared *sql.DB — separate types because several share the method name
// Delete with different meanings.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // "pgx" database/sql driver
	"github.com/pressly/goose/v3"

	"github.com/peristera-io/peristera/ergonomos/internal/task"
	"github.com/peristera-io/peristera/lib/audit"
	"github.com/peristera-io/peristera/lib/dbtx"
	"github.com/peristera-io/peristera/lib/pgconv"
	"github.com/peristera-io/peristera/lib/pii"
	"github.com/peristera-io/peristera/lib/search"
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

// storesFor builds the task convention bundle over an executor (the pool
// for reads, a transaction inside InTx) — ADR-0015.
func (d *DB) storesFor(e dbtx.Executor) task.Stores {
	return task.Stores{
		Tasks:  &TaskRepo{db: e},
		Audit:  audit.NewEmitter(&pgconv.AuditSink{DB: e}, pii.NewPseudonyms(&pgconv.PseudonymStore{DB: e})),
		Search: search.NewFeeder(&pgconv.SearchIndex{DB: e}),
	}
}

// Reader returns a non-transactional store bundle (reads, export/erase).
func (d *DB) Reader() task.Stores { return d.storesFor(d.sql) }

// InTx runs fn with a transaction-bound store bundle, atomically.
func (d *DB) InTx(ctx context.Context, fn func(task.Stores) error) error {
	return dbtx.InTx(ctx, d.sql, func(tx *sql.Tx) error { return fn(d.storesFor(tx)) })
}

var _ task.TxRunner = (*DB)(nil)
