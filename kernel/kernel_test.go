package kernel_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/config"
	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/kernel"
	"github.com/arandu-io/framework/observability"
)

// stub is a module with every optional interface, so one type covers boot,
// health, migrations and shutdown.
type stub struct {
	name       string
	bootErr    error
	healthErr  error
	booted     bool
	closed     bool
	closeOrder *[]string
}

func (s *stub) Name() string { return s.name }

func (s *stub) Routes(r *httpx.Router) {
	r.Get("/"+s.name, func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func (s *stub) Boot(ctx context.Context) error {
	s.booted = true
	return s.bootErr
}

func (s *stub) Health(ctx context.Context) error { return s.healthErr }

func (s *stub) Close(ctx context.Context) error {
	s.closed = true
	if s.closeOrder != nil {
		*s.closeOrder = append(*s.closeOrder, s.name)
	}
	return nil
}

func (s *stub) Migrations() []kernel.Migration {
	return []kernel.Migration{{ID: s.name + "_0001", Up: "SELECT 1"}}
}

func testConfig(env config.Env) config.Config {
	return config.Config{
		AppName:  "test",
		Env:      env,
		HTTPAddr: ":0",
		AppKey:   make([]byte, config.AppKeyLen),
		Database: config.DatabaseConfig{
			Connection: data.DialectSQLite,
			Database:   "database/database.sqlite",
		},
		LogLevel: slog.LevelError,
	}
}

func TestBootRegistersModuleRoutes(t *testing.T) {
	k := kernel.New(testConfig(config.EnvProd)).Register(&stub{name: "billing"})

	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	rec := httptest.NewRecorder()
	k.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/billing", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestBootRejectsDuplicateModule catches the copy-paste in the wiring file, where
// two entries would otherwise fight over the same routes.
func TestBootRejectsDuplicateModule(t *testing.T) {
	k := kernel.New(testConfig(config.EnvProd)).
		Register(&stub{name: "billing"}, &stub{name: "billing"})

	err := k.Boot(context.Background())
	if err == nil {
		t.Fatal("two modules with the same name were accepted")
	}
	if !strings.Contains(err.Error(), "registered twice") {
		t.Errorf("error = %v", err)
	}
}

func TestBootRejectsEmptyModuleName(t *testing.T) {
	k := kernel.New(testConfig(config.EnvProd)).Register(&stub{name: ""})

	if err := k.Boot(context.Background()); err == nil {
		t.Fatal("a module without a name was accepted")
	}
}

// TestBootFailsFast is the no-degraded-mode rule: a module that cannot boot stops
// the process, rather than serving a subset of the application.
func TestBootFailsFast(t *testing.T) {
	failing := &stub{name: "broken", bootErr: errors.New("no connection")}
	k := kernel.New(testConfig(config.EnvProd)).Register(failing)

	err := k.Boot(context.Background())
	if err == nil {
		t.Fatal("Boot succeeded with a failing module")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("the error must name the module, got: %v", err)
	}
}

func TestBootTwiceIsRejected(t *testing.T) {
	k := kernel.New(testConfig(config.EnvProd)).Register(&stub{name: "a"})
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	if err := k.Boot(context.Background()); err == nil {
		t.Fatal("a second Boot was accepted: routes would be registered twice")
	}
}

func TestHealthReportsTheFailingModule(t *testing.T) {
	broken := &stub{name: "billing", healthErr: errors.New("database is away")}
	k := kernel.New(testConfig(config.EnvProd)).Register(&stub{name: "auth"}, broken)
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	rec := httptest.NewRecorder()
	k.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_arandu/health", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	// A probe that only says "unhealthy" costs an hour of guessing.
	if !strings.Contains(rec.Body.String(), "billing") {
		t.Errorf("the body must name the failing module, got %q", rec.Body.String())
	}
}

func TestHealthIsOKWhenEveryModuleIsHealthy(t *testing.T) {
	k := kernel.New(testConfig(config.EnvProd)).Register(&stub{name: "auth"})
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	rec := httptest.NewRecorder()
	k.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_arandu/health", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
}

// TestDebugConsoleIsDevelopmentOnly enforces the absolute rule of the
// observability package: no debug surface outside development.
func TestDebugConsoleIsDevelopmentOnly(t *testing.T) {
	for env, want := range map[config.Env]int{
		config.EnvDev:     http.StatusOK,
		config.EnvStaging: http.StatusNotFound,
		config.EnvProd:    http.StatusNotFound,
	} {
		k := kernel.New(testConfig(env))
		if err := k.Boot(context.Background()); err != nil {
			t.Fatalf("Boot in %s: %v", env, err)
		}

		rec := httptest.NewRecorder()
		k.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_arandu/debug", nil))
		if rec.Code != want {
			t.Errorf("/_arandu/debug in %s = %d, want %d", env, rec.Code, want)
		}
	}
}

func TestMigrationsAreCollectedInRegistrationOrder(t *testing.T) {
	k := kernel.New(testConfig(config.EnvProd)).
		Register(&stub{name: "auth"}, &stub{name: "billing"})

	migrations := k.Migrations()

	if len(migrations) != 2 {
		t.Fatalf("collected %d migrations, want 2", len(migrations))
	}
	if migrations[0].ID != "auth_0001" || migrations[1].ID != "billing_0001" {
		t.Fatalf("order = %s, %s: registration order decides schema order", migrations[0].ID, migrations[1].ID)
	}
}

// TestShutdownClosesInReverseOrder: a module registered later may depend on an
// earlier one, so closing forward would tear down a dependency still in use.
func TestShutdownClosesInReverseOrder(t *testing.T) {
	var order []string
	first := &stub{name: "database", closeOrder: &order}
	second := &stub{name: "cache", closeOrder: &order}
	k := kernel.New(testConfig(config.EnvProd)).Register(first, second)
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	if err := k.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if len(order) != 2 || order[0] != "cache" || order[1] != "database" {
		t.Fatalf("close order = %v, want cache then database", order)
	}
}

func TestRunBeforeBootIsRejected(t *testing.T) {
	k := kernel.New(testConfig(config.EnvProd))

	if err := k.Run(context.Background()); err == nil {
		t.Fatal("Run before Boot was accepted: it would serve no routes at all")
	}
}

func TestFormatRoutesGroupsByModule(t *testing.T) {
	k := kernel.New(testConfig(config.EnvProd)).
		Register(&stub{name: "billing"}, &stub{name: "auth"})
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	out := kernel.FormatRoutes(k.Routes())

	if !strings.Contains(out, "auth\n  GET     /auth") {
		t.Errorf("routes are not grouped under their module:\n%s", out)
	}
	if strings.Index(out, "auth") > strings.Index(out, "billing") {
		t.Errorf("modules must be listed in a stable order:\n%s", out)
	}
	if !strings.Contains(out, "/_arandu/health") {
		t.Errorf("the framework's own routes must be listed too:\n%s", out)
	}
}

// loggerProbe records the logger a handler sees.
type loggerProbe struct {
	seen *slog.Logger
}

func (p *loggerProbe) Name() string { return "probe" }

func (p *loggerProbe) Routes(r *httpx.Router) {
	r.Get("/probe", func(w http.ResponseWriter, req *http.Request) {
		p.seen = observability.Log(req.Context())
	})
}

// TestRequestLoggerIsTheApplicationLogger: without the root logger installed at
// the top of the pipeline, Log(ctx) falls back to slog.Default(), which ignores
// the configured handler and level. In production that means request lines in the
// wrong format, and it is only noticed when someone goes looking for a log line
// that is not there.
func TestRequestLoggerIsTheApplicationLogger(t *testing.T) {
	probe := &loggerProbe{}
	k := kernel.New(testConfig(config.EnvProd)).Register(probe)
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	k.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/probe", nil))

	if probe.seen == nil {
		t.Fatal("the handler did not run")
	}
	if probe.seen != k.Logger() {
		t.Fatal("the handler received a different logger than the application's, so the configured handler is bypassed")
	}
	if probe.seen == slog.Default() {
		t.Fatal("the handler received slog.Default()")
	}
}

func TestFormatRoutesWithoutRoutes(t *testing.T) {
	if got := kernel.FormatRoutes(nil); !strings.Contains(got, "no routes") {
		t.Fatalf("FormatRoutes(nil) = %q", got)
	}
}
