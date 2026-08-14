// The relay, answered by github.com/arandu-io/hesape/events.
//
// The publisher contract aliases and the delivery is hesape's: one pass reads
// the pending events, publishes them, marks them, parks the ones that gave up.
// What stays here is the lock, because that is where the design diverged --
// hesape/events.RelayOptions takes a *cache.Locks, and the lock this framework
// hands out is the Locker interface that github.com/arandu-io/kv implements.

package events

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/framework/observability"
	hevents "github.com/arandu-io/hesape/events"
)

// Publisher is where events go once they are committed.
//
// The framework does not pick one. NATS, a webhook, an in-process handler and a
// queue are all the same shape from here, and the choice belongs to the
// application -- what the framework guarantees is that whatever you plug in
// receives every event that was stored, at least once.
type Publisher = hevents.Publisher

// PublisherFunc adapts a function to Publisher.
type PublisherFunc = hevents.PublisherFunc

// Locker keeps N replicas from publishing the same event N times.
//
// It stays an alias for kernel.Locker, which is the one declaration of it in the
// framework: the scheduler needs the same thing, and two identical interfaces in
// two packages is a signature that can drift in one of them.
// github.com/arandu-io/kv implements it, and asserts as much against this name.
//
// It is the one thing here hesape has no counterpart for. There the lock is
// *cache.Locks, a concrete issuer over a store that can acquire and release by
// owner, and a Locker -- which only knows how to run a function under a lock it
// takes and gives back itself -- cannot be turned into one. So the name stays,
// and Relay.Run below is what drives it.
type Locker = kernel.Locker

// relayLock names the lock one pass of the relay holds. It is the name the kv
// adapter has been keying on since before this bridge existed, and hesape asks
// for the same one.
const relayLock = "outbox-relay"

// RelayOptions configures the relay.
//
// It stays declared here rather than aliasing hesape/events.RelayOptions, whose
// last field is a *cache.Locks. An alias would change the field every caller
// that wires a distributed lock is written against -- one line in bootstrap/app.go
// in every project -- and a bridge that changes a signature is not a bridge.
type RelayOptions struct {
	// Interval is how often the outbox is polled. Default 1s.
	//
	// Polling rather than LISTEN/NOTIFY, and that is a deliberate trade:
	// LISTEN/NOTIFY is lower latency and is Postgres-specific, which would put a
	// driver dependency in the core and give SQLite and MySQL a second code
	// path. One second of latency on a background publish is not the problem
	// this framework exists to solve.
	Interval time.Duration
	// Batch is how many events one pass publishes. Default 100.
	Batch int
	// MaxAttempts is how many failures an event gets before it is parked.
	// Default 10.
	MaxAttempts int
	// LockTTL bounds how long one pass may hold the lock. Default 30s.
	LockTTL time.Duration
	// Locker is the distributed lock. Nil means a single replica.
	Locker Locker
}

// The defaults for the two fields the locked loop reads for itself. Batch and
// MaxAttempts are not here because nothing on this side reads them: they travel
// to hesape, which defaults them, and restating those two numbers would be this
// package having an opinion about a value it does not use.
const (
	defaultInterval = time.Second
	defaultLockTTL  = 30 * time.Second
)

// Relay publishes what the outbox stored.
//
// Delivery is at-least-once, and that is not a limitation to fix -- it is the
// price of never losing an event. The consumer deduplicates on Stored.ID, which
// is why the id is stable and why it travels with the event.
//
// It is an envelope over hesape/events.Relay, which is the code that publishes.
// What this adds is the Locker: hesape takes a *cache.Locks and there is no way
// to build one from a Locker, so a relay wired with one runs its ticker here and
// gives each pass to hesape's Drain under the lock. A relay without a Locker --
// which is every relay in a single-replica deployment -- is hesape's loop
// unchanged.
type Relay struct {
	inner    *hevents.Relay
	locker   Locker
	interval time.Duration
	lockTTL  time.Duration
}

// NewRelay returns the relay.
func NewRelay(o *Outbox, p Publisher, opts RelayOptions) *Relay {
	interval := opts.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	lockTTL := opts.LockTTL
	if lockTTL <= 0 {
		lockTTL = defaultLockTTL
	}

	return &Relay{
		inner: hevents.NewRelay(o, p, hevents.RelayOptions{
			Interval:    interval,
			Batch:       opts.Batch,
			MaxAttempts: opts.MaxAttempts,
			LockTTL:     lockTTL,
		}),
		locker:   opts.Locker,
		interval: interval,
		lockTTL:  lockTTL,
	}
}

// Run polls until the context is cancelled.
//
// It is started by the module at boot and stopped at shutdown, in the same
// process as the application -- like the scheduler, and for the same reason: a
// second deployable to run background work is a second thing to monitor, page
// on, and forget to restart.
//
// Without a Locker it is hesape's loop. With one, the loop is here and each tick
// gives one pass to hesape under the lock, because the lock cannot travel there.
func (r *Relay) Run(ctx context.Context) error {
	if r.locker == nil {
		return r.inner.Run(ctx)
	}

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	log := observability.Log(ctx).With("component", "outbox-relay")

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		err := r.locker.Run(ctx, relayLock, r.lockTTL, r.inner.Drain)
		switch {
		case err == nil:
		case isLocked(err):
			// Another replica is publishing. That is the lock working, not a
			// failure, and logging it every second would bury everything else.
		case errors.Is(err, context.Canceled):
		default:
			// A failed pass is not fatal: the next tick tries again, and the
			// events are still in the table. Stopping the relay because the
			// database blinked would turn a hiccup into a backlog.
			log.Warn("outbox pass failed", "error", err)
		}
	}
}

// isLocked recognizes "somebody else holds it" without importing the kv package.
//
// By message rather than by type, which is ugly and is the price of the core not
// depending on the adapter. The alternative -- an exported sentinel in the core
// that kv would have to import -- inverts the dependency the wrong way. It is
// kept here because it goes with the Locker: hesape's own lock answers with a
// sentinel and needs none of this.
func isLocked(err error) bool {
	return err != nil && strings.Contains(err.Error(), "lock is held")
}

// Drain publishes everything pending, once, and returns.
//
// This is what a test uses. There is no synchronous mode -- the test runs the
// same code path as production, with the relay executed inline instead of on a
// ticker. "Sync only in tests" is a second way to do one thing, and the second
// way always leaks into production.
func (r *Relay) Drain(ctx context.Context) error { return r.inner.Drain(ctx) }

// Parked returns the events that gave up, for the diagnosis and for whoever is
// deciding whether to retry them.
func (r *Relay) Parked(ctx context.Context, limit int) ([]Stored, error) {
	return r.inner.Parked(ctx, limit)
}

// Lag is how long the oldest unpublished event has been waiting.
//
// This is the number that matters: a relay that stopped looks exactly like a
// relay with nothing to do, and only the age of the oldest pending event tells
// them apart. It feeds the health check and the hint on the error page.
func (r *Relay) Lag(ctx context.Context) (time.Duration, error) { return r.inner.Lag(ctx) }
