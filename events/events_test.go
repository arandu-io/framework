package events_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/events"
	"github.com/arandu-io/framework/security"
)

const tenant = "tenant-1"

// TestStoreRefusesOutsideATransaction is the guarantee this package exists for.
//
// An event written next to a row that then rolled back makes the rest of the
// system react to something that did not happen. An event written after the
// commit is one crash away from never leaving. Both are worse than an error at
// the call site.
func TestStoreRefusesOutsideATransaction(t *testing.T) {
	outbox := events.NewOutbox(data.Wrap(nil, data.DialectSQLite))

	err := outbox.Store(context.Background(), grant(tenant), []events.Event{
		{Name: "customer.created", Aggregate: "customer", AggregateID: "1"},
	})
	if !errors.Is(err, events.ErrNoTransaction) {
		t.Fatalf("err = %v, want ErrNoTransaction", err)
	}
}

// TestStoringNothingIsNotAnError: a write that produced no event is the common
// case, and forcing every caller to check the length first would put the same
// three lines in every service.
func TestStoringNothingIsNotAnError(t *testing.T) {
	outbox := events.NewOutbox(data.Wrap(nil, data.DialectSQLite))

	if err := outbox.Store(context.Background(), grant(tenant), nil); err != nil {
		t.Fatalf("storing no events: %v", err)
	}
}

// TestRecorderClearsWhatItHandsOver: an entity stored twice must not emit the
// same event twice.
func TestRecorderClearsWhatItHandsOver(t *testing.T) {
	var r events.Recorder
	r.Record(events.Event{Name: "invoice.paid"})
	r.Record(events.Event{Name: "invoice.closed"})

	first := r.PullEvents()
	if len(first) != 2 {
		t.Fatalf("pulled %d events, want 2", len(first))
	}
	if second := r.PullEvents(); len(second) != 0 {
		t.Fatalf("pulled %d events the second time, want 0", len(second))
	}
}

// TestTheRecorderKeepsOrder: events are a sequence of facts, and two events
// about the same aggregate only make sense in the order they happened.
func TestTheRecorderKeepsOrder(t *testing.T) {
	var r events.Recorder
	for _, name := range []string{"a", "b", "c"} {
		r.Record(events.Event{Name: name})
	}
	for i, e := range r.PullEvents() {
		if want := []string{"a", "b", "c"}[i]; e.Name != want {
			t.Errorf("event %d = %q, want %q", i, e.Name, want)
		}
	}
}

func TestDecodeReadsThePayloadBack(t *testing.T) {
	stored := events.Stored{Name: "invoice.paid", Payload: `{"amount":1250,"currency":"BRL"}`}

	var payload struct {
		Amount   int    `json:"amount"`
		Currency string `json:"currency"`
	}
	if err := stored.Decode(&payload); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if payload.Amount != 1250 || payload.Currency != "BRL" {
		t.Fatalf("payload = %+v", payload)
	}
}

// TestTheEventCarriesWhenItHappened: OccurredAt is the domain's clock, and a
// relay that reorders by insertion time would deliver a correction before the
// thing it corrects.
func TestTheEventCarriesWhenItHappened(t *testing.T) {
	when := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	e := events.Event{Name: "invoice.paid", OccurredAt: when}

	if !e.OccurredAt.Equal(when) {
		t.Fatal("OccurredAt was not kept")
	}
}

// TestTheOutboxMigrationIsPortable: the table has to exist on SQLite, Postgres
// and MySQL with one definition, because there is one migration path.
func TestTheOutboxMigrationIsPortable(t *testing.T) {
	migrations := events.NewModule().Migrations()
	if len(migrations) != 1 {
		t.Fatalf("%d migrations, want 1", len(migrations))
	}

	up := migrations[0].Up
	for _, engineSpecific := range []string{"jsonb", "uuid ", "timestamptz", "SERIAL", "AUTO_INCREMENT", "WHERE published_at IS NULL"} {
		if strings.Contains(up, engineSpecific) {
			t.Errorf("the migration uses %q, which is one engine's spelling", engineSpecific)
		}
	}
	if migrations[0].Down == "" {
		t.Error("the migration cannot be rolled back")
	}
}

func grant(tenant string) security.Grant {
	return security.SystemGrant("outbox.store", tenant)
}
