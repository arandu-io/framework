package feature

import (
	"context"
	"strings"
	"testing"

	"github.com/arandu-io/framework/events"
	"github.com/arandu-io/hesape/database/migrations"
)

// tenant is who the events in these tests belong to. Every outbox row carries
// one, because a relay reading a row without one would not know who to deliver
// it to.
const tenant = "tenant-1"

// TestTheOutboxMigrationIsPortable: the table has to exist on SQLite, Postgres
// and MySQL with one definition, because there is one migration path.
//
// The statements are read with UpStatements, which runs the migration against a
// connection that records instead of sending. That is the same code path the
// migrator takes, so a statement this test never sees is one the database never
// gets either.
func TestTheOutboxMigrationIsPortable(t *testing.T) {
	declared := events.NewModule().Migrations()
	if len(declared) == 0 {
		t.Fatal("no migrations")
	}

	for _, m := range declared {
		up, err := migrations.UpStatements(context.Background(), m)
		if err != nil {
			t.Fatalf("%s: %v", m.GetName(), err)
		}
		for _, statement := range up {
			for _, engineSpecific := range []string{"jsonb", "uuid ", "timestamptz", "SERIAL", "AUTO_INCREMENT", "WHERE published_at IS NULL"} {
				if strings.Contains(statement, engineSpecific) {
					t.Errorf("%s uses %q, which is one engine's spelling", m.GetName(), engineSpecific)
				}
			}
		}

		down, err := migrations.DownStatements(context.Background(), m)
		if err != nil {
			t.Fatalf("%s: %v", m.GetName(), err)
		}
		if len(down) == 0 {
			t.Errorf("%s cannot be rolled back", m.GetName())
		}
	}
}

// TestTheDiagnosisIsSilentWhenNothingIsWrong: a diagnosis that always says
// something is a diagnosis nobody reads, and the error page has limited room
// before people stop looking at it.
func TestTheDiagnosisIsSilentWhenNothingIsWrong(t *testing.T) {
	if got := events.NewModule().Diagnose(context.Background()); len(got) != 0 {
		t.Fatalf("a module with no relay diagnosed %v", got)
	}
}

// TestAModuleWithNoRelayIsHealthy: storing without publishing is a real state,
// not a broken one. Storing is what cannot be recovered later; publishing can
// start the day there is something to publish to.
func TestAModuleWithNoRelayIsHealthy(t *testing.T) {
	if err := events.NewModule().Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if err := events.NewModule().Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := events.NewModule().Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
