package data

import (
	"fmt"
	"strconv"
	"strings"
)

// Dialect is the SQL flavour of a connection.
//
// The names match Laravel's DB_CONNECTION values, because the .env of an Arandu
// project is meant to be readable by someone arriving from there.
type Dialect string

// Supported dialects.
const (
	// DialectSQLite is the default for local development: a file, no server,
	// nothing to install.
	DialectSQLite Dialect = "sqlite"
	// DialectPostgres is the production target.
	DialectPostgres Dialect = "pgsql"
	// DialectMySQL is accepted by the connection layer. Repositories are not
	// portable to it yet: MySQL has no RETURNING, so every insert needs a second
	// statement. See docs/adr/0009.
	DialectMySQL Dialect = "mysql"
)

// ParseDialect validates a DB_CONNECTION value.
func ParseDialect(v string) (Dialect, error) {
	switch d := Dialect(v); d {
	case DialectSQLite, DialectPostgres, DialectMySQL:
		return d, nil
	default:
		return "", fmt.Errorf("unknown database connection %q (expected sqlite, pgsql or mysql)", v)
	}
}

// Driver is the database/sql driver name a dialect expects to be registered
// under. The application imports the driver; the framework only names it, which
// is what keeps the core free of database dependencies.
func (d Dialect) Driver() string {
	switch d {
	case DialectPostgres:
		return "pgx"
	case DialectMySQL:
		return "mysql"
	default:
		return "sqlite"
	}
}

// Rebind translates the portable "?" placeholder into what the dialect expects.
//
// Every query in this framework is written with "?", the form SQLite and MySQL
// use, and Postgres gets "$1, $2, ..." here. That is the entire portability
// layer: there is no query builder, and the SQL you read in a repository is the
// SQL that runs. Anything beyond placeholders -- a type, a function -- is the
// repository's job to keep portable.
//
// Placeholders inside string literals are left alone, because '?' is an ordinary
// character in a LIKE pattern or in seeded data.
func (d Dialect) Rebind(query string) string {
	if d != DialectPostgres || !strings.ContainsRune(query, '?') {
		return query
	}

	var (
		b       strings.Builder
		n       int
		inQuote bool
	)
	b.Grow(len(query) + 8)

	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case c == '\'':
			// Doubled quotes ('') are an escaped quote inside a literal, and
			// leave the quoting state unchanged.
			inQuote = !inQuote
			b.WriteByte(c)
		case c == '?' && !inQuote:
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
