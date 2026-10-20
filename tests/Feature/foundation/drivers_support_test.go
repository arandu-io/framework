package feature

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"

	"github.com/arandu-io/framework/foundation/bootstrap"
	"github.com/arandu-io/hesape/database"
)

// A database/sql driver that answers a ping and nothing else. The drivers are
// built over an open database, and what this module can test without a server
// is everything the framework builds around that database: no engine is linked
// here, because the core's dependency budget has no room for one, and a test
// dependency is still a dependency.
func init() { sql.Register("arandu-ping-only", pingDriver{}) }

type pingDriver struct{}

func (pingDriver) Open(string) (driver.Conn, error) { return pingConn{}, nil }

type pingConn struct{}

var errOnlyPing = errors.New("the ping-only database answers a ping and nothing else")

func (pingConn) Prepare(string) (driver.Stmt, error) { return nil, errOnlyPing }
func (pingConn) Close() error                        { return nil }
func (pingConn) Begin() (driver.Tx, error)           { return nil, errOnlyPing }
func (pingConn) Ping(context.Context) error          { return nil }

// pingDB opens the ping-only database.
func pingDB(t *testing.T) *database.DB {
	t.Helper()
	raw, err := sql.Open("arandu-ping-only", "")
	if err != nil {
		t.Fatalf("opening the ping-only database: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return database.Wrap(raw, database.DialectSQLite)
}

// settingsFor loads the sections from a clean environment plus the values
// given, from a directory with no .env.
func settingsFor(t *testing.T, values ...string) bootstrap.Settings {
	t.Helper()
	s, err := loadSettingsFor(t, values...)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	return s
}

func loadSettingsFor(t *testing.T, values ...string) (bootstrap.Settings, error) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("APP_ENV", "dev")
	t.Setenv("APP_KEY", "base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	t.Setenv("DATABASE_URL", "sqlite://"+filepath.Join(t.TempDir(), "test.sqlite"))
	for _, key := range []string{
		"APP_URL", "CACHE_STORE", "CACHE_PREFIX", "REDIS_URL", "REDIS_CA_FILE", "REDIS_CERT_FILE",
		"REDIS_KEY_FILE", "REDIS_TLS_SERVER_NAME", "SESSION_DRIVER", "QUEUE_CONNECTION",
		"FILESYSTEM_DISK", "FILESYSTEM_ROOT", "FILESYSTEM_BUCKET", "FILESYSTEM_ENDPOINT",
		"FILESYSTEM_ACCESS_KEY_ID", "FILESYSTEM_SECRET_ACCESS_KEY", "MAIL_URL", "QUEUE_WORKERS",
		"APP_NAME", "APP_DEBUG", "HTTP_ADDR", "HTTP_MAX_BODY_BYTES", "TRUSTED_PROXIES", "ARANDU_TRACING_SECRET",
	} {
		t.Setenv(key, "")
	}
	for i := 0; i+1 < len(values); i += 2 {
		t.Setenv(values[i], values[i+1])
	}
	return bootstrap.LoadSettings()
}
