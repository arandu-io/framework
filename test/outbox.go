package test

import (
	"context"
	"testing"

	"github.com/arandu-io/hesape/events"
)

// DrainOutbox publishes everything the outbox is holding, once.
//
// It is the relay, executed inline instead of on a ticker:
//
//	test.DrainOutbox(t, ctx, outbox, publisher)
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
