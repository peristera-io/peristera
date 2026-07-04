package dbtx

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite" // pure-Go sqlite for the test
)

func open(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func count(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestInTxCommits(t *testing.T) {
	db := open(t)
	defer db.Close()
	err := InTx(context.Background(), db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `INSERT INTO t (id) VALUES (1), (2)`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if count(t, db) != 2 {
		t.Errorf("want 2 rows committed, got %d", count(t, db))
	}
}

func TestInTxRollsBackOnError(t *testing.T) {
	db := open(t)
	defer db.Close()
	sentinel := errors.New("boom")
	err := InTx(context.Background(), db, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(context.Background(), `INSERT INTO t (id) VALUES (1)`); e != nil {
			return e
		}
		return sentinel // abort after a write
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel error, got %v", err)
	}
	if count(t, db) != 0 {
		t.Errorf("error must roll back the write, got %d rows", count(t, db))
	}
}

func TestInTxRollsBackOnPanic(t *testing.T) {
	db := open(t)
	defer db.Close()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic should propagate")
			}
		}()
		_ = InTx(context.Background(), db, func(tx *sql.Tx) error {
			_, _ = tx.ExecContext(context.Background(), `INSERT INTO t (id) VALUES (1)`)
			panic("kaboom")
		})
	}()
	if count(t, db) != 0 {
		t.Errorf("panic must roll back the write, got %d rows", count(t, db))
	}
}
