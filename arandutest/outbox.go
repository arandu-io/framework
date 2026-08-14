// The two outbox helpers, whose counterparts are in
// github.com/arandu-io/hesape/arandutest and which cannot be reached from here
// yet.
//
// hesape/arandutest.DrainOutbox takes a *hesape/events.Outbox, and
// framework/events.Outbox is a different type: the hesape outbox is built over
// a DB interface that reports its own transaction, the framework one over
// *data.DB. There is nothing to hand across, so DrainOutbox is declared here,
// composed out of the framework's own events package -- which is itself a
// bridge. Collected is declared for the same reason one step removed: it has to
// satisfy framework/events.Publisher, and that interface names
// framework/events.Stored. It becomes a one-line alias for
// hesape/arandutest.Collected the day framework/events.Stored is an alias for
// hesape/events.Stored.
//
// Neither is a second implementation of anything in hesape: the relay they
// drive is the one that runs in production, executed inline instead of on a
// ticker.

package arandutest

import (
	"context"
	"testing"

	"github.com/arandu-io/framework/events"
)

// DrainOutbox publishes everything the outbox is holding, once.
//
// It is the relay, executed inline instead of on a ticker:
//
//	arandutest.DrainOutbox(t, ctx, outbox, publisher)
//
// A test that asserts on an event has to wait for it somehow, and the two
// alternatives are worse. Sleeping is flaky and slow. A synchronous publish
// path is a second implementation that will drift from the real one.
func DrainOutbox(t *testing.T, ctx context.Context, outbox *events.Outbox, publisher events.Publisher) {
	t.Helper()

	relay := events.NewRelay(outbox, publisher, events.RelayOptions{})
	if err := relay.Drain(ctx); err != nil {
		t.Fatalf("draining the outbox: %v", err)
	}
}

// Collected is a Publisher that keeps what it received, for a test to assert on.
type Collected struct {
	Events []events.Stored
}

// Publish stores the event.
func (c *Collected) Publish(_ context.Context, e events.Stored) error {
	c.Events = append(c.Events, e)
	return nil
}

// Names returns the event names in the order they arrived, which is what most
// assertions are actually about.
func (c *Collected) Names() []string {
	out := make([]string, 0, len(c.Events))
	for _, e := range c.Events {
		out = append(out, e.Name)
	}
	return out
}
