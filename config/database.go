package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/arandu-io/framework/data"
)

// DatabaseConfig describes one connection.
//
// The field names follow Laravel's DB_* variables on purpose: a developer coming
// from there should be able to read the .env of an Arandu project without a
// translation table. The default connection is SQLite, so a fresh checkout runs
// with nothing installed.
type DatabaseConfig struct {
	Connection data.Dialect
	Database   string // file path for SQLite, database name otherwise
	Host       string
	Port       string
	Username   string
	Password   string

	// URL, when set, wins over every field above. Platforms hand out a single
	// DATABASE_URL, and rebuilding it from parts is how a deployment ends up
	// talking to the wrong database.
	URL string
}

// DefaultSQLitePath is where a fresh project keeps its database file. It mirrors
// Laravel's database/database.sqlite.
const DefaultSQLitePath = "database/database.sqlite"

func loadDatabase() (DatabaseConfig, error) {
	connection, err := data.ParseDialect(env("DB_CONNECTION", string(data.DialectSQLite)))
	if err != nil {
		return DatabaseConfig{}, err
	}

	cfg := DatabaseConfig{
		Connection: connection,
		Database:   env("DB_DATABASE", defaultDatabaseName(connection)),
		Host:       env("DB_HOST", "127.0.0.1"),
		Port:       env("DB_PORT", defaultPort(connection)),
		Username:   env("DB_USERNAME", ""),
		Password:   env("DB_PASSWORD", ""),
		URL:        env("DATABASE_URL", ""),
	}
	return cfg, nil
}

func defaultDatabaseName(d data.Dialect) string {
	if d == data.DialectSQLite {
		return DefaultSQLitePath
	}
	return "arandu"
}

func defaultPort(d data.Dialect) string {
	switch d {
	case data.DialectPostgres:
		return "5432"
	case data.DialectMySQL:
		return "3306"
	default:
		return ""
	}
}

// Validate reports a connection that cannot work, at boot rather than on the
// first query.
func (d DatabaseConfig) Validate() error {
	if d.URL != "" {
		if _, err := url.Parse(d.URL); err != nil {
			return fmt.Errorf("DATABASE_URL is not a valid URL: %w", err)
		}
		return nil
	}
	if d.Database == "" {
		return fmt.Errorf("DB_DATABASE is required")
	}
	if d.Connection != data.DialectSQLite {
		if d.Host == "" {
			return fmt.Errorf("DB_HOST is required for %s", d.Connection)
		}
		if d.Username == "" {
			return fmt.Errorf("DB_USERNAME is required for %s", d.Connection)
		}
	}
	return nil
}

// DSN returns the connection string for the driver of this dialect.
func (d DatabaseConfig) DSN() string {
	if d.URL != "" {
		return d.URL
	}

	switch d.Connection {
	case data.DialectSQLite:
		// _pragma arguments are not decoration. Without foreign_keys, SQLite
		// ignores the constraints the migration declares; without WAL and a busy
		// timeout, two concurrent requests turn into "database is locked".
		return d.Database + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

	case data.DialectMySQL:
		return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
			d.Username, d.Password, d.Host, d.Port, d.Database)

	default:
		u := url.URL{
			Scheme: "postgres",
			Host:   d.Host + ":" + d.Port,
			Path:   "/" + d.Database,
		}
		if d.Username != "" {
			u.User = url.UserPassword(d.Username, d.Password)
		}
		q := url.Values{}
		q.Set("sslmode", env("DB_SSLMODE", "disable"))
		u.RawQuery = q.Encode()
		return u.String()
	}
}

// SQLitePath returns the file the database lives in, or the empty string for a
// server-based connection. The application uses it to create the directory
// before opening: SQLite creates the file, never the directory above it.
func (d DatabaseConfig) SQLitePath() string {
	if d.Connection != data.DialectSQLite || d.URL != "" {
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
