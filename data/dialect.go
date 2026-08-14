// The SQL flavour of a connection, answered by
// github.com/arandu-io/hesape/database.
//
// Dialect is a named string type and aliases, so the three constants below are
// the hesape values: a DB_CONNECTION parsed by this package and one parsed by
// hesape/database produce the same value, and Rebind is the same function.

package data

import (
	"time"

	"github.com/arandu-io/hesape/database"
)

// Dialect is the SQL flavour of a connection.
//
// The names are the conventional DB_CONNECTION values, so an .env reads the way
// somebody expects it to. Driver and Rebind are methods on the hesape type and
// come across with the alias.
type Dialect = database.Dialect

// Supported dialects.
const (
	// DialectSQLite is the default for local development: a file, no server,
	// nothing to install.
	DialectSQLite = database.DialectSQLite
	// DialectPostgres is the production target.
	DialectPostgres = database.DialectPostgres
	// DialectMySQL is supported and is not the recommendation. Every query here
	// is written with "?", which MySQL takes directly, so nothing about the SQL
	// changes; what changes is that Postgres is where the migration story, the
	// transactional DDL and the outbox relay are least surprising.
	DialectMySQL = database.DialectMySQL
)

// ParseDialect validates a DB_CONNECTION value.
func ParseDialect(v string) (Dialect, error) { return database.ParseDialect(v) }

// Day truncates a time to midnight UTC, which is what a date column means.
//
// It exists because DATE is the one type in the portable subset that the three
// engines do not agree about: PostgreSQL drops the time part on write and
// SQLite stores whatever the driver sent, so the same code, on the same day,
// returns different values depending on the engine. `aru make:module` emits it
// for every field declared as a date.
func Day(t time.Time) time.Time { return database.Day(t) }
