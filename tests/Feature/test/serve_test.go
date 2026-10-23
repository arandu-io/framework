package feature

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/arandu-io/framework/test"
	"github.com/arandu-io/hesape/database"
	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/routing"
)

// The core links no SQLite driver -- the real one lives in its own module, and
// the tests that prove two tests' rows apart run there. What these tests prove
// is the server's life, so the database under them is a driver that opens and
// answers nothing.
func init() {
	sql.Register("arandutest-serve", inertDriver{})
	database.Register(inertConnector{})
}

type inertConnector struct{}

func (inertConnector) Dialect() database.Dialect { return database.DialectSQLite }
func (inertConnector) DriverName() string        { return "arandutest-serve" }

type inertDriver struct{}

func (inertDriver) Open(string) (driver.Conn, error) { return inertConn{}, nil }

type inertConn struct{}

func (inertConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("inert: no statements") }
func (inertConn) Close() error                        { return nil }
func (inertConn) Begin() (driver.Tx, error)           { return nil, errors.New("inert: no transactions") }
func (inertConn) Ping(context.Context) error          { return nil }

func noMigration(context.Context, test.Env) error { return nil }

// boot answers the router as the application, with the routes it registered.
func boot(r *routing.Router, sessions *hhttp.SessionStore) test.Boot {
	return func(context.Context, test.Env) (test.Application, error) {
		return test.Application{Handler: r, Routes: r.Routes(), Sessions: sessions}, nil
	}
}

// The server's client goes over the socket: the request reaches the handler
// with the address the server listens on as its host, which a request built in
// process never carries.
func TestServeAnswersOverTheSocket(t *testing.T) {
	r := routing.NewRouter()
	r.Get("/where", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("host " + req.Host))
	}))

	server := test.Serve(t, boot(r, nil), test.SQLite(), test.Migrate(noMigration))

	if !strings.HasPrefix(server.URL, "http://127.0.0.1:") {
		t.Fatalf("the server listens on %q, want 127.0.0.1", server.URL)
	}
	server.Client().Get("/where").AssertOk().AssertSee("host " + strings.TrimPrefix(server.URL, "http://"))
}

// The schema exists before the application boots, as a deploy migrates before
// it starts the binary, and the seeders write after the boot -- so they can
// reach the application's services -- and before the first request.
func TestServeMigratesBeforeTheBootAndSeedsBeforeTheFirstRequest(t *testing.T) {
	var mu sync.Mutex
	var steps []string
	step := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		steps = append(steps, s)
	}

	r := routing.NewRouter()
	r.Get("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { step("request") }))

	var booted test.Env
	server := test.Serve(t,
		func(_ context.Context, env test.Env) (test.Application, error) {
			step("boot")
			booted = env
			return test.Application{Handler: r}, nil
		},
		test.SQLite(),
		test.Migrate(func(_ context.Context, env test.Env) error {
			if env.DB == nil || env.DatabaseURL == "" {
				t.Errorf("the migration was handed no database: %+v", env)
			}
			step("migrate")
			return nil
		}),
		test.Seed(func(context.Context, test.Env) error { step("seed 1"); return nil }),
		test.Seed(func(context.Context, test.Env) error { step("seed 2"); return nil }),
	)
	server.Client().Get("/").AssertOk()

	if got := strings.Join(steps, ", "); got != "migrate, boot, seed 1, seed 2, request" {
		t.Errorf("Serve ran %s", got)
	}
	if booted.URL != server.URL || booted.DB != server.DB() || !strings.HasPrefix(booted.DatabaseURL, "sqlite://") {
		t.Errorf("the boot was handed %+v, the server is %s", booted, server.URL)
	}
}

// When the test ends the server stops answering before the application shuts
// down, so nothing reaches an application that is stopping.
func TestServeStopsWhenTheTestEnds(t *testing.T) {
	var address string
	var shutdowns atomic.Int32
	answeredDuringShutdown := make(chan bool, 1)

	t.Run("serving", func(t *testing.T) {
		r := routing.NewRouter()
		r.Get("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("up")) }))
		server := test.Serve(t,
			func(context.Context, test.Env) (test.Application, error) {
				return test.Application{Handler: r, Shutdown: func() error {
					shutdowns.Add(1)
					conn, err := net.Dial("tcp", address)
					if err == nil {
						_ = conn.Close()
					}
					answeredDuringShutdown <- err == nil
					return nil
				}}, nil
			},
			test.SQLite(), test.Migrate(noMigration))
		address = strings.TrimPrefix(server.URL, "http://")
		server.Client().Get("/").AssertSee("up")
	})

	if shutdowns.Load() != 1 {
		t.Fatalf("the application was shut down %d times, want once", shutdowns.Load())
	}
	if <-answeredDuringShutdown {
		t.Error("the server still accepted connections while the application shut down")
	}
	if conn, err := net.Dial("tcp", address); err == nil {
		_ = conn.Close()
		t.Errorf("%s still accepts connections after the test ended", address)
	}
}

// Serve hands the test a server that is ready, not one that is listening: the
// ready route is asked until it answers 200.
func TestServeWaitsForTheReadyRoute(t *testing.T) {
	var asked atomic.Int32
	r := routing.NewRouter()
	r.Get("/_arandu/health", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if asked.Add(1) < 4 {
			http.Error(w, "module database unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})).Name("arandu.health")

	test.Serve(t, boot(r, nil), test.SQLite(), test.Migrate(noMigration))

	if got := asked.Load(); got != 4 {
		t.Errorf("the ready route was asked %d times before Serve returned, want 4", got)
	}
}

// ReadyRoute names another route to wait on, for an application whose
// readiness answers somewhere else.
func TestReadyRouteNamesTheRouteToWaitOn(t *testing.T) {
	var asked atomic.Int32
	r := routing.NewRouter()
	r.Get("/up", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if asked.Add(1) < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})).Name("up")

	test.Serve(t, boot(r, nil), test.SQLite(), test.Migrate(noMigration), test.ReadyRoute("up"))

	if got := asked.Load(); got != 2 {
		t.Errorf("the ready route was asked %d times, want 2", got)
	}
}

// A route is reached by its name, so a test keeps working when the address
// moves and stops when the name is wrong.
func TestServerRouteBuildsTheAddressByName(t *testing.T) {
	r := routing.NewRouter()
	r.Get("/invoices/{invoice}", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("invoice " + req.PathValue("invoice")))
	})).Name("invoices.show")

	server := test.Serve(t, boot(r, nil), test.SQLite(), test.Migrate(noMigration))

	if got := server.Route("invoices.show", "42"); got != "/invoices/42" {
		t.Fatalf(`Route("invoices.show", "42") = %q`, got)
	}
	server.Client().Get(server.Route("invoices.show", "42")).AssertSee("invoice 42")
}

// The jar works over the socket as it does in process: a cookie the server set
// is sent back, and a redirect is answered rather than followed.
func TestTheServerClientKeepsCookiesAndDoesNotFollowRedirects(t *testing.T) {
	r := routing.NewRouter()
	r.Get("/set", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc", Path: "/"})
		http.Redirect(w, req, "/read", http.StatusFound)
	}))
	r.Get("/read", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		c, err := req.Cookie("session")
		if err != nil {
			_, _ = w.Write([]byte("carrying nothing"))
			return
		}
		_, _ = w.Write([]byte("carrying " + c.Value))
	}))

	server := test.Serve(t, boot(r, nil), test.SQLite(), test.Migrate(noMigration))
	client := server.Client()

	client.Get("/set").AssertStatus(http.StatusFound).AssertRedirect("/read")
	client.Get("/read").AssertSee("carrying abc")
	if cookies := client.Cookies(); len(cookies) != 1 || cookies[0].Value != "abc" {
		t.Errorf("the client holds %v", cookies)
	}
}

// Two servers in parallel tests are two ports and two databases.
func TestParallelServersAreApart(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	t.Run("group", func(t *testing.T) {
		for _, name := range []string{"a", "b"} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				var dbURL string
				server := test.Serve(t, func(_ context.Context, env test.Env) (test.Application, error) {
					dbURL = env.DatabaseURL
					return test.Application{Handler: routing.NewRouter()}, nil
				}, test.SQLite(), test.Migrate(noMigration))
				mu.Lock()
				seen = append(seen, server.URL, dbURL)
				mu.Unlock()
			})
		}
	})
	if len(seen) != 4 {
		t.Fatalf("collected %v", seen)
	}
	for i := range seen {
		for j := i + 1; j < len(seen); j++ {
			if seen[i] == seen[j] {
				t.Errorf("two parallel tests share %s", seen[i])
			}
		}
	}
}
