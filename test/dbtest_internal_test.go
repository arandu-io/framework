package test

import (
	"strings"
	"testing"
)

func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// Every way the database is chosen, decided without a server: a branch that
// can only be reached with Postgres running is a branch nobody runs.
func TestChooseEngine(t *testing.T) {
	const pg = "postgres://u:p@127.0.0.1:5432/main?sslmode=disable"
	for _, c := range []struct {
		name string
		vars map[string]string
		opts serveOptions
		want engine
		url  string
		says string
	}{
		{name: "nothing set skips", want: engineSkip, says: "ARANDU_TEST_DATABASE_URL"},
		{name: "nothing set in CI fails", vars: map[string]string{"CI": "true"}, want: engineFail, says: "CI is set"},
		{name: "CI=false still skips", vars: map[string]string{"CI": "false"}, want: engineSkip},
		{name: "DATABASE_URL on Postgres", vars: map[string]string{"DATABASE_URL": pg}, want: enginePostgres, url: pg},
		{name: "the test variable wins", vars: map[string]string{
			"ARANDU_TEST_DATABASE_URL": pg, "DATABASE_URL": "sqlite://database/database.sqlite",
		}, want: enginePostgres, url: pg},
		{name: "Database wins over the environment", vars: map[string]string{"DATABASE_URL": "sqlite://x/y.sqlite"},
			opts: serveOptions{database: pg}, want: enginePostgres, url: pg},
		{name: "a SQLite URL is a temporary file", vars: map[string]string{"DATABASE_URL": "sqlite://database/database.sqlite"},
			want: engineSQLite},
		{name: "SQLite wins over the environment", vars: map[string]string{"DATABASE_URL": pg, "CI": "true"},
			opts: serveOptions{sqlite: true}, want: engineSQLite},
		{name: "SQLite and Database together fail", opts: serveOptions{sqlite: true, database: pg},
			want: engineFail, says: "once"},
		{name: "MySQL has no template", vars: map[string]string{"DATABASE_URL": "mysql://u:p@127.0.0.1:3306/main"},
			want: engineFail, says: "no template"},
		{name: "a URL that is not one fails", vars: map[string]string{"DATABASE_URL": "127.0.0.1:5432/main"},
			want: engineFail, says: "DATABASE_URL"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := chooseEngine(env(c.vars), &c.opts)
			if got.engine != c.want || got.url != c.url {
				t.Errorf("chose %d %q, want %d %q (%s)", got.engine, got.url, c.want, c.url, got.reason)
			}
			if !strings.Contains(got.reason, c.says) {
				t.Errorf("the reason %q does not say %q", got.reason, c.says)
			}
		})
	}
}
