// The module, which is the one file here that is not a bridge.
//
// github.com/arandu-io/hesape/events has a Module of its own and this is not an
// alias of it, for two reasons. Routes takes the framework's *http.Router, which
// is the contract the kernel collects. And Start hands the loop to Relay.Run,
// which is where the Locker this framework still hands out is driven; hesape's
// Module would take a relay that cannot carry one. The outbox migrations are
// not one of the reasons: they are hesape's, and Migrations hands them over.

package events

import (
	"context"
	"fmt"
	"time"

	"github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/kernel"
	hevents "github.com/arandu-io/hesape/events"
)

// Module brings the outbox table, and runs the relay when one is wired.
//
// It registers no routes: it exists so the table travels with the framework
// rather than being copied into every project's migrations. Register it in
// bootstrap/app.go next to the modules that store events.
//
// # Why this one is declared and not aliased
//
// hesape/events.Module registers its routes on a *routing.Router, and the
// contract the kernel collects hands a module the framework's *http.Router, so
// the hesape module is not a module this kernel can register. Start is the
// second reason: it hands the loop to Relay.Run, where the Locker this
// framework still issues is driven, and the hesape module drives a relay that
// has no field for one.
//
// The table is not a reason. hesape/events.Module declares the outbox
// migrations, and Migrations here returns those same values, so there is one
// definition of the table and one name for each migration whichever module
// brought it.
type Module struct {
	relay *Relay
	// stop cancels the relay loop at shutdown.
	stop context.CancelFunc
	done chan struct{}
}

// NewModule returns the module with no relay: the table exists, events are
// stored, and nothing publishes them yet.
//
// That is a useful state rather than a broken one. Storing is what cannot be
// recovered later; publishing can start on the day there is something to
// publish to.
func NewModule() *Module { return &Module{} }

// WithRelay returns the module running the relay in this process.
//
// In-process, like the scheduler and for the same reason: a second deployable
// for background work is a second thing to monitor, page on, and forget to
// restart. With more than one replica, give the relay a Locker -- otherwise
// each one publishes every event.
func WithRelay(r *Relay) *Module { return &Module{relay: r} }

var (
	_ kernel.Module     = (*Module)(nil)
	_ kernel.Migratable = (*Module)(nil)
	_ kernel.Background = (*Module)(nil)
	_ kernel.Closable   = (*Module)(nil)
	_ kernel.Health     = (*Module)(nil)
	_ kernel.Diagnostic = (*Module)(nil)
)

// Name is the module identifier.
func (*Module) Name() string { return "events" }

// Routes registers nothing: this module has no HTTP surface.
func (*Module) Routes(*http.Router) {}

// Migrations returns the outbox table: hesape/events.Module's two migrations,
// the one that creates it and the one that adds the column a parked event is
// marked with.
//
// They are handed over rather than declared again. A second declaration under
// the same names is a second definition of one table, and the migration
// registry refuses two different migrations under one name, so a project that
// collected both modules' lists would stop at the second. These are the same
// values, so the registry keeps one of each.
func (*Module) Migrations() []kernel.Migration {
	return hevents.NewModule().Migrations()
}

// Start begins the relay loop, and only the process that serves calls it.
//
// It used to be Boot, which every command calls: each `aru work` replica ran a
// relay of its own, and so did `aru routes`. The lock made the duplicate
// harmless rather than correct. See kernel.Background.
func (m *Module) Start(ctx context.Context) error {
	if m.relay == nil {
		return nil
	}

	// The loop outlives the boot context, which is cancelled once boot returns.
	// It is stopped by Close, which the kernel calls on shutdown.
	loop, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.stop = cancel
	m.done = make(chan struct{})

	go func() {
		defer close(m.done)
		_ = m.relay.Run(loop)
	}()
	return nil
}

// Close stops the relay and waits for the pass in flight.
//
// Waiting matters: a pass interrupted between publishing and marking published
// delivers the event again on the next start, and that is the duplicate this
// framework can avoid rather than the one it cannot.
func (m *Module) Close(ctx context.Context) error {
	if m.stop == nil {
		return nil
	}
	m.stop()

	select {
	case <-m.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// maxLag is how far behind the relay may fall before the health check fails.
//
// A minute is generous for a loop that ticks every second. Past it, something
// is wrong -- the relay is not running, the publisher is refusing everything,
// or another replica holds the lock and died.
const maxLag = time.Minute

// hintLag is when a backlog stops being normal and starts being worth
// mentioning on an error page. It is well below the threshold that fails the
// health check: by the time the health check trips, somebody is already paged.
const hintLag = 30 * time.Second

// Diagnose says what is wrong with event delivery, in a sentence.
//
// This is the hint doc 27 asks for: "invoice.paid has been waiting four minutes
// -- is the relay running?". It shows up on the error page, next to the failure
// somebody is already looking at, which is the moment they are most likely to
// act on it.
func (m *Module) Diagnose(ctx context.Context) []string {
	if m.relay == nil {
		return nil
	}
	var out []string

	if lag, err := m.relay.Lag(ctx); err == nil && lag > hintLag {
		out = append(out, fmt.Sprintf(
			"The oldest unpublished event has been waiting %s. Is the relay running, and is the publisher accepting?",
			lag.Truncate(time.Second)))
	}

	if parked, err := m.relay.Parked(ctx, 5); err == nil && len(parked) > 0 {
		out = append(out, fmt.Sprintf(
			"%d event(s) gave up after repeated failures, the most recent being %s: %s. They stay in the outbox until retried.",
			len(parked), parked[0].Name, parked[0].LastError))
	}
	return out
}

// Health fails when the outbox is falling behind.
//
// A relay that stopped looks exactly like a relay with nothing to do, and the
// age of the oldest pending event is what tells them apart. Without this, the
// first sign is a customer asking why they never got the email.
func (m *Module) Health(ctx context.Context) error {
	if m.relay == nil {
		return nil
	}

	lag, err := m.relay.Lag(ctx)
	if err != nil {
		return err
	}
	if lag > maxLag {
		return fmt.Errorf("the oldest unpublished event has been waiting %s -- is the relay running?", lag.Truncate(time.Second))
	}
	return nil
}
