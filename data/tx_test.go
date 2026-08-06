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
		if !data.InTransaction(ctx, db) {
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
	sqldb, state := newFakeDB()
	if data.InTransaction(context.Background(), data.Wrap(sqldb, data.DialectSQLite)) {
		t.Fatal("a bare context reports a transaction")
	}

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

// TestATransactionDoesNotCaptureAnotherDatabase is a bug an audit found.
//
// The transaction travelled on the context under a key that named no handle, so
// one context held one transaction for the whole process. An application with a
// primary and an analytics database -- or a primary and a read replica -- would
// open a transaction on the first, and every statement issued through the second
// while it ran executed against the first instead. No error, no warning, the
// write simply landed in the wrong database.
func TestATransactionDoesNotCaptureAnotherDatabase(t *testing.T) {
	primarySQL, primary := newFakeDB()
	analyticsSQL, analytics := newFakeDB()

	primaryDB := data.Wrap(primarySQL, data.DialectSQLite)
	analyticsDB := data.Wrap(analyticsSQL, data.DialectSQLite)

	err := data.Transaction(context.Background(), primaryDB, func(ctx context.Context) error {
		// This handle has no transaction open. It must not join the other one.
		if data.InTransaction(ctx, analyticsDB) {
			t.Error("a transaction on the primary reports as open on the analytics handle")
		}
		if _, err := analyticsDB.ExecContext(ctx, "INSERT INTO page_view (id) VALUES (?)", "1"); err != nil {
			return err
		}
		_, err := primaryDB.ExecContext(ctx, "INSERT INTO customer (id) VALUES (?)", "1")
		return err
	})
	if err != nil {
		t.Fatalf("Transaction: %v", err)
	}

	// Each statement has to reach the database it was issued through. With the
	// bug the analytics fake sees nothing and the primary sees both, because
	// both ran on the primary's transaction.
	if !analytics.sawStatement("page_view") {
		t.Error("the analytics write never reached the analytics database")
	}
	if analytics.sawStatement("customer") {
		t.Error("a primary write reached the analytics database")
	}
	if !primary.sawStatement("customer") {
		t.Error("the primary write never reached the primary")
	}
	if primary.sawStatement("page_view") {
		t.Error("the analytics write ran against the primary, inside its transaction")
	}
}
