package data_test

import (
	"testing"

	"github.com/arandu-io/framework/data"
)

// TestRebindTranslatesPlaceholders is the whole portability layer in one test:
// queries are written with "?" and Postgres gets numbered placeholders.
func TestRebindTranslatesPlaceholders(t *testing.T) {
	query := `SELECT id FROM users WHERE tenant_id = ? AND email = ? LIMIT ?`

	if got := data.DialectPostgres.Rebind(query); got != `SELECT id FROM users WHERE tenant_id = $1 AND email = $2 LIMIT $3` {
		t.Fatalf("postgres rebind = %q", got)
	}
	if got := data.DialectSQLite.Rebind(query); got != query {
		t.Fatalf("sqlite must not touch the query, got %q", got)
	}
	if got := data.DialectMySQL.Rebind(query); got != query {
		t.Fatalf("mysql must not touch the query, got %q", got)
	}
}

// TestRebindLeavesStringLiteralsAlone: '?' is an ordinary character inside a LIKE
// pattern or in seeded text, and renumbering it would corrupt the statement.
func TestRebindLeavesStringLiteralsAlone(t *testing.T) {
	cases := map[string]string{
		`SELECT * FROM t WHERE a = ? AND b LIKE '%?%'`:     `SELECT * FROM t WHERE a = $1 AND b LIKE '%?%'`,
		`INSERT INTO t (msg, n) VALUES ('why? really', ?)`: `INSERT INTO t (msg, n) VALUES ('why? really', $1)`,
		`SELECT 'no placeholders here'`:                    `SELECT 'no placeholders here'`,
	}

	for query, want := range cases {
		if got := data.DialectPostgres.Rebind(query); got != want {
			t.Errorf("Rebind(%q)\n  got  %q\n  want %q", query, got, want)
		}
	}
}

func TestParseDialect(t *testing.T) {
	for _, name := range []string{"sqlite", "pgsql", "mysql"} {
		if _, err := data.ParseDialect(name); err != nil {
			t.Errorf("ParseDialect(%q): %v", name, err)
		}
	}

	_, err := data.ParseDialect("oracle")
	if err == nil {
		t.Fatal("an unsupported connection was accepted")
	}
	// The message has to list what is valid: a typo in .env is the most common
	// way to hit this.
	if got := err.Error(); got == "" || !contains(got, "sqlite, pgsql or mysql") {
		t.Errorf("error = %v", err)
	}
}

func TestDialectDriverNames(t *testing.T) {
	want := map[data.Dialect]string{
		data.DialectSQLite:   "sqlite",
		data.DialectPostgres: "pgx",
		data.DialectMySQL:    "mysql",
	}
	for dialect, driver := range want {
		if got := dialect.Driver(); got != driver {
			t.Errorf("%s driver = %q, want %q", dialect, got, driver)
		}
	}
}

// TestWrapDefaultsToSQLite keeps the development default in one place: an empty
// dialect is the file-based one, never a server nobody started.
func TestWrapDefaultsToSQLite(t *testing.T) {
	sqldb, _ := newFakeDB()
	defer sqldb.Close()

	if got := data.Wrap(sqldb, "").Dialect(); got != data.DialectSQLite {
		t.Fatalf("default dialect = %q, want sqlite", got)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// TestAnApostropheInACommentDoesNotEatThePlaceholder is a bug an audit found,
// and it is the same one splitStatements had already learned: prose inside SQL
// is prose, not SQL.
//
// The rebinder toggled its "inside a literal" flag on every apostrophe, so one
// in an English contraction opened a string that never closed -- and every
// placeholder after it went to PostgreSQL as a literal ?, which answers with a
// syntax error about a character position, lines away from the comment.
func TestAnApostropheInACommentDoesNotEatThePlaceholder(t *testing.T) {
	cases := []struct {
		what  string
		query string
		want  string
	}{
		{
			"an apostrophe in a line comment",
			"SELECT id FROM invoice -- don't page this by hand\nWHERE tenant_id = ?",
			"SELECT id FROM invoice -- don't page this by hand\nWHERE tenant_id = $1",
		},
		{
			"an apostrophe in a block comment",
			"/* it isn't indexed */ SELECT id FROM invoice WHERE tenant_id = ?",
			"/* it isn't indexed */ SELECT id FROM invoice WHERE tenant_id = $1",
		},
		{
			"a comment at the end, with no newline",
			"SELECT ? -- that's all",
			"SELECT $1 -- that's all",
		},
		{
			"a real literal still hides its question mark",
			"SELECT id FROM t WHERE label = 'why?' AND tenant_id = ?",
			"SELECT id FROM t WHERE label = 'why?' AND tenant_id = $1",
		},
		{
			"an escaped quote inside a literal",
			"SELECT id FROM t WHERE label = 'it''s ?' AND tenant_id = ?",
			"SELECT id FROM t WHERE label = 'it''s ?' AND tenant_id = $1",
		},
		{
			"a quoted identifier",
			`SELECT "order?" FROM t WHERE tenant_id = ?`,
			`SELECT "order?" FROM t WHERE tenant_id = $1`,
		},
	}

	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			if got := data.DialectPostgres.Rebind(c.query); got != c.want {
				t.Errorf("Rebind\n got: %s\nwant: %s", got, c.want)
			}
		})
	}
}
