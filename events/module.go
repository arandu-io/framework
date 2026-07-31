package events

import (
	"context"
	"fmt"
	"time"

	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/kernel"
)

// Module brings the outbox table, and runs the relay when one is wired.
//
// It registers no routes: it exists so the table travels with the framework
// rather than being copied into every project's migrations. Register it in
// cmd/app/main.go next to the modules that store events.
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
	_ kernel.Bootable   = (*Module)(nil)
	_ kernel.Closable   = (*Module)(nil)
	_ kernel.Health     = (*Module)(nil)
	_ kernel.Diagnostic = (*Module)(nil)
)

// Name is the module identifier.
func (*Module) Name() string { return "events" }

// Routes registers nothing. The relay and the event console are phase 3.
func (*Module) Routes(*httpx.Router) {}

// Migrations returns the outbox table.
func (*Module) Migrations() []kernel.Migration {
	return []kernel.Migration{
		{
			ID: "2026_07_31_000001_create_outbox_table",
			// Portable types only: TEXT, INTEGER and TIMESTAMP mean the same
			// thing on SQLite, Postgres and MySQL. jsonb would be one engine's
			// spelling, and the payload is written and read as JSON text either
			// way.
			Up: `
CREATE TABLE outbox (
    id            TEXT PRIMARY KEY,
    tenant_id     TEXT NOT NULL,
    event         TEXT NOT NULL,
    aggregate     TEXT NOT NULL,
    aggregate_id  TEXT NOT NULL,
    payload       TEXT NOT NULL,
    authorized_by TEXT NOT NULL,
    action        TEXT NOT NULL,
    occurred_at   TIMESTAMP NOT NULL,
    published_at  TIMESTAMP,
    attempts      INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT
);

-- The relay reads unpublished events oldest first. A partial index would be
-- tighter, and MySQL does not have one; the two leading columns give the same
-- scan on every engine.
CREATE INDEX idx_outbox_pending ON outbox (published_at, occurred_at);

-- Deduplication is the consumer's job, and the id is the key it deduplicates
-- on. Delivery is at-least-once: the same event can arrive twice, and that is
-- the price of never losing one.
CREATE INDEX idx_outbox_tenant ON outbox (tenant_id, occurred_at);
`,
			Down: `DROP TABLE outbox;`,
		},
		{
			// A separate migration rather than an edit to the one above,
			// because the first one has already run somewhere. RULE 16: the
			// column is nullable, so the previous binary keeps working during a
			// rollout -- it simply never writes it.
			ID: "2026_07_31_000002_add_outbox_dead_letter",
			Up: `
ALTER TABLE outbox ADD COLUMN failed_at TIMESTAMP;

-- The relay reads pending events on every tick, and "pending" now means
-- neither published nor parked.
CREATE INDEX idx_outbox_unfinished ON outbox (failed_at, published_at, occurred_at);
`,
			Down: `
DROP INDEX idx_outbox_unfinished;
ALTER TABLE outbox DROP COLUMN failed_at;
`,
		},
	}
}

// Boot starts the relay loop.
func (m *Module) Boot(ctx context.Context) error {
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
