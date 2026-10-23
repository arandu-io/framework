package test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/test/internal/dbclone"
	"github.com/arandu-io/hesape/database"
)

// engine is what database a test runs on, as chooseEngine decided it.
type engine int

const (
	engineSkip engine = iota
	engineFail
	engineSQLite
	enginePostgres
)

// engineChoice is the decision and what it rests on: the URL of the server for
// Postgres, and the sentence the test is skipped or failed with otherwise.
type engineChoice struct {
	engine engine
	url    string
	reason string
}

// The variables the test database is read from, the first set one winning.
// The test-only one comes first so a project whose DATABASE_URL names the
// development database can point its tests elsewhere.
var databaseVars = []string{"ARANDU_TEST_DATABASE_URL", "DATABASE_URL"}

// chooseEngine decides the database from the options and the environment,
// read through lookup. It is a function of its inputs so every branch can be
// tested without a server and without the process environment.
func chooseEngine(lookup func(string) string, o *serveOptions) engineChoice {
	if o.sqlite && o.database != "" {
		return engineChoice{engine: engineFail, reason: "Serve was given both test.SQLite and " +
			"test.Database; say which engine once"}
	}
	if o.sqlite {
		return engineChoice{engine: engineSQLite}
	}

	raw, from := o.database, "test.Database"
	for _, name := range databaseVars {
		if raw != "" {
			break
		}
		raw, from = lookup(name), name
	}
	if raw == "" {
		reason := "no test database: set ARANDU_TEST_DATABASE_URL (or DATABASE_URL) to a Postgres server, " +
			"or declare the test SQLite-safe with test.SQLite()"
		if ci, _ := strconv.ParseBool(lookup("CI")); ci {
			return engineChoice{engine: engineFail, reason: reason + ". CI is set, and a skip there is a suite " +
				"that proved nothing and reported green"}
		}
		return engineChoice{engine: engineSkip, reason: reason}
	}

	cfg, err := database.ParseURL(raw)
	if err != nil {
		return engineChoice{engine: engineFail, reason: fmt.Sprintf("%s: %v", from, err)}
	}
	switch cfg.Connection {
	case database.DialectPostgres:
		return engineChoice{engine: enginePostgres, url: raw}
	case database.DialectSQLite:
		return engineChoice{engine: engineSQLite}
	default:
		return engineChoice{engine: engineFail, reason: fmt.Sprintf("%s names %s, and Serve makes a database "+
			"per test on Postgres, by copying a template, and on SQLite, as a file. %s has no template to copy",
			from, cfg.Redacted(), cfg.Connection)}
	}
}

// testDatabase makes the test its database, migrated, and arranges for it to
// go when the test ends. It answers the URL and the open handle.
func testDatabase(t *testing.T, o *serveOptions) (string, *database.DB) {
	t.Helper()

	choice := chooseEngine(os.Getenv, o)
	switch choice.engine {
	case engineSkip:
		t.Skip(choice.reason)
	case engineFail:
		t.Fatal(choice.reason)
	case engineSQLite:
		return sqliteDatabase(t, o)
	}
	return postgresDatabase(t, o, choice.url)
}

// sqliteDatabase is a temporary file, migrated for this test alone.
func sqliteDatabase(t *testing.T, o *serveOptions) (string, *database.DB) {
	t.Helper()
	url := "sqlite://" + filepath.Join(t.TempDir(), "test.sqlite")
	db := openTestDatabase(t, url)
	if o.exportEnv {
		t.Setenv("DATABASE_URL", url)
	}
	if err := o.migrate(t.Context(), Env{DatabaseURL: url, DB: db}); err != nil {
		t.Fatalf("migrating the test database: %v", err)
	}
	return url, db
}

// openTestDatabase opens url and closes it when the test ends.
func openTestDatabase(t *testing.T, url string) *database.DB {
	t.Helper()
	cfg, err := database.ParseURL(url)
	if err != nil {
		t.Fatalf("%v", err)
	}
	db, closeDB, err := database.Open(cfg)
	if err != nil {
		t.Fatalf("opening the test database: %v", err)
	}
	t.Cleanup(closeDB)
	return db
}

// template is one template of this process: built once, by whichever test
// asked first, while every other test asking for it waits.
type template struct {
	mu   sync.Mutex
	done bool
	name string
	err  error
}

var (
	templatesMu sync.Mutex
	templates   = map[string]*template{}
)

// postgresDatabase is a copy of the package's template, dropped when the test
// ends.
func postgresDatabase(t *testing.T, o *serveOptions, raw string) (string, *database.DB) {
	t.Helper()

	server, err := dbclone.Open(raw)
	if err != nil {
		t.Fatalf("connecting to the test database server: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("%v", err)
	}
	build, err := dbclone.Build()
	if err != nil {
		t.Fatalf("%v", err)
	}
	family := dbclone.Key(dir, migrationName(o.migrate))

	tpl := templateFor(raw + "\x00" + family + "\x00" + build)
	tpl.mu.Lock()
	if !tpl.done {
		tpl.name, tpl.err = server.EnsureTemplate(t.Context(), family, build, func(ctx context.Context, url string) error {
			return migrateTemplate(t, o, url)
		})
		tpl.done = true
	}
	name, err := tpl.name, tpl.err
	tpl.mu.Unlock()
	if err != nil {
		t.Fatalf("migrating the test database template: %v", err)
	}

	clone, url, err := server.Clone(t.Context(), family, name)
	if err != nil {
		t.Fatalf("%v", err)
	}
	// Registered before the handle is opened, so it runs after the handle is
	// closed: cleanups run last first.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Drop(ctx, clone); err != nil {
			t.Errorf("%v", err)
		}
	})
	return url, openTestDatabase(t, url)
}

func templateFor(key string) *template {
	templatesMu.Lock()
	defer templatesMu.Unlock()
	tpl, ok := templates[key]
	if !ok {
		tpl = &template{}
		templates[key] = tpl
	}
	return tpl
}

// migrateTemplate runs the migration on the template being built and closes
// every connection it opened, which Postgres needs before it renames or
// copies the database.
func migrateTemplate(t *testing.T, o *serveOptions, url string) error {
	cfg, err := database.ParseURL(url)
	if err != nil {
		return err
	}
	db, closeDB, err := database.Open(cfg)
	if err != nil {
		return fmt.Errorf("opening the template: %w", err)
	}
	defer closeDB()
	if o.exportEnv {
		t.Setenv("DATABASE_URL", url)
	}
	return o.migrate(t.Context(), Env{DatabaseURL: url, DB: db})
}
