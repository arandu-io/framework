package data_test

import (
	"context"
	"errors"
	"testing"

	"github.com/arandu-io/framework/data"
)

var migrations = []data.Migration{
	{ID: "0001_create_users", Up: `CREATE TABLE users (id uuid PRIMARY KEY)`, Down: `DROP TABLE users`},
	{ID: "0002_add_email", Up: `ALTER TABLE users ADD COLUMN email text`, Down: `ALTER TABLE users DROP COLUMN email`},
}

func TestMigrateAppliesPendingInOrder(t *testing.T) {
	sqldb, state := newFakeDB()
	defer sqldb.Close()
	db := data.Wrap(sqldb)

	applied, err := data.Migrate(context.Background(), db, migrations)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if len(applied) != 2 || applied[0] != "0001_create_users" || applied[1] != "0002_add_email" {
		t.Fatalf("applied = %v, want both ids in order", applied)
	}
	if !state.sawStatement("CREATE TABLE IF NOT EXISTS " + data.MigrationsTable) {
		t.Fatal("the tracking table must be created before anything else")
	}
	if !state.sawStatement("INSERT INTO " + data.MigrationsTable) {
		t.Fatal("an applied migration must be recorded")
	}
	if !state.sawStatement("COMMIT") {
		t.Fatal("each migration must be committed with its own record")
	}
}

// TestMigrateSkipsWhatIsAlreadyApplied is what makes `aru migrate` safe to run
// twice, which is the only way a deploy pipeline can use it.
func TestMigrateSkipsWhatIsAlreadyApplied(t *testing.T) {
	sqldb, state := newFakeDB()
	defer sqldb.Close()
	state.rows["SELECT id FROM "+data.MigrationsTable] = []string{"0001_create_users"}
	db := data.Wrap(sqldb)

	applied, err := data.Migrate(context.Background(), db, migrations)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if len(applied) != 1 || applied[0] != "0002_add_email" {
		t.Fatalf("applied = %v, want only the pending one", applied)
	}
	if state.sawStatement("CREATE TABLE users") {
		t.Fatal("an already applied migration was run again")
	}
}

// TestMigrateStopsAtTheFirstFailure: continuing over a broken schema turns one
// clear error into an unrecoverable database.
func TestMigrateStopsAtTheFirstFailure(t *testing.T) {
	sqldb, state := newFakeDB()
	defer sqldb.Close()
	state.failOn = "CREATE TABLE users"
	state.failErr = errors.New("syntax error")
	db := data.Wrap(sqldb)

	applied, err := data.Migrate(context.Background(), db, migrations)
	if err == nil {
		t.Fatal("Migrate succeeded over a failing statement")
	}
	if len(applied) != 0 {
		t.Fatalf("applied = %v, want none", applied)
	}
	if state.sawStatement("ALTER TABLE users") {
		t.Fatal("the second migration ran after the first one failed")
	}
}

func TestMigrateRejectsEmptyID(t *testing.T) {
	sqldb, _ := newFakeDB()
	defer sqldb.Close()
	db := data.Wrap(sqldb)

	_, err := data.Migrate(context.Background(), db, []data.Migration{{Up: `SELECT 1`}})
	if err == nil {
		t.Fatal("a migration without an id was accepted: order would be undefined")
	}
}

func TestPendingListsWhatMigrateWouldApply(t *testing.T) {
	sqldb, state := newFakeDB()
	defer sqldb.Close()
	state.rows["SELECT id FROM "+data.MigrationsTable] = []string{"0001_create_users"}
	db := data.Wrap(sqldb)

	pending, err := data.Pending(context.Background(), db, migrations)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}

	if len(pending) != 1 || pending[0].ID != "0002_add_email" {
		t.Fatalf("pending = %+v, want only 0002_add_email", pending)
	}
}
