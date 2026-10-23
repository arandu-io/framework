package test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/hesape/database"
	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/routing"
)

// Env is what Serve made for one test: the address the server listens on and
// the database the test owns. The boot function, the migration and the
// seeders are handed it, and nothing in it is shared with another test.
type Env struct {
	// URL is the address of the server, http://127.0.0.1:<port>, with no
	// trailing slash. It is what the application is told its own address is.
	//
	// The migration sees it empty. On Postgres it runs once, into a template
	// every test of the package is copied from, so nothing of one test may be
	// written into it; on SQLite it sees the same, so a migration that works
	// on one engine works on the other.
	URL string

	// DatabaseURL is the connection URL of this test's database.
	DatabaseURL string

	// DB is this test's database, opened by Serve and closed by it when the
	// test ends. An application composed over it must not close it.
	DB *database.DB
}

// Application is what a boot function hands back: the application composed
// and booted over the test's database.
type Application struct {
	// Handler answers every request, exactly as the binary serves it.
	Handler http.Handler

	// Sessions is the store the application's guards read the session from.
	// [As] starts its sessions here, and refuses a server without one.
	Sessions *hhttp.SessionStore

	// Routes are the application's routes, which [Server.Route] builds an
	// address from by name.
	Routes []*routing.Route

	// Shutdown stops what the boot started. It runs when the test ends, after
	// the server stopped answering and before the database is closed. Nil
	// when there is nothing to stop.
	Shutdown func() error
}

// Boot composes and boots the application over the test's database: the
// application's own composition -- the function its bootstrap/app.go is
// built from -- called with what Serve made.
type Boot func(ctx context.Context, env Env) (Application, error)

// Option changes what Serve does.
type Option func(*serveOptions)

type serveOptions struct {
	migrate    func(ctx context.Context, env Env) error
	seeds      []func(ctx context.Context, env Env) error
	sqlite     bool
	database   string
	setenv     [][2]string
	exportEnv  bool
	readyRoute string
}

// Migrate is how Serve builds the schema: the application's migrations, run
// the way a deploy runs them, over env.DB. It is required.
//
// On Postgres it runs once per package and migration, into a template every
// test of the package is copied from, and again only when the test binary
// changes. It is told which migration it is by the function itself, so pass
// the same function everywhere one schema is meant.
//
// On SQLite it runs for every test, on that test's file.
func Migrate(fn func(ctx context.Context, env Env) error) Option {
	return func(o *serveOptions) { o.migrate = fn }
}

// Seed runs fn on the test's own database after the application booted and
// before the server answers, so what it writes is there for the first
// request. Several run in the order they were given. A seeder may reach the
// application's services through what the boot function kept.
func Seed(fn func(ctx context.Context, env Env) error) Option {
	return func(o *serveOptions) { o.seeds = append(o.seeds, fn) }
}

// SQLite declares the test SQLite-safe: it runs on a temporary SQLite file of
// its own, whatever database the environment names. It is for a test that
// asserts nothing an engine decides -- the production engine is what a
// feature and a journey are proved on.
func SQLite() Option { return func(o *serveOptions) { o.sqlite = true } }

// Database names the server the test's database is made on, in place of the
// one the environment names.
func Database(url string) Option { return func(o *serveOptions) { o.database = url } }

// Setenv sets an environment variable for the test, with t.Setenv, before the
// migration and the boot.
//
// The environment is the process's, so a test that sets one cannot run in
// parallel with another, and t.Setenv refuses a test that called t.Parallel.
// Configuration an application can take as an argument is better handed to
// it from [Env].
func Setenv(key, value string) Option {
	return func(o *serveOptions) { o.setenv = append(o.setenv, [2]string{key, value}) }
}

// ExportEnv writes DATABASE_URL and APP_URL into the environment with
// t.Setenv, for an application whose configuration is read from it: the
// migration sees the database it migrates and the boot sees the test's
// database and the server's address. It carries what [Setenv] carries -- the
// test cannot run in parallel.
func ExportEnv() Option { return func(o *serveOptions) { o.exportEnv = true } }

// ReadyRoute names the route Serve asks, over the socket, until it answers
// 200 before handing the server to the test. Without it Serve asks the route
// named "arandu.health" when the application has one, and otherwise asks
// nothing: the socket is listening before Serve returns, so a request waits
// for the server rather than failing.
func ReadyRoute(name string) Option { return func(o *serveOptions) { o.readyRoute = name } }

// readyName is the route an application composed by the framework answers its
// readiness on.
const readyName = "arandu.health"

// readyTimeout bounds the wait for the ready route, and readyPoll is how often
// it is asked.
const (
	readyTimeout = 10 * time.Second
	readyPoll    = 20 * time.Millisecond
)

// Server is an application Serve booted and is serving on a socket. It stops
// when the test ends.
type Server struct {
	// URL is the address it listens on, http://127.0.0.1:<port>, with no
	// trailing slash.
	URL string

	t        *testing.T
	env      Env
	app      Application
	handler  http.Handler
	sessions *hhttp.SessionStore
	routes   *routing.Routes
	remote   *http.Client
}

// Serve boots the application through boot and serves it on a free port of
// 127.0.0.1 until the test ends.
//
//	server := test.Serve(t, tests.Boot, test.Migrate(tests.Migrate))
//	server.Client().Get(server.Route("home")).AssertOk()
//
// In order, it:
//   - listens on 127.0.0.1:0, so the address is known before anything boots;
//   - sets the variables [Setenv] and [ExportEnv] asked for;
//   - makes the test a database of its own, migrated by [Migrate] (see
//     below);
//   - calls boot with the address and the database;
//   - runs the [Seed] functions;
//   - serves, and waits for the ready route (see [ReadyRoute]).
//
// When the test ends the server stops answering, the application's Shutdown
// runs, the database is closed and, on Postgres, dropped.
//
// # The database
//
// It is made on the server ARANDU_TEST_DATABASE_URL names, or DATABASE_URL
// when that is empty, or the one [Database] names.
//   - Postgres: a copy (CREATE DATABASE ... TEMPLATE) of a template migrated
//     once, named arandutest_... and dropped when the test ends. The database
//     the URL names is only connected to.
//   - SQLite: a temporary file.
//   - [SQLite] puts the test on a temporary file whatever is set.
//
// With no database named the test is skipped -- and fails instead when the CI
// environment variable is true, because a skipped suite in CI is a suite that
// proved nothing and reported green.
//
// Every test gets its own database and its own port, so tests that call
// t.Parallel do not see each other's rows. [Setenv] and [ExportEnv] are the
// exceptions: the environment is the process's.
func Serve(t *testing.T, boot Boot, opts ...Option) *Server {
	t.Helper()

	var o serveOptions
	for _, opt := range opts {
		opt(&o)
	}
	if boot == nil {
		t.Fatalf("Serve was handed a nil boot function")
	}
	if o.migrate == nil {
		t.Fatalf("Serve needs test.Migrate: an application booted over an empty schema answers " +
			"its first request with a missing table, and the test reports that instead of what it is about")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on 127.0.0.1: %v", err)
	}
	listenerOpen := true
	t.Cleanup(func() {
		if listenerOpen {
			_ = listener.Close()
		}
	})
	base := "http://" + listener.Addr().String()

	for _, kv := range o.setenv {
		t.Setenv(kv[0], kv[1])
	}
	if o.exportEnv {
		t.Setenv("APP_URL", base)
	}

	ctx := t.Context()
	env := Env{URL: base}
	env.DatabaseURL, env.DB = testDatabase(t, &o)
	if o.exportEnv {
		t.Setenv("DATABASE_URL", env.DatabaseURL)
	}

	app, err := boot(ctx, env)
	if err != nil {
		t.Fatalf("booting the application: %v", err)
	}
	if app.Shutdown != nil {
		t.Cleanup(func() {
			if err := app.Shutdown(); err != nil {
				t.Errorf("shutting the application down: %v", err)
			}
		})
	}
	if app.Handler == nil {
		t.Fatalf("the boot function answered an Application with no Handler")
	}

	for i, seed := range o.seeds {
		if err := seed(ctx, env); err != nil {
			t.Fatalf("seeder %d: %v", i+1, err)
		}
	}

	routes := routing.NewRoutes()
	for _, r := range app.Routes {
		routes.Add(r)
	}
	routes.Refresh()

	// The client below is outside the outbound guard on purpose: it reaches only
	// the server this function started on 127.0.0.1, and a guard that refuses
	// loopback would refuse the one address it exists to reach.
	transport := &http.Transport{}
	s := &Server{
		URL: base, t: t, env: env, app: app, handler: app.Handler, sessions: app.Sessions, routes: routes,
		remote: &http.Client{
			Transport: transport,
			// Redirects are the test's to assert, not the client's to follow:
			// see Client.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}

	server := &http.Server{Handler: app.Handler, ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	listenerOpen = false
	go func() { served <- server.Serve(listener) }()
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(stop); err != nil {
			_ = server.Close()
		}
		if err := <-served; err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("the server stopped with %v", err)
		}
	})

	s.waitReady(o.readyRoute)
	return s
}

// waitReady asks the ready route over the socket until it answers 200.
func (s *Server) waitReady(name string) {
	s.t.Helper()
	if name == "" {
		if !s.routes.HasNamedRoute(readyName) {
			return
		}
		name = readyName
	}
	path := s.Route(name)

	deadline := time.NewTimer(readyTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(readyPoll)
	defer tick.Stop()

	last := "no answer"
	for {
		req, err := http.NewRequestWithContext(s.t.Context(), http.MethodGet, s.URL+path, nil)
		if err != nil {
			s.t.Fatalf("asking %s: %v", path, err)
		}
		resp, err := s.remote.Do(req)
		if err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			last = fmt.Sprintf("%d %s", resp.StatusCode, strings.TrimSpace(string(body)))
		} else {
			last = err.Error()
		}
		select {
		case <-deadline.C:
			s.t.Fatalf("the server did not become ready within %s: GET %s answered %s", readyTimeout, path, last)
		case <-tick.C:
		}
	}
}

// Client returns a browser for the running server: every request goes over
// the socket, through the whole stack the binary serves, and the cookies the
// server sets are kept as Client keeps them.
func (s *Server) Client() *Client {
	return &Client{t: s.t, base: s.URL, remote: s.remote}
}

// Route builds the path of the application's route called name, filling the
// parameters in order, and fails the test when there is no such route or the
// parameters do not fit it. Prefix it with URL for an absolute address.
func (s *Server) Route(name string, params ...string) string {
	s.t.Helper()
	path, err := s.routes.Route(name, params...)
	if err != nil {
		s.t.Fatalf("%v", err)
	}
	return path
}

// Handler is the application's handler, the one the server listens with, for
// a test that drives a request in process with the cookies As handed back.
func (s *Server) Handler() http.Handler { return s.handler }

// Resolve builds the path of the application's route called name like Route,
// taking the parameters as any value and printing each with fmt, and returns
// the error instead of failing the test. It is the browser.Resolver a page
// visits route names with: browser.Routes(server.Resolve).
func (s *Server) Resolve(name string, params ...any) (string, error) {
	values := make([]string, len(params))
	for i, p := range params {
		values[i] = fmt.Sprint(p)
	}
	return s.routes.Route(name, values...)
}

// DB is the test's database, for arranging what a request will find and for
// the database assertions.
func (s *Server) DB() *database.DB { return s.env.DB }

// migrationName identifies a migration function for the template it builds.
// Two calls passing the same function share a template; a closure is named by
// where it was written.
func migrationName(fn func(ctx context.Context, env Env) error) string {
	pc := reflect.ValueOf(fn).Pointer()
	if f := runtime.FuncForPC(pc); f != nil {
		return f.Name()
	}
	return fmt.Sprintf("%#x", pc)
}
