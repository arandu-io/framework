// Transactions, answered by github.com/arandu-io/hesape/database.
//
// The open transaction travels on the context under a key hesape owns, and the
// handle is the same type on both sides, so a transaction opened through this
// name is the transaction hesape/database.InTransaction reports -- which is
// what the outbox asks before it agrees to store an event.

package data

import (
	"context"

	"github.com/arandu-io/hesape/database"
)

// Tx is an instrumented transaction.
//
// Statements run through it are recorded on the Collector exactly like the ones
// outside, which matters more than it sounds: a query that only misbehaves
// inside a transaction is the one nobody can see on the debug page.
type Tx = database.Tx

// Transaction runs fn inside a database transaction.
//
// Every statement issued through the same *DB while fn runs joins it, because
// the transaction travels on the context. Returning an error rolls back;
// returning nil commits. A panic rolls back and keeps panicking -- swallowing
// it would leave the caller believing the write happened.
//
// A Transaction inside a Transaction joins the outer one rather than opening a
// second. There are no savepoints: partial rollback is a second failure mode
// for the same operation, and the shape this framework wants is one write, one
// outcome.
func Transaction(ctx context.Context, db *DB, fn func(context.Context) error) error {
	return database.Transaction(ctx, db, fn)
}

// InTransaction reports whether the context is inside a transaction on db.
//
// The outbox uses it to refuse to store an event outside a transaction, which
// is the whole guarantee: an event written next to a row that rolled back is
// worse than no event at all.
//
// It takes the handle because "in a transaction" is only meaningful about one
// database. An outbox on the analytics handle is not protected by a transaction
// open on the primary.
func InTransaction(ctx context.Context, db *DB) bool { return database.InTransaction(ctx, db) }
