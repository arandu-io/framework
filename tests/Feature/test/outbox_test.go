package feature

import (
	"context"
	"testing"

	"github.com/arandu-io/framework/test"
	"github.com/arandu-io/hesape/events"
)

// The order events arrived in is the assertion most tests are actually making,
// and a collector that answered in map order would be right about the set and
// wrong about the story: created then paid is a different fact from paid then
// created.
func TestCollectedKeepsWhatArrivedInTheOrderItArrived(t *testing.T) {
	var collected test.Collected

	for _, name := range []string{"invoice.created", "invoice.paid"} {
		if err := collected.Publish(context.Background(), events.Stored{ID: name, Name: name}); err != nil {
			t.Fatalf("collecting %s: %v", name, err)
		}
	}

	names := collected.Names()
	if len(names) != 2 || names[0] != "invoice.created" || names[1] != "invoice.paid" {
		t.Errorf("collected %v, want [invoice.created invoice.paid]", names)
	}
	if len(collected.Events) != 2 {
		t.Errorf("kept %d event(s), want 2", len(collected.Events))
	}
}
