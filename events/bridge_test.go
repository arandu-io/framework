package events_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/events"
	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/framework/security"
	hevents "github.com/arandu-io/hesape/events"
)

// This file tests the bridge and nothing else: that the old name reaches the
// code in github.com/arandu-io/hesape/events, and that the three places where
// the design diverged translate. What the outbox and the relay do is tested
// there, against the code that now runs.

// TestTheNamesAreTheHesapeTypes is the whole point of an alias: a value produced
// against one name is the same type as a value produced against the other, so a
// repository generated before the move and one generated after take the same
// event.
//
// reflect.TypeFor rather than an assignment, because two distinct interface
// types with the same method set are assignable to each other and would pass a
// test written that way while being two types.
func TestTheNamesAreTheHesapeTypes(t *testing.T) {
	for _, c := range []struct {
		name     string
		bridged  reflect.Type
		upstream reflect.Type
	}{
		{"Event", reflect.TypeFor[events.Event](), reflect.TypeFor[hevents.Event]()},
		{"Recorder", reflect.TypeFor[events.Recorder](), reflect.TypeFor[hevents.Recorder]()},
		{"Stored", reflect.TypeFor[events.Stored](), reflect.TypeFor[hevents.Stored]()},
		{"Outbox", reflect.TypeFor[events.Outbox](), reflect.TypeFor[hevents.Outbox]()},
		{"Publisher", reflect.TypeFor[events.Publisher](), reflect.TypeFor[hevents.Publisher]()},
		{"PublisherFunc", reflect.TypeFor[events.PublisherFunc](), reflect.TypeFor[hevents.PublisherFunc]()},
	} {
		if c.bridged != c.upstream {
			t.Errorf("events.%s is %s, and hesape/events.%s is %s: the bridge declared a second type instead of aliasing",
				c.name, c.bridged, c.name, c.upstream)
		}
	}
}

// TestErrNoTransactionIsOneValue: a caller comparing against the old name has to
// match the error hesape returns, or the guarantee this package exists for
// becomes an unrecognized error at the call site.
func TestErrNoTransactionIsOneValue(t *testing.T) {
	if events.ErrNoTransaction != hevents.ErrNoTransaction {
		t.Fatalf("events.ErrNoTransaction is %v and hesape's is %v", events.ErrNoTransaction, hevents.ErrNoTransaction)
	}
}

// TestLockerIsTheKernelInterface: github.com/arandu-io/kv asserts
// `var _ events.Locker = (*Locker)(nil)` in a separate module, and the scheduler
// takes a kernel.Locker. One interface is what keeps the wiring one line.
func TestLockerIsTheKernelInterface(t *testing.T) {
	if reflect.TypeFor[events.Locker]() != reflect.TypeFor[kernel.Locker]() {
		t.Fatal("events.Locker stopped being kernel.Locker: kv wires the same value into both")
	}
	var _ events.Locker = (*fakeLocker)(nil)
}

// TestStoreRefusesOutsideATransaction proves the envelope around NewOutbox
// answers the question hesape asks. hesape/events.NewOutbox takes an interface
// whose fourth method reports whether the context is in a transaction, and
// *data.DB answers that through a package-level function instead.
//
// It is the guarantee this package exists for: an event written next to a row
// that then rolled back makes the rest of the system react to something that
// did not happen.
func TestStoreRefusesOutsideATransaction(t *testing.T) {
	outbox := events.NewOutbox(data.Wrap(nil, data.DialectSQLite))

	err := outbox.Store(context.Background(), grant(tenant), []events.Event{
		{Name: "customer.created", Aggregate: "customer", AggregateID: "1"},
	})
	if !errors.Is(err, events.ErrNoTransaction) {
		t.Fatalf("err = %v, want ErrNoTransaction", err)
	}
}

// TestStoreInsideATransactionIsAccepted is the other half of the same envelope,
// and the half that would break silently: an adapter that always answered "not
// in a transaction" would pass the test above and refuse every real write.
func TestStoreInsideATransactionIsAccepted(t *testing.T) {
	db, _ := newFakeOutbox()
	t.Cleanup(func() { _ = db.Close() })

	handle := data.Wrap(db, data.DialectSQLite)
	outbox := events.NewOutbox(handle)

	err := data.Transaction(context.Background(), handle, func(ctx context.Context) error {
		return outbox.Store(ctx, grant(tenant), []events.Event{
			{Name: "customer.created", Aggregate: "customer", AggregateID: "1"},
		})
	})
	if err != nil {
		t.Fatalf("storing inside a transaction: %v", err)
	}
}

// TestARelayWithALockerPublishesUnderTheLock covers the divergence that cannot
// be aliased: hesape/events.RelayOptions carries a *cache.Locks, nothing can
// build one from the Locker that kv implements, so a relay wired with one runs
// its ticker here and hands each pass to hesape under the lock.
func TestARelayWithALockerPublishesUnderTheLock(t *testing.T) {
	db, table := newFakeOutbox()
	t.Cleanup(func() { _ = db.Close() })

	publisher := newRecorder()
	locker := &fakeLocker{}
	relay := events.NewRelay(
		events.NewOutbox(data.Wrap(db, data.DialectSQLite)),
		publisher,
		events.RelayOptions{Interval: time.Millisecond, LockTTL: 2 * time.Second, Locker: locker},
	)
	table.add("invoice.paid", 0)

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- relay.Run(ctx) }()

	select {
	case <-publisher.got:
	case <-time.After(5 * time.Second):
		t.Fatal("the relay never published: the pass never ran under the lock")
	}
	cancel()

	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return when the context was cancelled")
	}

	name, ttl := locker.took()
	if name != "outbox-relay" {
		t.Errorf("the lock was taken as %q: kv keys on outbox-relay", name)
	}
	if ttl != 2*time.Second {
		t.Errorf("the lock ttl was %s, want the configured 2s", ttl)
	}
	if got := table.delivered(); len(got) == 0 {
		t.Fatal("the event was published and never marked published: the next start would deliver it again")
	}
}

// TestARelayHeldByAnotherReplicaKeepsGoing: "somebody else has it" is the lock
// working, not a failure. A relay that stopped on it would deliver nothing for
// as long as the other replica lived.
func TestARelayHeldByAnotherReplicaKeepsGoing(t *testing.T) {
	db, table := newFakeOutbox()
	t.Cleanup(func() { _ = db.Close() })

	publisher := newRecorder()
	locker := &fakeLocker{refuse: errors.New("kv: the lock is held by another process")}
	relay := events.NewRelay(
		events.NewOutbox(data.Wrap(db, data.DialectSQLite)),
		publisher,
		events.RelayOptions{Interval: time.Millisecond, Locker: locker},
	)
	table.add("invoice.paid", 0)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := relay.Run(ctx); err != nil {
		t.Fatalf("a held lock stopped the relay: %v", err)
	}

	if attempts := locker.attempts(); attempts == 0 {
		t.Fatal("the relay never tried to take the lock")
	}
	if got := publisher.received(); len(got) != 0 {
		t.Fatalf("published %v while another replica held the lock", got)
	}
}

// TestDrainReachesTheHesapeRelay: Drain is what a test uses, through
// arandutest.DrainOutbox, and it is the one relay method with no ticker in front
// of it.
func TestDrainReachesTheHesapeRelay(t *testing.T) {
	db, table := newFakeOutbox()
	t.Cleanup(func() { _ = db.Close() })

	publisher := newRecorder()
	relay := events.NewRelay(events.NewOutbox(data.Wrap(db, data.DialectSQLite)), publisher, events.RelayOptions{})
	table.add("invoice.paid", 0)

	if err := relay.Drain(context.Background()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if got := publisher.received(); len(got) != 1 || got[0] != "invoice.paid" {
		t.Fatalf("Drain published %v, want invoice.paid", got)
	}
	if lag, err := relay.Lag(context.Background()); err != nil || lag != 0 {
		t.Fatalf("Lag after draining = %s, %v; want 0 and no error", lag, err)
	}
}

// TestParkedReachesTheHesapeRelay: the dead letters feed the diagnosis on the
// error page, which is the only place anyone sees them.
func TestParkedReachesTheHesapeRelay(t *testing.T) {
	db, table := newFakeOutbox()
	t.Cleanup(func() { _ = db.Close() })

	relay := events.NewRelay(events.NewOutbox(data.Wrap(db, data.DialectSQLite)), newRecorder(), events.RelayOptions{})
	table.park("invoice.refunded", "the broker refused the message")

	parked, err := relay.Parked(context.Background(), 5)
	if err != nil {
		t.Fatalf("Parked: %v", err)
	}
	if len(parked) != 1 || parked[0].Name != "invoice.refunded" {
		t.Fatalf("Parked returned %+v", parked)
	}
}

func grant(tenant string) security.Grant {
	return security.SystemGrant("outbox.store", tenant)
}

// fakeLocker is a Locker of the shape github.com/arandu-io/kv implements: it
// runs the work under a lock it takes and gives back itself.
type fakeLocker struct {
	// refuse is what a lock somebody else holds answers with. The relay reads
	// that from the message, because the core does not import the adapter.
	refuse error

	mu    sync.Mutex
	calls int
	name  string
	ttl   time.Duration
}

func (l *fakeLocker) Run(ctx context.Context, name string, ttl time.Duration, fn func(context.Context) error) error {
	l.mu.Lock()
	l.calls++
	l.name = name
	l.ttl = ttl
	refuse := l.refuse
	l.mu.Unlock()

	if refuse != nil {
		return refuse
	}
	return fn(ctx)
}

func (l *fakeLocker) took() (string, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.name, l.ttl
}

func (l *fakeLocker) attempts() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}
