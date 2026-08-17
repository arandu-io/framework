package config_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/framework/config"
	"github.com/arandu-io/framework/data"
)

// TestDefaultConnectionIsSQLite is the promise of a fresh checkout: clone, set a
// key, run. Nothing installed, no server, no Docker.
func TestDefaultConnectionIsSQLite(t *testing.T) {
	t.Setenv("APP_KEY", strings.Repeat("k", appKeyLen))

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Database.Connection != data.DialectSQLite {
		t.Fatalf("default connection = %q, want sqlite", cfg.Database.Connection)
	}
	if cfg.Database.Database != defaultSQLitePath {
		t.Fatalf("default database = %q, want %q", cfg.Database.Database, defaultSQLitePath)
	}
}

func TestLoadRejectsUnknownConnection(t *testing.T) {
	t.Setenv("APP_KEY", strings.Repeat("k", appKeyLen))
	t.Setenv("DATABASE_URL", "mongodb://127.0.0.1/db")

	_, err := config.Load()

	if err == nil {
		t.Fatal("an unsupported DB_CONNECTION was accepted")
	}
	if !strings.Contains(err.Error(), "sqlite, pgsql or mysql") {
		t.Errorf("the message must list what works, got: %v", err)
	}
}

func TestSQLiteDSNCarriesTheRequiredPragmas(t *testing.T) {
	cfg := config.DatabaseConfig{Connection: data.DialectSQLite, Database: "database/database.sqlite"}

	dsn := cfg.DSN()

	if !strings.HasPrefix(dsn, "database/database.sqlite?") {
		t.Fatalf("dsn = %q", dsn)
	}
	// Each of these is a bug that only shows up later: constraints silently
	// ignored, and "database is locked" under two concurrent requests.
	for _, pragma := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout"} {
		if !strings.Contains(dsn, pragma) {
			t.Errorf("dsn is missing %s: %s", pragma, dsn)
		}
	}
}

func TestPostgresDSN(t *testing.T) {
	cfg := config.DatabaseConfig{
		Connection: data.DialectPostgres,
		Host:       "db.internal",
		Port:       "5432",
		Database:   "arandu",
		Username:   "app",
		Password:   "s3cr3t",
	}

	dsn := cfg.DSN()

	for _, want := range []string{"postgres://", "app:", "db.internal:5432", "/arandu", "sslmode=disable"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("dsn %q is missing %q", dsn, want)
		}
	}
}

// TestTheDSNIsBuiltForTheDriverAndNotPassedThrough.
//
// DATABASE_URL is the one source now, and the DSN used to be that string
// verbatim whenever it was set. That is right for pgx and wrong for the other
// two: go-sql-driver takes user:pass@tcp(host:port)/db, with no scheme, and
// modernc/sqlite takes a path. Handing either of them the URL is a connection
// that fails with a parse error naming neither the driver nor the variable.
func TestTheDSNIsBuiltForTheDriverAndNotPassedThrough(t *testing.T) {
	mysql, err := config.ParseDatabaseURL("mysql://user:pass@db.example:3306/billing")
	if err != nil {
		t.Fatal(err)
	}
	if got := mysql.DSN(); !strings.HasPrefix(got, "user:pass@tcp(db.example:3306)/billing") {
		t.Errorf("the MySQL DSN is %q, which go-sql-driver does not read", got)
	}
	if strings.Contains(mysql.DSN(), "mysql://") {
		t.Error("the scheme reached the driver")
	}

	sqlite, err := config.ParseDatabaseURL("sqlite://database/blog.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sqlite.DSN(), "database/blog.sqlite?") {
		t.Errorf("the SQLite DSN is %q, which is not a path", sqlite.DSN())
	}

	pg, err := config.ParseDatabaseURL("postgres://user:pass@managed.example:5432/production")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"managed.example:5432", "/production", "user", "sslmode=disable"} {
		if !strings.Contains(pg.DSN(), want) {
			t.Errorf("the Postgres DSN is missing %s: %s", want, pg.DSN())
		}
	}
}

// TestRedactedHidesThePassword: the connection string shows up in logs and on the
// error page, and a password there survives in a screenshot forever.
func TestRedactedHidesThePassword(t *testing.T) {
	cfg := config.DatabaseConfig{
		Connection: data.DialectPostgres,
		Host:       "db.internal",
		Port:       "5432",
		Database:   "arandu",
		Username:   "app",
		Password:   "s3cr3t",
	}

	if got := cfg.Redacted(); strings.Contains(got, "s3cr3t") {
		t.Fatalf("Redacted leaks the password: %s", got)
	}
}

func TestValidateRequiresCredentialsForAServer(t *testing.T) {
	cfg := config.DatabaseConfig{
		Connection: data.DialectPostgres,
		Host:       "db.internal",
		Database:   "arandu",
	}

	if err := cfg.Validate(); err == nil {
		t.Fatal("a server connection without a username was accepted")
	}
}

// TestSQLitePathIsEmptyForMemoryAndServers tells the application when it has to
// create a directory: SQLite creates the file, never the folder above it.
func TestSQLitePathIsEmptyForMemoryAndServers(t *testing.T) {
	file := config.DatabaseConfig{Connection: data.DialectSQLite, Database: "database/database.sqlite"}
	if got := file.SQLitePath(); got != "database/database.sqlite" {
		t.Errorf("file path = %q", got)
	}

	memory := config.DatabaseConfig{Connection: data.DialectSQLite, Database: ":memory:"}
	if got := memory.SQLitePath(); got != "" {
		t.Errorf("in-memory path = %q, want empty", got)
	}

	server := config.DatabaseConfig{Connection: data.DialectPostgres, Database: "arandu"}
	if got := server.SQLitePath(); got != "" {
		t.Errorf("server path = %q, want empty", got)
	}
}

// TestSQLiteRefusesAServerDatabaseName.
//
// For SQLite, DB_DATABASE is a PATH. A value with neither a separator nor an
// extension is a server database name pasted into a SQLite setting, and every
// layer accepts it: SQLite creates the file, the application runs, the
// migrations apply, and a database appears in the project root under a name no
// ignore rule expects.
//
// This repository committed three of them -- a database, its -shm and its -wal
// -- twice, because the file has no extension and every rule written for one
// missed it.
func TestSQLiteRefusesAServerDatabaseName(t *testing.T) {
	for _, name := range []string{"arandu_blog", "blog", "myapp"} {
		cfg := config.DatabaseConfig{Connection: data.DialectSQLite, Database: name}

		err := cfg.Validate()
		if err == nil {
			t.Errorf("%q was accepted as a SQLite path", name)
			continue
		}
		// The message has to carry the fix, not only the refusal.
		if !strings.Contains(err.Error(), "database/"+name+".sqlite") {
			t.Errorf("the error does not say what to write instead: %v", err)
		}
	}
}

// TestSQLiteAcceptsAPath: a separator or an extension is enough, and both of
// these are things people legitimately write.
func TestSQLiteAcceptsAPath(t *testing.T) {
	for _, path := range []string{
		"database/database.sqlite", "./blog.db", "/tmp/x/test.sqlite",
		"blog.sqlite", "database/blog",
	} {
		cfg := config.DatabaseConfig{Connection: data.DialectSQLite, Database: path}
		if err := cfg.Validate(); err != nil {
			t.Errorf("%q was refused: %v", path, err)
		}
	}
}
