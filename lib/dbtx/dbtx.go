// Package dbtx is the transactional-storage unit of work (ADR-0015):
// store implementations run against an Executor (satisfied by both *sql.DB
// and *sql.Tx), so a mutation's same-database writes — the entity row, the
// audit event, the search feed — can share one transaction and be atomic.
// The OpenFGA tuple write stays outside (a separate system).
package dbtx

import (
	"context"
	"database/sql"
	"fmt"
)

// Executor is the subset of *sql.DB / *sql.Tx that store methods use, so
// the same store code runs inside or outside a transaction.
type Executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

var (
	_ Executor = (*sql.DB)(nil)
	_ Executor = (*sql.Tx)(nil)
)

// InTx runs fn inside a transaction, committing on success and rolling
// back on error or panic. On panic the rollback runs and the panic is
// re-raised.
func InTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback() // no-op if already committed
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("dbtx: commit: %w", err)
	}
	committed = true
	return nil
}
