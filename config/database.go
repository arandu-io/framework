package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arandu-io/framework/data"
)

// DatabaseConfig describes one connection.
//
// It comes from ONE variable, and that is the whole of the design:
//
//	DATABASE_URL=postgres://arandu:arandu@127.0.0.1:5432/arandu?sslmode=disable
//	DATABASE_URL=mysql://arandu:arandu@127.0.0.1:3306/arandu
//	DATABASE_URL=sqlite://database/database.sqlite
//
// The parts spelled out separately -- DB_CONNECTION, DB_HOST, DB_PORT,
// DB_USERNAME, DB_PASSWORD, DB_DATABASE -- with DATABASE_URL winning over all of
// them when set, are two ways to say where the database is, and the failure that
// produces is the worst kind: half the parts overridden and half not, an
// application pointing at a host from one variable and a database from another,
// with every value individually correct.
//
// One string also travels. Every managed platform hands out exactly this, a
// compose file passes it as one line, and a person moving between them copies
// one thing instead of six.
//
// The fields below are what the URL parsed into. They are read, never written:
// what wrote them is ParseDatabaseURL, in one place, with one set of rules.
type DatabaseConfig struct {
	Connection data.Dialect
	Database   string // file path for SQLite, database name otherwise
	Host       string
	Port       string
	Username   string
	Password   string

	// Options is the query string, carried through to the driver. sslmode for
	// Postgres, parseTime for MySQL, the _pragma list for SQLite -- anything
	// the driver understands and this package should not have an opinion about.
	Options url.Values

	// URL is what was parsed, kept for the error messages and for a driver that
	// would rather have the string than the parts.
	URL string
}

// DefaultSQLitePath is where a fresh project keeps its database file.
const DefaultSQLitePath = "database/database.sqlite"

// DefaultDatabaseURL is what a project with no DATABASE_URL runs on: a file,
// no server, nothing installed.
const DefaultDatabaseURL = "sqlite://" + DefaultSQLitePath

// The variables that carry the connection in parts, refused rather than
// ignored.
//
// Ignoring them would be a quiet failure: an .env full of DB_HOST and
// DB_PASSWORD, an application connecting to something else entirely, and every
// value in the file individually correct.
var retiredDatabaseVars = []string{
	"DB_CONNECTION", "DB_HOST", "DB_PORT", "DB_USERNAME", "DB_PASSWORD", "DB_DATABASE",
}

func loadDatabase() (DatabaseConfig, error) {
	if name, value := firstSet(retiredDatabaseVars); name != "" {
		return DatabaseConfig{}, fmt.Errorf(`%s is set, and the connection comes from one URL now.

    DATABASE_URL=postgres://user:password@127.0.0.1:5432/dbname
    DATABASE_URL=mysql://user:password@127.0.0.1:3306/dbname
    DATABASE_URL=sqlite://database/database.sqlite

Seven variables and a URL that overrode them were two ways to say where the
database is, and the failure that produces is half of one and half of the other:
a host from one variable, a database from another, every value correct on its
own.

Remove %s (it is %q) and the rest of the DB_* block.`, name, name, value)
	}

	raw := env("DATABASE_URL", DefaultDatabaseURL)
	return ParseDatabaseURL(raw)
}

// ParseDatabaseURL reads the one variable.
//
// The schemes are the conventional ones, so a string copied from a platform,
// a compose file or another language works unchanged: postgres, postgresql,
// mysql, sqlite, file.
func ParseDatabaseURL(raw string) (DatabaseConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DatabaseConfig{}, fmt.Errorf("DATABASE_URL is empty")
	}

	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		// One answer for both, because they are one mistake. A value with no
		// scheme either fails to parse -- "127.0.0.1:5432/blog" is refused with
		// "first path segment in URL cannot contain colon", which explains
		// nothing -- or parses into something with no scheme. Neither says the
		// thing that is missing.
		return DatabaseConfig{}, fmt.Errorf(`DATABASE_URL is not a connection URL: %q

The scheme is what says which database it is:

    postgres://user:password@host:5432/dbname
    mysql://user:password@host:3306/dbname
    sqlite://database/database.sqlite`, raw)
	}

	cfg := DatabaseConfig{URL: raw, Options: u.Query()}

	switch strings.ToLower(u.Scheme) {
	case "sqlite", "sqlite3", "file":
		cfg.Connection = data.DialectSQLite
		// sqlite://database/x.sqlite parses as host=database, path=/x.sqlite,
		// and the file is the two joined. sqlite:///tmp/x.sqlite has no host and
		// an absolute path. Both are things people write, and both mean a path.
		cfg.Database = u.Host + u.Path
		if u.Opaque != "" {
			// sqlite:database/x.sqlite, with no slashes at all.
			cfg.Database = u.Opaque
		}

	case "postgres", "postgresql":
		cfg.Connection = data.DialectPostgres
		cfg.Port = "5432"

	case "mysql", "mariadb":
		cfg.Connection = data.DialectMySQL
		cfg.Port = "3306"

	default:
		return DatabaseConfig{}, fmt.Errorf(
			`DATABASE_URL uses the scheme %q, and this framework speaks sqlite, pgsql or mysql:

    postgres://user:password@host:5432/dbname
    mysql://user:password@host:3306/dbname
    sqlite://database/database.sqlite`, u.Scheme)
	}

	if cfg.Connection != data.DialectSQLite {
		cfg.Host = u.Hostname()
		if p := u.Port(); p != "" {
			cfg.Port = p
		}
		cfg.Database = strings.TrimPrefix(u.Path, "/")
		if u.User != nil {
			cfg.Username = u.User.Username()
			cfg.Password, _ = u.User.Password()
		}
	}

	return cfg, nil
}

// firstSet returns the first variable of the list that carries a value.
func firstSet(names []string) (string, string) {
	for _, name := range names {
		if value := env(name, ""); value != "" {
			return name, value
		}
	}
	return "", ""
}

// Validate reports a connection that cannot work, at boot rather than on the
// first query.
func (d DatabaseConfig) Validate() error {
	if d.Database == "" {
		return fmt.Errorf("DATABASE_URL names no database: %q", d.URL)
	}
	if d.Connection != data.DialectSQLite {
		if d.Host == "" {
			return fmt.Errorf("DATABASE_URL names no host: %q", d.URL)
		}
		if d.Username == "" {
			return fmt.Errorf(`DATABASE_URL carries no user: %q

    postgres://USER:password@host:5432/dbname`, d.URL)
		}
		return nil
	}

	// For SQLite, DB_DATABASE is a PATH. A value with neither a separator nor an
	// extension is a server database NAME that somebody pasted into a SQLite
	// setting -- and it is accepted by everything: SQLite creates the file, the
	// application runs, the migrations apply, and a database appears in the
	// project root under a name no .gitignore pattern expects.
	//
	// That is not hypothetical. This repository committed three of them --
	// arandu_blog, its -shm and its -wal -- twice, because the file has no
	// extension and every rule written for one missed it.
	if !strings.ContainsAny(d.Database, `/\`) && !strings.Contains(d.Database, ".") {
		return fmt.Errorf(`DB_DATABASE is %q, which reads as a server database name rather than a file.

For sqlite it is a PATH, and a bare name puts the file in whichever directory
the process started in -- usually the project root, where it is easy to commit
by accident.

    DATABASE_URL=sqlite://database/%s.sqlite

If you meant a server, the scheme is what says so: postgres:// or mysql://.`,
			d.Database, d.Database)
	}
	return nil
}

// DSN returns the connection string for the driver of this dialect.
func (d DatabaseConfig) DSN() string {
	switch d.Connection {
	case data.DialectSQLite:
		// The pragmas are not decoration. Without foreign_keys, SQLite ignores
		// the constraints the migration declares; without WAL and a busy
		// timeout, two concurrent requests turn into "database is locked".
		//
		// They are defaults and not a fixed string: a URL that sets one of them
		// wins, because somebody who writes _pragma in a DATABASE_URL means it.
		// Written by hand rather than through url.Values.Encode, which
		// percent-encodes the parentheses: _pragma=foreign_keys%281%29. The
		// driver decodes them again, so it works -- and a DSN nobody can read
		// in a log is a DSN nobody can check, which is the whole reason these
		// three are worth being explicit about.
		params := []string{
			"_pragma=foreign_keys(1)",
			"_pragma=journal_mode(WAL)",
			"_pragma=busy_timeout(5000)",
		}
		if len(d.Options["_pragma"]) > 0 {
			// A URL that sets one means it, so none of the defaults apply: half
			// a set of pragmas from each source is a combination nobody chose.
			params = nil
			for _, p := range d.Options["_pragma"] {
				params = append(params, "_pragma="+p)
			}
		}
		for key, values := range d.Options {
			if key == "_pragma" {
				continue
			}
			for _, v := range values {
				params = append(params, url.QueryEscape(key)+"="+url.QueryEscape(v))
			}
		}
		sort.Strings(params[len(params)-len(params):])
		return d.Database + "?" + strings.Join(params, "&")

	case data.DialectMySQL:
		// go-sql-driver takes its own shape, which is not a URL: the host goes
		// in tcp(...) and there is no scheme. Handing it the DATABASE_URL
		// verbatim is a connection that fails with a parse error naming neither.
		q := clone(d.Options)
		if q.Get("parseTime") == "" {
			// Without it, a DATETIME column scans into []byte and every
			// time.Time field in the application is empty -- silently.
			q.Set("parseTime", "true")
		}
		return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?%s",
			d.Username, d.Password, d.Host, d.Port, d.Database, q.Encode())

	default:
		// pgx reads a URL, so this one is rebuilt rather than passed through --
		// the parsed parts are what Validate checked, and passing the raw string
		// would mean the driver sees something Validate never looked at.
		q := clone(d.Options)
		if q.Get("sslmode") == "" {
			q.Set("sslmode", "disable")
		}
		u := url.URL{
			Scheme:   "postgres",
			Host:     d.Host + ":" + d.Port,
			Path:     "/" + d.Database,
			RawQuery: q.Encode(),
		}
		if d.Username != "" {
			u.User = url.UserPassword(d.Username, d.Password)
		}
		return u.String()
	}
}

// clone copies the options so a default written into them does not mutate the
// configuration a second caller reads.
func clone(in url.Values) url.Values {
	out := url.Values{}
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// SQLitePath returns the file the database lives in, or the empty string for a
// server-based connection. The application uses it to create the directory
// before opening: SQLite creates the file, never the directory above it.
func (d DatabaseConfig) SQLitePath() string {
	// The URL is always set now, so testing it here would answer "no path" for
	// every SQLite connection there is -- and nothing would create the directory
	// the file goes in, because SQLite creates the file and never the directory
	// above it.
	if d.Connection != data.DialectSQLite {
		return ""
	}
	if d.Database == ":memory:" || strings.HasPrefix(d.Database, "file::memory:") {
		return ""
	}
	return filepath.Clean(d.Database)
}

// Redacted returns the connection as a string safe to log or show on the error
// page: the password is never part of it.
func (d DatabaseConfig) Redacted() string {
	if d.Connection == data.DialectSQLite {
		return fmt.Sprintf("sqlite:%s", d.Database)
	}
	return fmt.Sprintf("%s://%s@%s:%s/%s", d.Connection, d.Username, d.Host, d.Port, d.Database)
}
