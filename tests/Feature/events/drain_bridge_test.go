package feature

import (
	"context"
	"slices"
	"testing"

	"github.com/arandu-io/framework/arandutest"
	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/events"
)

// arandutest.DrainOutbox is a forward, and a forward that dropped its call
// would still compile against every signature the bridge test pins: the test
// that drained would then assert on an empty collector and fail for a reason
// it never names. So the old name is driven once, over an outbox holding one
// event, and the event has to arrive and be marked delivered.
func TestTheOldDrainOutboxPublishesWhatTheOutboxHolds(t *testing.T) {
	db, table := newFakeOutbox()
	t.Cleanup(func() { _ = db.Close() })
	table.add("invoice.paid", 0)

	var collected arandutest.Collected
	arandutest.DrainOutbox(t, context.Background(), events.NewOutbox(data.Wrap(db, data.DialectSQLite)), &collected)

	if got := collected.Names(); !slices.Equal(got, []string{"invoice.paid"}) {
		t.Fatalf("DrainOutbox published %v, want [invoice.paid]", got)
	}
	if got := table.delivered(); len(got) != 1 {
		t.Fatalf("the event was published and marked delivered %d time(s), want once", len(got))
	}
}
