package events_test

import (
	"context"
	"strings"
	"testing"

	"github.com/arandu-io/framework/events"
)

// tenant is who the events in these tests belong to. Every outbox row carries
// one, because a relay reading a row without one would not know who to deliver
// it to (RULE 14).
const tenant = "tenant-1"

// TestTheOutboxMigrationIsPortable: the table has to exist on SQLite, Postgres
// and MySQL with one definition, because there is one migration path.
//
// The migrations stay in this package rather than moving with the outbox: a
// Migration is a value the migrator consumes, and the migrator has not moved.
func TestTheOutboxMigrationIsPortable(t *testing.T) {
	migrations := events.NewModule().Migrations()
	if len(migrations) == 0 {
		t.Fatal("no migrations")
	}

	for _, m := range migrations {
		for _, engineSpecific := range []string{"jsonb", "uuid ", "timestamptz", "SERIAL", "AUTO_INCREMENT", "WHERE published_at IS NULL"} {
			if strings.Contains(m.Up, engineSpecific) {
				t.Errorf("%s uses %q, which is one engine's spelling", m.ID, engineSpecific)
			}
		}
		if m.Down == "" {
			t.Errorf("%s cannot be rolled back", m.ID)
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
