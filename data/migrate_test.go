package data_test

import (
	"context"
	"errors"
	"strings"
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
	db := data.Wrap(sqldb, data.DialectPostgres)

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
	state.rows["FROM "+data.MigrationsTable] = []string{"0001_create_users"}
	db := data.Wrap(sqldb, data.DialectPostgres)

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
	db := data.Wrap(sqldb, data.DialectPostgres)

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
	db := data.Wrap(sqldb, data.DialectPostgres)

	_, err := data.Migrate(context.Background(), db, []data.Migration{{Up: `SELECT 1`}})
	if err == nil {
		t.Fatal("a migration without an id was accepted: order would be undefined")
	}
}

func TestPendingListsWhatMigrateWouldApply(t *testing.T) {
	sqldb, state := newFakeDB()
	defer sqldb.Close()
	state.rows["FROM "+data.MigrationsTable] = []string{"0001_create_users"}
	db := data.Wrap(sqldb, data.DialectPostgres)

	pending, err := data.Pending(context.Background(), db, migrations)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}

	if len(pending) != 1 || pending[0].ID != "0002_add_email" {
		t.Fatalf("pending = %+v, want only 0002_add_email", pending)
	}
}

// TestMigrateGroupsACallIntoOneBatch is what makes rollback undo a deploy rather
// than a single migration -- the same model Laravel uses.
func TestMigrateGroupsACallIntoOneBatch(t *testing.T) {
	sqldb, state := newFakeDB()
	defer sqldb.Close()
	db := data.Wrap(sqldb, data.DialectPostgres)

	if _, err := data.Migrate(context.Background(), db, migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var batches []any
	for i, stmt := range state.statements() {
		if strings.Contains(stmt, "INSERT INTO "+data.MigrationsTable) {
			batches = append(batches, state.args[i][1].Value)
		}
	}
	if len(batches) != 2 {
		t.Fatalf("recorded %d migrations, want 2", len(batches))
	}
	if batches[0] != batches[1] {
		t.Fatalf("batches = %v, want both migrations in the same batch", batches)
	}
}

// TestRollbackRefusesAMigrationWithoutDown: skipping it would leave half a batch
// in place, producing a schema that matches neither version.
func TestRollbackRefusesAMigrationWithoutDown(t *testing.T) {
	sqldb, state := newFakeDB()
	defer sqldb.Close()
	state.rows["FROM "+data.MigrationsTable] = []string{"0001_create_users"}
	db := data.Wrap(sqldb, data.DialectPostgres)

	_, err := data.Rollback(context.Background(), db, []data.Migration{
		{ID: "0001_create_users", Up: "CREATE TABLE users (id TEXT)"},
	})

	if err == nil {
		t.Fatal("a migration without a Down was rolled back")
	}
	if !strings.Contains(err.Error(), "no Down") {
		t.Errorf("error = %v", err)
	}
}

// TestRollbackRefusesAnUnknownMigration covers the module that was removed from
// the wiring while its migration is still recorded.
func TestRollbackRefusesAnUnknownMigration(t *testing.T) {
	sqldb, state := newFakeDB()
	defer sqldb.Close()
	state.rows["FROM "+data.MigrationsTable] = []string{"0009_from_a_module_that_left"}
	db := data.Wrap(sqldb, data.DialectPostgres)

	_, err := data.Rollback(context.Background(), db, migrations)

	if err == nil {
		t.Fatal("an unknown migration was rolled back")
	}
	if !strings.Contains(err.Error(), "no longer declared") {
		t.Errorf("error = %v", err)
	}
}

func TestRollbackOnAnEmptyDatabase(t *testing.T) {
	sqldb, _ := newFakeDB()
	defer sqldb.Close()
	db := data.Wrap(sqldb, data.DialectPostgres)

	reverted, err := data.Rollback(context.Background(), db, migrations)

	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if len(reverted) != 0 {
		t.Fatalf("reverted = %v, want none", reverted)
	}
}

// TestStatusListsPendingAndApplied is what `aru migrate:status` prints, and the
// answer to "did that deploy actually migrate?".
func TestStatusListsPendingAndApplied(t *testing.T) {
	sqldb, state := newFakeDB()
	defer sqldb.Close()
	state.rows["FROM "+data.MigrationsTable] = []string{"0001_create_users"}
	db := data.Wrap(sqldb, data.DialectPostgres)

	status, err := data.Status(context.Background(), db, migrations)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if len(status) != 2 {
		t.Fatalf("status has %d rows, want one per declared migration", len(status))
	}
	if status[0].ID != "0001_create_users" || status[0].Batch == 0 {
		t.Errorf("applied migration = %+v, want a batch number", status[0])
	}
	if status[1].ID != "0002_add_email" || status[1].Batch != 0 {
		t.Errorf("pending migration = %+v, want batch 0", status[1])
	}
}

// TestASemicolonInACommentDoesNotSplit: the first migration whose comment
// contains one used to send the tail of the sentence to the database as a
// statement, and the error named a word from the prose.
func TestASemicolonInACommentDoesNotSplit(t *testing.T) {
	sqldb, state := newFakeDB()
	db := data.Wrap(sqldb, data.DialectSQLite)

	_, err := data.Migrate(context.Background(), db, []data.Migration{{
		ID: "one",
		Up: `
-- A partial index would be tighter; MySQL does not have one.
CREATE TABLE outbox (id TEXT PRIMARY KEY);
CREATE INDEX idx_outbox ON outbox (id);
`,
	}})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, s := range state.statements() {
		if strings.HasPrefix(strings.TrimSpace(s), "MySQL") {
			t.Fatalf("the comment was split into a statement: %q", s)
		}
	}
	if !state.sawStatement("CREATE TABLE outbox") || !state.sawStatement("CREATE INDEX idx_outbox") {
		t.Errorf("the statements did not survive the split: %v", state.statements())
	}
}

// TestASemicolonInALiteralDoesNotSplit: a default value or a seeded row can
// legitimately contain one.
func TestASemicolonInALiteralDoesNotSplit(t *testing.T) {
	sqldb, state := newFakeDB()
	db := data.Wrap(sqldb, data.DialectSQLite)

	_, err := data.Migrate(context.Background(), db, []data.Migration{{
		ID: "one",
		Up: `INSERT INTO setting (key, value) VALUES ('separators', 'a;b;c');`,
	}})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !state.sawStatement("VALUES ('separators', 'a;b;c')") {
		t.Errorf("the literal was split: %v", state.statements())
	}
}
