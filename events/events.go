// The outbox and the event types, answered by
// github.com/arandu-io/hesape/events.
//
// The types alias, so an event recorded by an entity written against this
// package is the value hesape stores, and a Grant minted through
// framework/security is the one its Store takes.

package events

import (
	"github.com/arandu-io/framework/data"
	hevents "github.com/arandu-io/hesape/events"
)

// Event is something that happened, in the past tense.
//
// The name is the vocabulary of the domain rather than of the database:
// "invoice.paid", not "invoice.updated". A consumer that has to diff two rows to
// learn what happened is a consumer coupled to your schema.
type Event = hevents.Event

// Recorder is what an entity embeds to collect its own events.
//
// The entity records; the service stores. That split is what keeps the entity
// free of a database handle and keeps the event next to the rule that produced
// it.
type Recorder = hevents.Recorder

// Stored is one row of the outbox.
type Stored = hevents.Stored

// Outbox stores events in the same transaction as the write.
//
// Store takes a Grant it does not otherwise need, and puts it in the row: who
// authorized it, which action, which tenant. That is a full audit trail without
// a second table.
type Outbox = hevents.Outbox

// ErrNoTransaction is returned when Store is called outside data.Transaction.
//
// It is an error rather than a fallback, and that is the whole guarantee: an
// event stored next to a row that then rolled back is worse than no event, and
// an event stored after the commit is one process crash away from being lost.
//
// The alias is what keeps it one value: a caller comparing against this name
// matches the error hesape returns.
var ErrNoTransaction = hevents.ErrNoTransaction

// NewOutbox returns an outbox over the application's database handle.
//
// It is hesape/events.NewOutbox, which takes the *data.DB as it is: the handle
// answers whether a context is inside a transaction on it with its own
// InTransaction method. An outbox write is still rebound for the dialect and
// still recorded on the Collector, next to the row it describes.
func NewOutbox(db *data.DB) *Outbox { return hevents.NewOutbox(db) }
