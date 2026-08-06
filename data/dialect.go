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
		b strings.Builder
		n int
	)
	b.Grow(len(query) + 8)

	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case c == '-' && i+1 < len(query) && query[i+1] == '-':
			// A line comment is copied through untouched.
			//
			// It used to be treated as ordinary text, so an apostrophe in prose
			// opened a string literal that never closed:
			//
			//	SELECT id FROM invoice -- don't page this by hand
			//	WHERE tenant_id = ?
			//
			// left the ? unbound, and PostgreSQL answered with a syntax error
			// about a character position, three lines from the comment that
			// caused it. splitStatements had already learned this lesson for
			// semicolons; this had not. Found by audit.
			for i < len(query) && query[i] != '\n' {
				b.WriteByte(query[i])
				i++
			}
			if i < len(query) {
				b.WriteByte('\n')
			}

		case c == '/' && i+1 < len(query) && query[i+1] == '*':
			for i < len(query) {
				b.WriteByte(query[i])
				if query[i] == '*' && i+1 < len(query) && query[i+1] == '/' {
					b.WriteByte('/')
					i++
					break
				}
				i++
			}

		case c == '\'' || c == '"':
			// A literal, or a quoted identifier. Neither holds a placeholder.
			quote := c
			b.WriteByte(c)
			i++
			for i < len(query) {
				b.WriteByte(query[i])
				// Doubling is how SQL escapes a quote inside a literal.
				if query[i] == quote {
					if i+1 < len(query) && query[i+1] == quote {
						b.WriteByte(quote)
						i++
					} else {
						break
					}
				}
				i++
			}

		case c == '?':
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))

		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
