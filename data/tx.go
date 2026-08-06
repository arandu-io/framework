package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/arandu-io/framework/observability"
)

// txKey carries the open transaction on the context.
//
// On the context rather than in every repository signature, for one reason: the
// Repository contract already takes a Grant, and adding a second required
// argument to five methods would make every generated repository exist in two
// shapes -- one for transactions and one for outside them. That is the second
// way to do one thing that RULE 9 forbids.
//
// It is also how the outbox works at all: events.Outbox.Store writes through the
// same DB handle the repository just wrote through, and lands in the same
// transaction without anyone threading a handle through the call.
//
// The key carries the handle the transaction belongs to. It used to be an empty
// struct, so one context held one transaction no matter how many databases the
// application had: a write issued through the analytics handle inside a
// transaction on the primary joined the primary's transaction and executed
// against the wrong database, silently and with no error. Found by audit.
type txKey struct{ db *DB }

// Tx is an instrumented transaction.
//
// Statements run through it are recorded on the Collector exactly like the ones
// outside, which matters more than it sounds: a query that only misbehaves
// inside a transaction is the one nobody can see on the debug page.
type Tx struct {
	inner   *sql.Tx
	dialect Dialect
}

// Transaction runs fn inside a database transaction.
//
// Every statement issued through the same *DB while fn runs joins it, because
// the transaction travels on the context. Returning an error rolls back;
// returning nil commits. A panic rolls back and keeps panicking -- swallowing it
// would leave the caller believing the write happened.
//
// A Transaction inside a Transaction joins the outer one rather than opening a
// second. There are no savepoints: partial rollback is a second failure mode for
// the same operation, and the shape this framework wants is one write, one
// outcome.
func Transaction(ctx context.Context, db *DB, fn func(context.Context) error) error {
	if db == nil {
		return errors.New("data: Transaction needs a database handle")
	}
	// Reentrant per handle: a transaction on this database joins; one on a
	// different database opens its own, because they are different databases.
	if _, ok := ctx.Value(txKey{db}).(*Tx); ok {
		return fn(ctx)
	}

	inner, err := db.inner.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning the transaction: %w", err)
	}
	tx := &Tx{inner: inner, dialect: db.dialect}

	committed := false
	defer func() {
		if committed {
			return
		}
		// A rollback that fails after the caller already failed has nothing left
		// to report to: the original error is the one worth keeping.
		_ = inner.Rollback()
	}()

	if err := fn(context.WithValue(ctx, txKey{db}, tx)); err != nil {
		return err
	}

	if err := inner.Commit(); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	committed = true
	return nil
}

// InTransaction reports whether the context is inside a transaction on db.
//
// The outbox uses it to refuse to store an event outside a transaction, which is
// the whole guarantee: an event written next to a row that rolled back is worse
// than no event at all.
//
// It takes the handle because "in a transaction" is only meaningful about one
// database. An outbox on the analytics handle is not protected by a transaction
// open on the primary.
func InTransaction(ctx context.Context, db *DB) bool {
	_, ok := ctx.Value(txKey{db}).(*Tx)
	return ok
}

// txFrom returns the open transaction on db, if any.
func txFrom(ctx context.Context, db *DB) (*Tx, bool) {
	tx, ok := ctx.Value(txKey{db}).(*Tx)
	return tx, ok
}

func (t *Tx) queryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	query = t.dialect.Rebind(query)
	start := time.Now()
	rows, err := t.inner.QueryContext(ctx, query, args...)
	observability.FromContext(ctx).RecordQuery(query, args, time.Since(start), -1, err)
	return rows, err
}

func (t *Tx) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	query = t.dialect.Rebind(query)
	start := time.Now()
	res, err := t.inner.ExecContext(ctx, query, args...)
	rows := -1
	if err == nil && res != nil {
		if n, e := res.RowsAffected(); e == nil {
			rows = int(n)
		}
	}
	observability.FromContext(ctx).RecordQuery(query, args, time.Since(start), rows, err)
	return res, err
}

func (t *Tx) queryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	query = t.dialect.Rebind(query)
	start := time.Now()
	row := t.inner.QueryRowContext(ctx, query, args...)
	observability.FromContext(ctx).RecordQuery(query, args, time.Since(start), 1, nil)
	return row
}
