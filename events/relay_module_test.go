package events_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/events"
)

// moduleWithRelay wires the module the way an application does: outbox over a
// handle, a publisher, and the relay running in this process.
func moduleWithRelay(t *testing.T, opts events.RelayOptions, publisher events.Publisher) (*events.Module, *fakeOutbox) {
	t.Helper()

	db, table := newFakeOutbox()
	t.Cleanup(func() { _ = db.Close() })

	outbox := events.NewOutbox(data.Wrap(db, data.DialectSQLite))
	return events.WithRelay(events.NewRelay(outbox, publisher, opts)), table
}

// recorder is a Publisher that signals the test when an event arrives.
type recorder struct {
	mu    sync.Mutex
	names []string
	got   chan struct{}
}

func newRecorder() *recorder { return &recorder{got: make(chan struct{}, 16)} }

func (r *recorder) Publish(_ context.Context, e events.Stored) error {
	r.mu.Lock()
	r.names = append(r.names, e.Name)
	r.mu.Unlock()
	select {
	case r.got <- struct{}{}:
	default:
	}
	return nil
}

func (r *recorder) received() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.names...)
}

// TestWithRelayPublishesAndStops covers the branch every other test skipped.
//
// events.WithRelay had no call site anywhere -- not in the framework, not in the
// skeleton, not in the examples -- so `relay != nil` never held and the four
// methods that check it (Start, Close, Health, Diagnose) each ran only their
// early return. The loop that delivers events had never been started by a test.
func TestWithRelayPublishesAndStops(t *testing.T) {
	publisher := newRecorder()
	module, table := moduleWithRelay(t, events.RelayOptions{Interval: time.Millisecond}, publisher)
	table.add("invoice.paid", 0)

	if err := module.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-publisher.got:
	case <-time.After(5 * time.Second):
		t.Fatal("the relay never published: Start did not run the loop")
	}

	// Close waits for the pass in flight. A pass interrupted between publishing
	// and marking published delivers the event again on the next start, and
	// that is the duplicate this framework can avoid.
	if err := module.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := publisher.received(); len(got) == 0 || got[0] != "invoice.paid" {
		t.Fatalf("published %v, want invoice.paid first", got)
	}
	if got := table.delivered(); len(got) == 0 {
		t.Fatal("the event was published and never marked published: the next start would deliver it again")
	}

	// Closing twice is what a shutdown path that runs on two signals does.
	if err := module.Close(context.Background()); err != nil {
		t.Fatalf("the second Close: %v", err)
	}
}

// TestCloseWithoutStartIsNotAnError: `aru routes` builds the same application a
// server does and never calls Start, so the shutdown path has to survive a relay
// that was wired and never run.
func TestCloseWithoutStartIsNotAnError(t *testing.T) {
	module, _ := moduleWithRelay(t, events.RelayOptions{}, newRecorder())

	if err := module.Close(context.Background()); err != nil {
		t.Fatalf("Close before Start: %v", err)
	}
}

// TestHealthFailsWhenTheOutboxFallsBehind: a relay that stopped looks exactly
// like a relay with nothing to do, and the age of the oldest pending event is
// the only thing that tells them apart. Without it the first sign is a customer
// asking why they never got the email.
func TestHealthFailsWhenTheOutboxFallsBehind(t *testing.T) {
	t.Run("an empty outbox is healthy", func(t *testing.T) {
		module, _ := moduleWithRelay(t, events.RelayOptions{}, newRecorder())
		if err := module.Health(context.Background()); err != nil {
			t.Fatalf("Health: %v", err)
		}
	})

	t.Run("a fresh backlog is healthy", func(t *testing.T) {
		module, table := moduleWithRelay(t, events.RelayOptions{}, newRecorder())
		table.add("invoice.paid", time.Second)
		if err := module.Health(context.Background()); err != nil {
			t.Fatalf("Health: %v", err)
		}
	})

	t.Run("an old backlog fails", func(t *testing.T) {
		module, table := moduleWithRelay(t, events.RelayOptions{}, newRecorder())
		table.add("invoice.paid", 5*time.Minute)

		err := module.Health(context.Background())
		if err == nil {
			t.Fatal("an event waiting five minutes was reported healthy")
		}
		if !strings.Contains(err.Error(), "is the relay running") {
			t.Errorf("the message must name the likely cause, got: %v", err)
		}
	})

	t.Run("a database that is not answering fails", func(t *testing.T) {
		module, table := moduleWithRelay(t, events.RelayOptions{}, newRecorder())
		table.breakTheLagQuery()

		if err := module.Health(context.Background()); err == nil {
			t.Fatal("a health check that cannot read the outbox reported healthy")
		}
	})
}

// TestDiagnoseNamesTheBacklogAndTheDeadLetters is the hint doc 27 asks for: it
// shows up on the error page, next to the failure somebody is already looking
// at, which is the moment they are most likely to act on it.
func TestDiagnoseNamesTheBacklogAndTheDeadLetters(t *testing.T) {
	module, table := moduleWithRelay(t, events.RelayOptions{}, newRecorder())
	table.add("invoice.paid", 4*time.Minute)
	table.park("invoice.refunded", "the broker refused the message")

	hints := module.Diagnose(context.Background())

	if len(hints) != 2 {
		t.Fatalf("diagnosed %d things, want the backlog and the dead letters: %v", len(hints), hints)
	}
	joined := strings.Join(hints, "\n")
	for _, want := range []string{"waiting 4m", "invoice.refunded", "the broker refused the message"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the diagnosis does not mention %q: %s", want, joined)
		}
	}
}

// TestDiagnoseIsSilentWhenTheRelayIsKeepingUp: a diagnosis that always says
// something is a diagnosis nobody reads, and the error page has limited room
// before people stop looking at it.
func TestDiagnoseIsSilentWhenTheRelayIsKeepingUp(t *testing.T) {
	module, table := moduleWithRelay(t, events.RelayOptions{}, newRecorder())
	table.add("invoice.paid", time.Second)

	if got := module.Diagnose(context.Background()); len(got) != 0 {
		t.Fatalf("a relay one second behind diagnosed %v", got)
	}
}
