// Package dbclone makes one Postgres database per test out of a template
// migrated once.
//
// Migrating a schema for every test is the cost that pushes suites onto one
// shared database, and one shared database is where two tests start reading
// each other's rows. Postgres copies a database at file level with CREATE
// DATABASE ... TEMPLATE, which is a fraction of a migration, so the schema is
// migrated once into a template and every test gets a copy of its own.
//
// Every database this package creates is named arandutest_..., and it never
// writes to, drops or alters a database under any other name. The database the
// connection URL names is only connected to, because a server has to be asked
// from inside some database to create another.
package dbclone

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/arandu-io/hesape/database"
)

// Prefix starts the name of every database this package creates, and is the
// only prefix it ever drops.
const Prefix = "arandutest_"

// Server is a connection to a Postgres server, held to create, clone and drop
// the databases of a test run.
type Server struct {
	raw   string
	admin *sql.DB
}

// Open connects to the server raw names, inside the database it names.
func Open(raw string) (*Server, error) {
	cfg, err := database.ParseURL(raw)
	if err != nil {
		return nil, err
	}
	if cfg.Connection != database.DialectPostgres {
		return nil, fmt.Errorf("dbclone: %s is not a Postgres URL", cfg.Redacted())
	}
	db, _, err := database.Open(cfg)
	if err != nil {
		return nil, err
	}
	return &Server{raw: raw, admin: db.Unwrap()}, nil
}

// Close closes the connection to the server. The databases stay.
func (s *Server) Close() error { return s.admin.Close() }

// URL is the connection URL of the database called name on this server: the
// URL the server was opened with, with its database replaced.
func (s *Server) URL(name string) (string, error) { return URLFor(s.raw, name) }

// URLFor is raw with the database it names replaced by name. The user, the
// password, the host and the options are kept as they were written.
func URLFor(raw, name string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("dbclone: reading the database URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("dbclone: the database URL names no server")
	}
	u.Path, u.RawPath = "/"+name, ""
	return u.String(), nil
}

// Key reduces what identifies a template -- the package directory, the
// migration -- to the short hex token a database name can carry.
func Key(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = io.WriteString(h, p)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:10]
}

// TemplateName is the template of one migration in one build: the family the
// migration belongs to, and the build the schema was migrated by. A family
// holds one template at a time; the next build replaces it.
func TemplateName(family, build string) string { return Prefix + family + "_t" + build }

// CloneName is a name for one test's database in a family, unique by the
// random part.
func CloneName(family string) (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return Prefix + family + "_c" + hex.EncodeToString(b), nil
}

// buildingSuffix marks a template whose migration has not finished. It is
// renamed to the template's own name only once the migration succeeded, so a
// process killed half way leaves a database nothing clones from.
const buildingSuffix = "_b"

// EnsureTemplate answers the template of the family for this build, creating
// and migrating it when the server does not hold it yet.
//
// migrate receives the URL of the database being built and must have closed
// every connection it opened to it when it returns: Postgres renames and
// copies only a database nobody is connected to. Connections it left are
// terminated before the rename.
//
// An advisory lock held for the whole of it makes two processes that need the
// same template wait for one migration rather than run two. Templates of the
// same family from earlier builds are dropped once the new one exists; one that
// is in use is left for a later run.
func (s *Server) EnsureTemplate(ctx context.Context, family, build string, migrate func(ctx context.Context, url string) error) (string, error) {
	name := TemplateName(family, build)
	conn, err := s.admin.Conn(ctx)
	if err != nil {
		return "", fmt.Errorf("dbclone: connecting to the server: %w", err)
	}
	defer func() { _ = conn.Close() }()

	lock := advisoryKey(name)
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lock); err != nil {
		return "", fmt.Errorf("dbclone: taking the template lock: %w", err)
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", lock) }()

	exists, err := databaseExists(ctx, conn, name)
	if err != nil || exists {
		return name, err
	}

	building := name + buildingSuffix
	// Left by a process that died while it held this lock; nobody else
	// builds under this name.
	if _, err := conn.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quote(building)+" WITH (FORCE)"); err != nil {
		return "", fmt.Errorf("dbclone: dropping the unfinished template %s: %w", building, err)
	}
	if _, err := conn.ExecContext(ctx, "CREATE DATABASE "+quote(building)); err != nil {
		return "", fmt.Errorf("dbclone: creating the template %s: %w", building, err)
	}
	url, err := s.URL(building)
	if err == nil {
		err = migrate(ctx, url)
	}
	if err != nil {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), "DROP DATABASE IF EXISTS "+quote(building)+" WITH (FORCE)")
		return "", err
	}
	if _, err := conn.ExecContext(ctx,
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()",
		building); err != nil {
		return "", fmt.Errorf("dbclone: closing the connections the migration left: %w", err)
	}
	for _, statement := range []string{
		"ALTER DATABASE " + quote(building) + " RENAME TO " + quote(name),
		// Nobody connects to a template, so nothing can keep a clone from
		// copying it.
		"ALTER DATABASE " + quote(name) + " WITH ALLOW_CONNECTIONS false",
	} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return "", fmt.Errorf("dbclone: finishing the template %s: %w", name, err)
		}
	}

	stale, err := databasesWithPrefix(ctx, conn, Prefix+family+"_t")
	if err != nil {
		return "", err
	}
	for _, old := range stale {
		if old != name {
			_, _ = conn.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quote(old))
		}
	}
	return name, nil
}

// cloneAttempts and cloneBackoff bound the wait for a template another
// statement is still holding. A copy in progress keeps the next one out for
// the moment it takes.
const (
	cloneAttempts = 40
	cloneBackoff  = 25 * time.Millisecond
)

// cloneMu keeps this process's copies of one server's templates one at a time,
// which is the order Postgres would impose anyway, without the retries.
var cloneMu sync.Mutex

// Clone copies the template into a new database of the family and answers its
// name and its URL.
func (s *Server) Clone(ctx context.Context, family, template string) (string, string, error) {
	name, err := CloneName(family)
	if err != nil {
		return "", "", err
	}
	url, err := s.URL(name)
	if err != nil {
		return "", "", err
	}

	cloneMu.Lock()
	defer cloneMu.Unlock()
	statement := "CREATE DATABASE " + quote(name) + " TEMPLATE " + quote(template)
	for attempt := 1; ; attempt++ {
		_, err = s.admin.ExecContext(ctx, statement)
		if err == nil || !InUse(err) || attempt == cloneAttempts {
			break
		}
		timer := time.NewTimer(time.Duration(attempt) * cloneBackoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", "", ctx.Err()
		case <-timer.C:
		}
	}
	if err != nil {
		return "", "", fmt.Errorf("dbclone: copying %s into %s: %w", template, name, err)
	}
	return name, url, nil
}

// Drop removes a database this package created, closing whatever is still
// connected to it. A name without the prefix is refused.
func (s *Server) Drop(ctx context.Context, name string) error {
	if !strings.HasPrefix(name, Prefix) {
		return fmt.Errorf("dbclone: refusing to drop %q, which this package did not create", name)
	}
	if _, err := s.admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quote(name)+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("dbclone: dropping %s: %w", name, err)
	}
	return nil
}

// Exists reports whether the server holds a database called name.
func (s *Server) Exists(ctx context.Context, name string) (bool, error) {
	conn, err := s.admin.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.Close() }()
	return databaseExists(ctx, conn, name)
}

// InUse reports whether err is Postgres refusing to copy, rename or drop a
// database because somebody is connected to it (SQLSTATE 55006). The driver's
// error type belongs to a module the core does not import, so the code is read
// from the text every driver prints it in.
func InUse(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "55006") || strings.Contains(msg, "is being accessed by other users")
}

func databaseExists(ctx context.Context, conn *sql.Conn, name string) (bool, error) {
	var one int
	err := conn.QueryRowContext(ctx, "SELECT 1 FROM pg_database WHERE datname = $1", name).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("dbclone: looking for %s: %w", name, err)
	}
	return true, nil
}

func databasesWithPrefix(ctx context.Context, conn *sql.Conn, prefix string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, "SELECT datname FROM pg_database")
	if err != nil {
		return nil, fmt.Errorf("dbclone: listing the databases: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
	}
	return out, rows.Err()
}

// quote is a database name as an identifier. The names this package writes are
// its prefix and hex, so the quotes never meet anything to escape; they are
// there so a name is never read as a keyword.
func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// advisoryKey is the lock a template's name is held under.
func advisoryKey(name string) int64 {
	sum := sha256.Sum256([]byte("arandutest template " + name))
	var k int64
	for _, b := range sum[:8] {
		k = k<<8 | int64(b)
	}
	return k
}

var (
	buildOnce sync.Once
	buildKey  string
	buildErr  error
)

// Build identifies the binary running: the hash of its own executable. A test
// binary is rebuilt whenever its code or a migration compiled into it changes,
// and a rebuilt binary migrates a template of its own.
func Build() (string, error) {
	buildOnce.Do(func() {
		path, err := os.Executable()
		if err != nil {
			buildErr = fmt.Errorf("dbclone: finding the running binary: %w", err)
			return
		}
		f, err := os.Open(path)
		if err != nil {
			buildErr = fmt.Errorf("dbclone: reading the running binary: %w", err)
			return
		}
		defer func() { _ = f.Close() }()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			buildErr = fmt.Errorf("dbclone: reading the running binary: %w", err)
			return
		}
		buildKey = hex.EncodeToString(h.Sum(nil))[:10]
	})
	return buildKey, buildErr
}
