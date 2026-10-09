package feature

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/arandu-io/framework/events"
	"github.com/arandu-io/hesape/database/migrations"
	hevents "github.com/arandu-io/hesape/events"
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

// TestBothEventsModulesDeclareTheOutboxOnce: the framework's module hands over
// the migrations hesape's module declares rather than declaring its own, so the
// two lists are the same values and the outbox has one definition. A project
// that registers both modules' migrations gets each name once, where two
// definitions under one name are refused by the registry as a copied file.
//
// The names are the ones a database that already migrated has recorded, and
// they cannot change without the table being created again.
func TestBothEventsModulesDeclareTheOutboxOnce(t *testing.T) {
	framework := events.NewModule().Migrations()
	native := hevents.NewModule().Migrations()
	if len(framework) != len(native) {
		t.Fatalf("the framework declares %d migrations and hesape %d", len(framework), len(native))
	}
	for i := range native {
		if framework[i] != native[i] {
			t.Errorf("migration %d: the framework declares %s %q and hesape %s %q, want the same value",
				i, qualified(framework[i]), framework[i].GetName(), qualified(native[i]), native[i].GetName())
		}
	}

	const group = "tests/feature/events/both-modules"
	func() {
		defer func() {
			if v := recover(); v != nil {
				t.Fatalf("registering both modules' migrations was refused: %v", v)
			}
		}()
		for _, m := range slices.Concat(framework, native) {
			migrations.Register(m, group)
		}
	}()

	var names []string
	for _, m := range migrations.Registered(group) {
		names = append(names, m.GetName())
	}
	want := []string{"2026_07_31_000001_create_outbox_table", "2026_07_31_000002_add_outbox_dead_letter"}
	if !slices.Equal(names, want) {
		t.Errorf("registered %q, want each of %q once", names, want)
	}
}

// qualified names a value's type with its whole package path, so two types
// that share a name in two packages read as two.
func qualified(v any) string {
	t := reflect.TypeOf(v)
	return t.PkgPath() + "." + t.Name()
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
