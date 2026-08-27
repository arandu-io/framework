// The two outbox helpers, answered by
// github.com/arandu-io/hesape/arandutest.
//
// They used to hold an implementation here, and the reason was a type: the
// hesape outbox is built over a DB interface that reports its own transaction,
// this module's was built over *data.DB, so a call through had nothing to pass.
// framework/events.Outbox, Publisher and Stored are Go aliases now, which means
// the two signatures name the same three types and there is nothing left to
// translate.

package arandutest

import (
	"context"
	"testing"

	"github.com/arandu-io/framework/events"
	"github.com/arandu-io/hesape/arandutest"
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
//
// The parameters keep the names this module spells them with, so the call sites
// are untouched.
func DrainOutbox(t *testing.T, ctx context.Context, outbox *events.Outbox, publisher events.Publisher) {
	t.Helper()
	arandutest.DrainOutbox(t, ctx, outbox, publisher)
}

// Collected is a Publisher that keeps what it received, for a test to assert on.
//
// Its Events field, its Publish and its Names are hesape's, reached through this
// name. Declaring the struct again here would compile and pass every assertion
// written against it, and it would be a second recorder to keep in step with the
// first.
type Collected = arandutest.Collected
