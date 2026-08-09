package config_test

import (
	"strings"
	"testing"

	"github.com/arandu-io/framework/config"
	"github.com/arandu-io/framework/data"
)

// The one variable, and every shape of it somebody actually writes.
//
// A string copied out of a platform's dashboard, a compose file, a Prisma
// schema or another language has to work unchanged -- that is the whole reason
// the connection is a URL rather than seven variables.

func TestEveryShapeOfDatabaseURL(t *testing.T) {
	for _, c := range []struct {
		url      string
		dialect  data.Dialect
		host     string
		port     string
		database string
		user     string
		password string
	}{
		{"postgres://arandu:secret@127.0.0.1:5432/blog", data.DialectPostgres,
			"127.0.0.1", "5432", "blog", "arandu", "secret"},

		// postgresql:// is what psql and every managed platform print.
		{"postgresql://u:p@db.internal:6543/app", data.DialectPostgres,
			"db.internal", "6543", "app", "u", "p"},

		// No port: the conventional one for the scheme, not an empty string that
		// the driver turns into a connection refused nobody can explain.
		{"postgres://u:p@db.internal/app", data.DialectPostgres,
			"db.internal", "5432", "app", "u", "p"},

		{"mysql://root:root@127.0.0.1:3306/shop", data.DialectMySQL,
			"127.0.0.1", "3306", "shop", "root", "root"},
		{"mysql://root@127.0.0.1/shop", data.DialectMySQL,
			"127.0.0.1", "3306", "shop", "root", ""},

		// The three ways a person writes a file path, all meaning one thing.
		{"sqlite://database/blog.sqlite", data.DialectSQLite, "", "", "database/blog.sqlite", "", ""},
		{"sqlite:database/blog.sqlite", data.DialectSQLite, "", "", "database/blog.sqlite", "", ""},
		{"sqlite:///tmp/blog.sqlite", data.DialectSQLite, "", "", "/tmp/blog.sqlite", "", ""},
		{"file://database/blog.sqlite", data.DialectSQLite, "", "", "database/blog.sqlite", "", ""},
	} {
		t.Run(c.url, func(t *testing.T) {
			got, err := config.ParseDatabaseURL(c.url)
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if got.Connection != c.dialect {
				t.Errorf("dialect = %s, want %s", got.Connection, c.dialect)
			}
			if got.Host != c.host || got.Port != c.port {
				t.Errorf("host:port = %s:%s, want %s:%s", got.Host, got.Port, c.host, c.port)
			}
			if got.Database != c.database {
				t.Errorf("database = %q, want %q", got.Database, c.database)
			}
			if got.Username != c.user || got.Password != c.password {
				t.Errorf("credentials = %q:%q, want %q:%q", got.Username, got.Password, c.user, c.password)
			}
		})
	}
}

// TestAPasswordWithSpecialCharactersSurvives.
//
// A generated password contains @ / : ? # more often than not, and every one of
// them means something in a URL. Percent-encoding is how it travels, and a
// parser that does not decode it connects with the wrong password and reports
// "authentication failed" about a password that is correct.
func TestAPasswordWithSpecialCharactersSurvives(t *testing.T) {
	// p@ss:w/rd?#
	got, err := config.ParseDatabaseURL("postgres://user:p%40ss%3Aw%2Frd%3F%23@host:5432/db")
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "p@ss:w/rd?#" {
		t.Fatalf("password = %q", got.Password)
	}
	if got.Host != "host" || got.Database != "db" {
		t.Errorf("the @ in the password moved the host: %s/%s", got.Host, got.Database)
	}
}

// TestTheQueryReachesTheDriver: sslmode, application_name, a timezone -- things
// this package should have no opinion about and must not drop.
func TestTheQueryReachesTheDriver(t *testing.T) {
	got, err := config.ParseDatabaseURL(
		"postgres://u:p@host:5432/db?sslmode=require&application_name=billing")
	if err != nil {
		t.Fatal(err)
	}
	dsn := got.DSN()
	for _, want := range []string{"sslmode=require", "application_name=billing"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("the DSN dropped %s: %s", want, dsn)
		}
	}
	// And the default does not fight what was asked for.
	if strings.Contains(dsn, "sslmode=disable") {
		t.Error("the default overrode the sslmode in the URL")
	}
}

// TestTheSQLitePragmasStayReadable.
//
// url.Values.Encode percent-encodes the parentheses -- _pragma=foreign_keys%281%29
// -- and the driver decodes them again, so it works. A DSN nobody can read in a
// log is a DSN nobody can check, and these three are the difference between
// foreign keys being enforced and being ignored.
func TestTheSQLitePragmasStayReadable(t *testing.T) {
	got, err := config.ParseDatabaseURL("sqlite://database/blog.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"_pragma=foreign_keys(1)", "_pragma=journal_mode(WAL)", "_pragma=busy_timeout(5000)"} {
		if !strings.Contains(got.DSN(), want) {
			t.Errorf("the DSN is missing %s: %s", want, got.DSN())
		}
	}
}

// TestAURLThatSetsAPragmaReplacesTheDefaults: half a set from each source is a
// combination nobody chose.
func TestAURLThatSetsAPragmaReplacesTheDefaults(t *testing.T) {
	got, err := config.ParseDatabaseURL("sqlite://x.sqlite?_pragma=journal_mode(DELETE)")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.DSN(), "journal_mode(DELETE)") {
		t.Fatalf("what the URL asked for was dropped: %s", got.DSN())
	}
	if strings.Contains(got.DSN(), "journal_mode(WAL)") {
		t.Error("both were sent, and the driver takes the last one it reads")
	}
}

// TestTheRetiredVariablesAreRefusedAndNotIgnored.
//
// Ignoring them is the quiet failure this change removes: an .env full of
// DB_HOST and DB_PASSWORD, an application connected to something else, and
// every value in the file correct on its own.
func TestTheRetiredVariablesAreRefusedAndNotIgnored(t *testing.T) {
	for _, name := range []string{"DB_CONNECTION", "DB_HOST", "DB_PORT", "DB_USERNAME", "DB_PASSWORD", "DB_DATABASE"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("APP_KEY", strings.Repeat("k", config.AppKeyLen))
			t.Setenv("DATABASE_URL", "sqlite://database/blog.sqlite")
			t.Setenv(name, "something")

			_, err := config.Load()
			if err == nil {
				t.Fatalf("%s was ignored", name)
			}
			if !strings.Contains(err.Error(), "DATABASE_URL=") {
				t.Errorf("the error does not say what to write instead: %v", err)
			}
		})
	}
}

// TestAURLWithNoSchemeSaysWhichOnesExist: "host:5432/db" is what somebody
// writes on the first try.
func TestAURLWithNoSchemeSaysWhichOnesExist(t *testing.T) {
	_, err := config.ParseDatabaseURL("127.0.0.1:5432/blog")
	if err == nil {
		t.Fatal("a URL with no scheme was accepted")
	}
	for _, want := range []string{"postgres://", "mysql://", "sqlite://"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not offer %s: %v", want, err)
		}
	}
}
