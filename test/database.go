package test

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/arandu-io/hesape/database"
)

// Match is what a row has to look like: column name to value.
//
// A nil value means the column is NULL, which is a different question from
// "equals the empty string" and the one people mean when they write nil.
//
// It is a map and not a struct because the assertion is about columns, not
// about the entity: the entity is what the code under test built, and checking
// the row against the same struct that wrote it proves the struct equals
// itself. Naming the columns is what makes the assertion say something -- and
// it is the only spelling that catches the field that never reached SQL.
type Match map[string]any

// NotNull is the value a Match carries to ask that a column holds something,
// without saying what.
//
//	test.Match{"tenant": "acme", "deleted_at": test.NotNull}
//
// nil already meant IS NULL, and the opposite had no spelling at all: a match
// could say a row was live and could not say it was deleted, so "this row is
// soft deleted" was not an assertion anybody could write. Every other value in
// a match is compared with a placeholder, and a placeholder holding a sentinel
// would compare against the sentinel -- so this one is read where the statement
// is built and becomes IS NOT NULL instead.
var NotNull = notNull{}

// notNull is unexported so that NotNull is the only one of it. A second value
// of the same shape would compile and would silently ask a different question.
type notNull struct{}

// String is what a failure message prints for it, in the column list beside the
// values the match compares against.
func (notNull) String() string { return "NOT NULL" }

// AssertDatabaseHas fails unless at least one row in the table matches.
//
//	test.AssertDatabaseHas(t, ctx, db, "users", test.Match{
//		"tenant": "acme",
//		"email":  "alice@example.com",
//	})
//
// It reads the table directly, with no auth.Grant, and that is the point rather
// than an oversight (see the package comment): it checks what the
// Grant-protected path wrote, so it cannot be that path. It is also why the
// tenant belongs in the match -- without it the assertion proves the row exists
// somewhere, which in a multi-tenant application is the bug rather than the
// assurance.
func AssertDatabaseHas(t *testing.T, ctx context.Context, db *database.DB, table string, where Match) {
	t.Helper()

	n, err := countRows(ctx, db, table, where)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if n == 0 {
		t.Errorf("no row in %s matches %s\n%s", table, where, sample(ctx, db, table))
	}
}

// AssertDatabaseMissing fails when any row in the table matches.
//
// It is the half that catches a delete that deleted nothing, an update that
// wrote to the wrong tenant's row, and a rollback that did not roll back.
func AssertDatabaseMissing(t *testing.T, ctx context.Context, db *database.DB, table string, where Match) {
	t.Helper()

	n, err := countRows(ctx, db, table, where)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if n != 0 {
		t.Errorf("%d row(s) in %s match %s and none should\n%s", n, table, where, sample(ctx, db, table))
	}
}

// AssertDatabaseCount fails unless the table holds exactly that many rows.
//
// It takes no match, which is deliberate: counting matching rows is
// AssertDatabaseHas answered with a number. What this one answers is the
// question a match cannot -- that the handler wrote one row and not three, and
// that the seeder ran once.
func AssertDatabaseCount(t *testing.T, ctx context.Context, db *database.DB, table string, want int) {
	t.Helper()

	n, err := countRows(ctx, db, table, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if n != want {
		t.Errorf("%s holds %d row(s), want %d\n%s", table, n, want, sample(ctx, db, table))
	}
}

// DeletedAtColumn is the column [AssertSoftDeleted] and [AssertNotSoftDeleted]
// ask about, and the one a model marks a row deleted in unless it was told to
// use another.
//
// A model that was told to use another is asserted about with
// [AssertDatabaseHas] and [NotNull] directly, which is what these two are: the
// name for the column almost every model uses.
const DeletedAtColumn = "deleted_at"

// AssertSoftDeleted fails unless a row matching is there and is marked deleted.
//
//	test.AssertSoftDeleted(t, ctx, db, "invoices", test.Match{
//		"tenant": "acme",
//		"id":     "inv-1",
//	})
//
// Both halves are the assertion. A delete that removed the row outright passes
// no more than a delete that did nothing: the first is a row that is gone when
// it should be recoverable, the second is a row that is still live, and the
// failure has to tell them apart. [AssertDatabaseMissing] is the one for a row
// that should not be there at all.
//
// The match must not name the deleted_at column itself -- that is the half this
// assertion supplies, and a match that supplied it too could contradict it.
func AssertSoftDeleted(t *testing.T, ctx context.Context, db *database.DB, table string, where Match) {
	t.Helper()

	deleted, err := withDeletedAt(where, NotNull)
	if err != nil {
		t.Fatalf("%v", err)
	}
	n, err := countRows(ctx, db, table, deleted)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if n == 0 {
		t.Errorf("no soft-deleted row in %s matches %s\n%s", table, deleted, sample(ctx, db, table))
	}
}

// AssertNotSoftDeleted fails unless a row matching is there and is not marked
// deleted.
//
// It is not the negation of [AssertSoftDeleted]: a row that was hard deleted
// fails both, because a row that is gone is not a row that survived. That is
// the failure this one catches -- a delete that was meant to leave the row
// recoverable and removed it instead reads as success to any assertion that
// only asks whether the row is still live.
//
// The match must not name the deleted_at column itself.
func AssertNotSoftDeleted(t *testing.T, ctx context.Context, db *database.DB, table string, where Match) {
	t.Helper()

	live, err := withDeletedAt(where, nil)
	if err != nil {
		t.Fatalf("%v", err)
	}
	n, err := countRows(ctx, db, table, live)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if n == 0 {
		t.Errorf("no live row in %s matches %s\n%s", table, live, sample(ctx, db, table))
	}
}

// withDeletedAt copies the match and adds the question the caller did not ask.
// It copies rather than writes because the caller's map is the caller's, and a
// test that hands the same one to two assertions would be asserting about a
// match the first of them edited.
func withDeletedAt(where Match, value any) (Match, error) {
	if _, taken := where[DeletedAtColumn]; taken {
		return nil, fmt.Errorf("the match already names %s, which this assertion supplies: "+
			"say it once. A model that marks a row deleted in another column is asserted "+
			"about with AssertDatabaseHas and test.NotNull", DeletedAtColumn)
	}
	out := make(Match, len(where)+1)
	maps.Copy(out, where)
	out[DeletedAtColumn] = value
	return out, nil
}

// String renders the match the way the failure message needs it: in column
// order, so two runs of the same failing test print the same line.
func (m Match) String() string {
	if len(m) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, col := range m.columns() {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s=%s", col, format(m[col]))
	}
	b.WriteByte('}')
	return b.String()
}

func (m Match) columns() []string {
	cols := make([]string, 0, len(m))
	for col := range m {
		cols = append(cols, col)
	}
	sort.Strings(cols)
	return cols
}

// countRows is the whole mechanism the three assertions share.
func countRows(ctx context.Context, db *database.DB, table string, where Match) (int, error) {
	if db == nil {
		return 0, fmt.Errorf("counting rows in %s: no database handle", table)
	}

	query, args, err := countQuery(table, where)
	if err != nil {
		return 0, err
	}

	var n int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting rows in %s: %w", table, err)
	}
	return n, nil
}

// countQuery builds the statement. Values go in placeholders; the table and the
// column names cannot, because no database takes an identifier as a parameter,
// so they are checked against a closed shape instead.
//
// A test helper that interpolated an identifier unchecked would be the one place
// in the collection where a string reaches SQL unexamined, and helpers get
// copied. The columns are sorted so the statement is the same on every run,
// which matters the day somebody asserts on the SQL the collector recorded.
func countQuery(table string, where Match) (string, []any, error) {
	if err := checkIdentifier("table", table); err != nil {
		return "", nil, err
	}

	var b strings.Builder
	b.WriteString("SELECT COUNT(*) FROM ")
	b.WriteString(table)

	args := make([]any, 0, len(where))
	for i, col := range where.columns() {
		if err := checkIdentifier("column", col); err != nil {
			return "", nil, err
		}
		if i == 0 {
			b.WriteString(" WHERE ")
		} else {
			b.WriteString(" AND ")
		}
		b.WriteString(col)

		// nil is NULL, and NULL never equals anything -- including a
		// placeholder holding nil. Written as "= ?" the assertion would fail
		// for a row that is exactly right, which is the failure that costs an
		// afternoon.
		if where[col] == nil {
			b.WriteString(" IS NULL")
			continue
		}
		// The other half of the same rule: NULL never equals anything, so
		// "holds something" is not a comparison either.
		if _, ok := where[col].(notNull); ok {
			b.WriteString(" IS NOT NULL")
			continue
		}
		b.WriteString(" = ?")
		args = append(args, where[col])
	}
	return b.String(), args, nil
}

// identifier is a bare table or column name: what a migration in this
// collection writes. Quoted names, schema prefixes and expressions are refused
// rather than escaped, because escaping is where the second dialect-dependent
// rule would go.
var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func checkIdentifier(kind, name string) error {
	if !identifier.MatchString(name) {
		return fmt.Errorf("%q is not a %s name: it has to be a bare identifier, "+
			"letters, digits and underscore, starting with a letter or an underscore", name, kind)
	}
	return nil
}

// sampleRows is how much of the table a failure prints. Enough to see what is
// there instead of what was asked for, short enough that a failing test in CI is
// still readable.
const sampleRows = 10

// sample renders some of the table, for the failure message.
//
// The answer to "why did that not match" is almost always in the rows, and
// getting it any other way means running the test again with a print in it. It
// never fails the test itself: it is already reporting a failure, and a second
// error about the sample would bury the first.
func sample(ctx context.Context, db *database.DB, table string) string {
	if db == nil || !identifier.MatchString(table) {
		return ""
	}

	rows, err := db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s LIMIT %d", table, sampleRows+1))
	if err != nil {
		return fmt.Sprintf("(could not read %s: %v)", table, err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return fmt.Sprintf("(could not read %s: %v)", table, err)
	}

	var out strings.Builder
	seen := 0
	for rows.Next() {
		seen++
		if seen > sampleRows {
			out.WriteString("  … more\n")
			break
		}

		cells := make([]any, len(cols))
		for i := range cells {
			cells[i] = new(any)
		}
		if err := rows.Scan(cells...); err != nil {
			return fmt.Sprintf("(could not read %s: %v)", table, err)
		}

		out.WriteString("  ")
		for i, col := range cols {
			if i > 0 {
				out.WriteString(" ")
			}
			fmt.Fprintf(&out, "%s=%s", col, format(*cells[i].(*any)))
		}
		out.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		return fmt.Sprintf("(could not read %s: %v)", table, err)
	}
	if seen == 0 {
		return fmt.Sprintf("%s is empty", table)
	}
	return fmt.Sprintf("%s holds:\n%s", table, strings.TrimRight(out.String(), "\n"))
}

// format prints one cell. Drivers hand text back as []byte often enough that
// printing it raw fills the failure with byte arrays, which is the message
// being useless in the one moment it is read.
func format(v any) string {
	switch value := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return fmt.Sprintf("%q", string(value))
	case string:
		return fmt.Sprintf("%q", value)
	default:
		return fmt.Sprintf("%v", value)
	}
}
