package data_test

import (
	"context"
	"errors"
	"testing"

	"github.com/arandu-io/framework/data"
)

// TestTransactionCommits: the statements issued through the same handle while fn
// runs have to reach the transaction, or the whole guarantee is decorative.
func TestTransactionCommits(t *testing.T) {
	sqldb, state := newFakeDB()
	db := data.Wrap(sqldb, data.DialectSQLite)

	err := data.Transaction(context.Background(), db, func(ctx context.Context) error {
		if !data.InTransaction(ctx) {
			t.Error("the context does not report a transaction")
		}
		_, err := db.ExecContext(ctx, "INSERT INTO customer (id) VALUES (?)", "1")
		return err
	})
	if err != nil {
		t.Fatalf("Transaction: %v", err)
	}

	if !state.sawStatement("INSERT INTO customer") {
		t.Error("the statement never reached the database")
	}
	if !state.sawStatement("COMMIT") {
		t.Error("the transaction was not committed")
	}
}

// TestTransactionRollsBackOnError: the error is returned unchanged, because the
// caller's error is the one worth reading -- a rollback that also failed has
// nothing left to report to.
func TestTransactionRollsBackOnError(t *testing.T) {
	sqldb, state := newFakeDB()
	db := data.Wrap(sqldb, data.DialectSQLite)
	sentinel := errors.New("the rule said no")

	err := data.Transaction(context.Background(), db, func(ctx context.Context) error {
		_, _ = db.ExecContext(ctx, "INSERT INTO customer (id) VALUES (?)", "1")
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the caller's error", err)
	}
	if state.sawStatement("COMMIT") {
		t.Error("a failed transaction was committed")
	}
	if !state.sawStatement("ROLLBACK") {
		t.Error("the transaction was not rolled back")
	}
}

// TestAPanicRollsBackAndKeepsPanicking: swallowing it would leave the caller
// believing the write happened.
func TestAPanicRollsBackAndKeepsPanicking(t *testing.T) {
	sqldb, state := newFakeDB()
	db := data.Wrap(sqldb, data.DialectSQLite)

	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic was swallowed")
			}
		}()
		_ = data.Transaction(context.Background(), db, func(ctx context.Context) error {
			_, _ = db.ExecContext(ctx, "INSERT INTO customer (id) VALUES (?)", "1")
			panic("something went wrong")
		})
	}()

	if state.sawStatement("COMMIT") {
		t.Error("a transaction that panicked was committed")
	}
	if !state.sawStatement("ROLLBACK") {
		t.Error("the transaction was not rolled back")
	}
}

// TestNestedTransactionJoinsTheOuterOne: one write, one outcome. A second BEGIN
// would mean a partial rollback is possible, which is a second failure mode for
// the same operation.
func TestNestedTransactionJoinsTheOuterOne(t *testing.T) {
	sqldb, state := newFakeDB()
	db := data.Wrap(sqldb, data.DialectSQLite)

	err := data.Transaction(context.Background(), db, func(ctx context.Context) error {
		return data.Transaction(ctx, db, func(ctx context.Context) error {
			_, err := db.ExecContext(ctx, "INSERT INTO customer (id) VALUES (?)", "1")
			return err
		})
	})
	if err != nil {
		t.Fatalf("Transaction: %v", err)
	}

	commits := 0
	for _, s := range state.statements() {
		if s == "COMMIT" {
			commits++
		}
	}
	if commits != 1 {
		t.Fatalf("%d commits, want 1", commits)
	}
}

// TestOutsideATransactionNothingChanges: the handle must keep working exactly as
// before for the code that does not use transactions, which is most of it.
func TestOutsideATransactionNothingChanges(t *testing.T) {
	if data.InTransaction(context.Background()) {
		t.Fatal("a bare context reports a transaction")
	}

	sqldb, state := newFakeDB()
	db := data.Wrap(sqldb, data.DialectSQLite)
	if _, err := db.ExecContext(context.Background(), "DELETE FROM customer"); err != nil {
		t.Fatal(err)
	}
	if state.sawStatement("COMMIT") {
		t.Error("a statement outside a transaction opened one")
	}
}

// TestTheTransactionRebindsPlaceholders: a repository written with "?" has to
// keep working on Postgres inside a transaction too, and this is exactly the
// kind of thing that only breaks in the one code path nobody tested.
func TestTheTransactionRebindsPlaceholders(t *testing.T) {
	sqldb, state := newFakeDB()
	db := data.Wrap(sqldb, data.DialectPostgres)

	err := data.Transaction(context.Background(), db, func(ctx context.Context) error {
		_, err := db.ExecContext(ctx, "INSERT INTO customer (id, name) VALUES (?, ?)", "1", "x")
		return err
	})
	if err != nil {
		t.Fatalf("Transaction: %v", err)
	}
	if !state.sawStatement("VALUES ($1, $2)") {
		t.Errorf("placeholders were not rebound inside the transaction: %v", state.statements())
	}
}
