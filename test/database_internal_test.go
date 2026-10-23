package test

import (
	"context"
	"testing"
)

func TestCountQueryPutsValuesInPlaceholdersAndSortsTheColumns(t *testing.T) {
	query, args, err := countQuery("users", Match{"tenant": "acme", "email": "alice@example.com"})
	if err != nil {
		t.Fatalf("building the count: %v", err)
	}

	// Sorted, so the statement is the same on every run: a map ranged in its own
	// order produces a different query each time, and the first thing that
	// notices is somebody diffing what the collector recorded.
	const want = "SELECT COUNT(*) FROM users WHERE email = ? AND tenant = ?"
	if query != want {
		t.Errorf("built %q, want %q", query, want)
	}
	if len(args) != 2 || args[0] != "alice@example.com" || args[1] != "acme" {
		t.Errorf("bound %v, want [alice@example.com acme]", args)
	}
}

// NULL never equals anything, a placeholder holding nil included. Written as
// "= ?" the assertion fails for the row that is exactly right.
func TestCountQueryAsksForNullInsteadOfComparingToIt(t *testing.T) {
	query, args, err := countQuery("users", Match{"deleted_at": nil})
	if err != nil {
		t.Fatalf("building the count: %v", err)
	}

	const want = "SELECT COUNT(*) FROM users WHERE deleted_at IS NULL"
	if query != want {
		t.Errorf("built %q, want %q", query, want)
	}
	if len(args) != 0 {
		t.Errorf("bound %v, want nothing", args)
	}
}

// The other half of the same rule, and the half that was missing: a match could
// say a column was NULL and had no way to say it held something, so "this row
// is soft deleted" was not an assertion anybody could write. A sentinel bound
// to a placeholder would compare the column against the sentinel.
func TestCountQueryAsksForNotNullWithoutBindingTheSentinel(t *testing.T) {
	query, args, err := countQuery("users", Match{"deleted_at": NotNull, "tenant": "acme"})
	if err != nil {
		t.Fatalf("building the count: %v", err)
	}

	const want = "SELECT COUNT(*) FROM users WHERE deleted_at IS NOT NULL AND tenant = ?"
	if query != want {
		t.Errorf("built %q, want %q", query, want)
	}
	if len(args) != 1 || args[0] != "acme" {
		t.Errorf("bound %v, want [acme]", args)
	}
}

func TestCountQueryWithoutAMatchCountsTheWholeTable(t *testing.T) {
	query, args, err := countQuery("users", nil)
	if err != nil {
		t.Fatalf("building the count: %v", err)
	}

	const want = "SELECT COUNT(*) FROM users"
	if query != want {
		t.Errorf("built %q, want %q", query, want)
	}
	if len(args) != 0 {
		t.Errorf("bound %v, want nothing", args)
	}
}

// An identifier cannot go in a placeholder, so it is checked instead. A helper
// that interpolated one unchecked would be the single place in the collection
// where a string reaches SQL unexamined -- and helpers are what people copy.
func TestCountQueryRefusesAnIdentifierThatIsNotOne(t *testing.T) {
	for _, c := range []struct {
		name  string
		table string
		match Match
	}{
		{"a table carrying a statement", "users; DROP TABLE users", nil},
		{"a column carrying one", "users", Match{"email = 'a' OR 1": 1}},
		{"a quoted name", `"users"`, nil},
		{"a schema prefix", "public.users", nil},
		{"nothing at all", "", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := countQuery(c.table, c.match); err == nil {
				t.Fatalf("built a query for %q and should have refused", c.table)
			}
		})
	}
}

func TestMatchPrintsItselfInColumnOrder(t *testing.T) {
	got := Match{"tenant": "acme", "deleted_at": nil, "count": 3}.String()

	const want = `{count=3, deleted_at=NULL, tenant="acme"}`
	if got != want {
		t.Errorf("printed %s, want %s", got, want)
	}
}

// The failure message has to say which question was asked, and a sentinel that
// printed as a struct literal would say it in a shape nobody reads.
func TestMatchPrintsTheNotNullPredicateAsThePredicate(t *testing.T) {
	got := Match{"deleted_at": NotNull}.String()

	const want = `{deleted_at=NOT NULL}`
	if got != want {
		t.Errorf("printed %s, want %s", got, want)
	}
}

// A soft delete leaves the row and stamps the column. Asking whether the row
// exists is not the assertion: a delete that removed it outright would pass
// nothing, and one that did nothing at all would pass a plain AssertDatabaseHas.
func TestAssertSoftDeletedAsksForTheRowAndForTheStamp(t *testing.T) {
	db, fake := newFakeDB(t)
	fake.count = 1

	AssertSoftDeleted(t, context.Background(), db, "invoices", Match{"tenant": "acme", "id": "inv-1"})

	const want = "SELECT COUNT(*) FROM invoices WHERE deleted_at IS NOT NULL AND id = ? AND tenant = ?"
	if got := fake.lastQuery(); got != want {
		t.Errorf("asked %q, want %q", got, want)
	}
}

// Not the negation of the one above: a row that was hard deleted fails both,
// because a row that is gone is not a row that survived.
func TestAssertNotSoftDeletedAsksForTheRowAndForTheAbsenceOfTheStamp(t *testing.T) {
	db, fake := newFakeDB(t)
	fake.count = 1

	AssertNotSoftDeleted(t, context.Background(), db, "invoices", Match{"tenant": "acme", "id": "inv-1"})

	const want = "SELECT COUNT(*) FROM invoices WHERE deleted_at IS NULL AND id = ? AND tenant = ?"
	if got := fake.lastQuery(); got != want {
		t.Errorf("asked %q, want %q", got, want)
	}
}

// The assertion supplies the deleted_at half, so a match that supplies it too
// is a test saying one thing twice -- and able to say two contradictory things,
// of which SQL would answer only the second.
func TestTheSoftDeleteAssertionsRefuseAMatchThatNamesTheColumnItself(t *testing.T) {
	if _, err := withDeletedAt(Match{DeletedAtColumn: nil}, NotNull); err == nil {
		t.Error("accepted a match that already named the column the assertion supplies")
	}
}

// A match handed to two assertions has to survive the first one: the caller's
// map is the caller's, and the column this adds is not something they asked for.
func TestTheSoftDeleteAssertionsDoNotEditTheMatchTheyWereGiven(t *testing.T) {
	db, fake := newFakeDB(t)
	fake.count = 1

	where := Match{"tenant": "acme"}
	AssertSoftDeleted(t, context.Background(), db, "invoices", where)

	if len(where) != 1 {
		t.Errorf("the match came back as %s, want {tenant=\"acme\"}", where)
	}
}

func TestAssertDatabaseHasCountsWithTheMatchItWasGiven(t *testing.T) {
	db, fake := newFakeDB(t)
	fake.count = 1

	AssertDatabaseHas(t, context.Background(), db, "users", Match{"tenant": "acme", "email": "alice@example.com"})

	const want = "SELECT COUNT(*) FROM users WHERE email = ? AND tenant = ?"
	if got := fake.lastQuery(); got != want {
		t.Errorf("asked %q, want %q", got, want)
	}
}

func TestAssertDatabaseMissingPassesWhenNothingMatches(t *testing.T) {
	db, fake := newFakeDB(t)
	fake.count = 0

	AssertDatabaseMissing(t, context.Background(), db, "users", Match{"email": "nobody@example.com"})

	if fake.lastQuery() == "" {
		t.Error("asserted nothing: no query reached the database")
	}
}

// The count is of the table, not of a match: counting matching rows is
// AssertDatabaseHas answered with a number, and one question gets one way to
// ask it.
func TestAssertDatabaseCountAsksAboutTheWholeTable(t *testing.T) {
	db, fake := newFakeDB(t)
	fake.count = 3

	AssertDatabaseCount(t, context.Background(), db, "users", 3)

	const want = "SELECT COUNT(*) FROM users"
	if got := fake.lastQuery(); got != want {
		t.Errorf("asked %q, want %q", got, want)
	}
}

// The failure message has to say what is in the table, or the answer to "why
// did that not match" costs a second run with a print in it.
func TestTheFailureShowsTheRowsThatAreThere(t *testing.T) {
	db, fake := newFakeDB(t)
	fake.columns = []string{"id", "email", "deleted_at"}
	fake.rows = [][]any{
		{"u1", []byte("alice@example.com"), nil},
		{"u2", []byte("bob@example.com"), nil},
	}

	got := sample(context.Background(), db, "users")

	want := "users holds:\n" +
		`  id="u1" email="alice@example.com" deleted_at=NULL` + "\n" +
		`  id="u2" email="bob@example.com" deleted_at=NULL`
	if got != want {
		t.Errorf("printed\n%s\nwant\n%s", got, want)
	}
}

func TestTheFailureSaysSoWhenTheTableIsEmpty(t *testing.T) {
	db, fake := newFakeDB(t)
	fake.columns = []string{"id"}

	if got := sample(context.Background(), db, "users"); got != "users is empty" {
		t.Errorf("printed %q, want %q", got, "users is empty")
	}
}

// A table that would need reading before it could be printed is not read: the
// sample runs while a failure is already being reported, and a second failure
// coming out of the message would bury the first.
func TestTheFailureNeverAsksAboutSomethingThatIsNotAnIdentifier(t *testing.T) {
	db, fake := newFakeDB(t)

	if got := sample(context.Background(), db, "users; DROP TABLE users"); got != "" {
		t.Errorf("printed %q, want nothing", got)
	}
	if fake.lastQuery() != "" {
		t.Errorf("sent %q to the database", fake.lastQuery())
	}
}
