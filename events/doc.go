// Package events is domain events with an outbox.
//
// There is no Publish(). The naive flow loses data in both directions: if the
// process dies between the write and the publish, the event never leaves; if the
// publish happens and the transaction rolls back, the rest of the system reacts
// to something that did not happen.
//
// So an event is stored in the same transaction as the write that produced it,
// and a relay publishes it afterwards. One way to do it, and the one that
// cannot lose an event.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/events directly.
//
// The outbox, the relay and the event types moved to
// github.com/arandu-io/hesape/events, which also holds the dispatcher this
// package never had. Nothing here holds an implementation of them: where the
// name and the signature survived the move it is a Go alias, and where the
// design diverged it is an envelope that translates and nothing more. The death
// date above is what keeps this from being a second way to import one type.
//
// The three envelopes, and what diverged:
//
//	NewOutbox     hesape/events.NewOutbox takes an interface that can be asked
//	              whether the context is in a transaction, and *data.DB answers
//	              that through a package-level function instead
//	RelayOptions  the Locker field became a *cache.Locks, which nothing outside
//	              hesape can build from the Locker that github.com/arandu-io/kv
//	              implements
//	Relay         Run drives the locked pass through that Locker, because the
//	              options it would otherwise be handed cannot carry one
//
// Module stays framework code rather than an envelope. It answers the module
// contract the kernel collects -- Routes and Migrations included -- so the
// outbox table travels with the module that owns it.
package events
