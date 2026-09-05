package feature

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/framework/foundation/bootstrap"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/database/migrations"
	"github.com/arandu-io/hesape/encryption"
)

// stub is a module with every optional interface, so one type covers boot,
// health, readiness, migrations and shutdown.
type stub struct {
	name       string
	bootErr    error
	healthErr  error
	readyErr   error
	booted     bool
	closed     bool
	closeOrder *[]string
}

// readyOnly implements Ready and not Health, which is the case the defaults have
// to answer rather than describe: it must be able to withhold traffic without
// being able to touch liveness.
type readyOnly struct {
	name     string
	readyErr error
}

func (m *readyOnly) Name() string { return m.name }

func (m *readyOnly) Routes(*fhttp.Router) {}

func (m *readyOnly) Ready(context.Context) error { return m.readyErr }

type reloadTagModule struct {
	name string
}

func (m *reloadTagModule) Name() string { return m.name }

func (m *reloadTagModule) Routes(r *fhttp.Router) {
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>application</body></html>"))
	})
}

func (m *reloadTagModule) ReloadTag(string) string {
	return `<script data-reload-owner="` + m.name + `"></script>`
}

func (s *stub) Name() string { return s.name }

func (s *stub) Routes(r *fhttp.Router) {
	r.Get("/"+s.name, func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func (s *stub) Boot(ctx context.Context) error {
	s.booted = true
	return s.bootErr
}

func (s *stub) Health(ctx context.Context) error { return s.healthErr }

func (s *stub) Ready(ctx context.Context) error { return s.readyErr }

func (s *stub) Close(ctx context.Context) error {
	s.closed = true
	if s.closeOrder != nil {
		*s.closeOrder = append(*s.closeOrder, s.name)
	}
	return nil
}

func (s *stub) Migrations() []foundation.Migration {
	return []foundation.Migration{stubMigration{name: s.name + "_0001"}}
}

// stubMigration is a migration that names itself and does nothing, which is all
// the collection test asks of one.
type stubMigration struct {
	migrations.BaseMigration
	name string
}

func (m stubMigration) GetName() string { return m.name }

func (stubMigration) Up(ctx context.Context, conn migrations.Connection) error {
	_, err := conn.Statement(ctx, "SELECT 1", nil)
	return err
}

// testConfig is what the Application reads and nothing else. It opens no
// connection, so there is no database here to configure.
func testConfig(env config.Env) bootstrap.Configuration {
	return bootstrap.Configuration{
		App: config.App{
			Name:     "test",
			Env:      env,
			HTTPAddr: ":0",
			Key:      make([]byte, encryption.KeySize),
		},
		Observability: bootstrap.Observability{LogLevel: slog.LevelError},
	}
}

func TestBootRegistersModuleRoutes(t *testing.T) {
	k := foundation.New(testConfig(config.EnvProd)).Register(&stub{name: "billing"})

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
	k := foundation.New(testConfig(config.EnvProd)).
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
	k := foundation.New(testConfig(config.EnvProd)).Register(&stub{name: ""})

	if err := k.Boot(context.Background()); err == nil {
		t.Fatal("a module without a name was accepted")
	}
}

// TestBootFailsFast is the no-degraded-mode rule: a module that cannot boot stops
// the process, rather than serving a subset of the application.
func TestBootFailsFast(t *testing.T) {
	failing := &stub{name: "broken", bootErr: errors.New("no connection")}
	k := foundation.New(testConfig(config.EnvProd)).Register(failing)

	err := k.Boot(context.Background())
	if err == nil {
		t.Fatal("Boot succeeded with a failing module")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("the error must name the module, got: %v", err)
	}
}

func TestBootTwiceIsRejected(t *testing.T) {
	k := foundation.New(testConfig(config.EnvProd)).Register(&stub{name: "a"})
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	if err := k.Boot(context.Background()); err == nil {
		t.Fatal("a second Boot was accepted: routes would be registered twice")
	}
}

func TestHealthReportsTheFailingModule(t *testing.T) {
	broken := &stub{name: "billing", healthErr: errors.New("database is away")}
	k := foundation.New(testConfig(config.EnvProd)).Register(&stub{name: "auth"}, broken)
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
	k := foundation.New(testConfig(config.EnvProd)).Register(&stub{name: "auth"})
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	rec := httptest.NewRecorder()
	k.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_arandu/health", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
}

// TestReadinessFailsWhileLivenessPassesOnTheSameApplication is the assertion the
// split exists for, and it is deliberately not a check that both routes are
// mounted.
//
// One endpoint used to answer both questions, so a module reporting a condition
// that stops traffic -- an outbox minutes behind, a shared cache that cannot be
// reached -- returned 503 to whichever probe was pointed at it. An orchestrator
// wired to it for liveness kills the process, and a restart drains no outbox and
// brings no cache back: it throws away the warm state and starts the same
// backlog again on a colder instance. Two routes that both answered 503 would
// reproduce that exactly while looking fixed.
//
// So what is measured is the disagreement: one application, one moment, and the
// two endpoints giving opposite answers. The three cases are the three shapes a
// module can take -- Ready without Health, Health without a readiness opinion,
// and both together with only the readiness half failing. The last one is the
// footgun check: implementing Ready must not quietly retire the Health check
// beside it, and the reverse case is the row above it.
func TestReadinessFailsWhileLivenessPassesOnTheSameApplication(t *testing.T) {
	for _, tc := range []struct {
		name   string
		module foundation.Module
		wants  string
	}{
		{
			name:   "a module that implements Ready alone and is not ready",
			module: &readyOnly{name: "relay", readyErr: errors.New("outbox is five minutes behind")},
			wants:  "relay",
		},
		{
			name:   "a module that implements Health alone and is unhealthy",
			module: &stub{name: "cache", healthErr: errors.New("shared cache is unreachable")},
			wants:  "cache",
		},
		{
			name:   "a module that is healthy and still not ready",
			module: &stub{name: "warming", readyErr: errors.New("cache is still warming")},
			wants:  "warming",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := foundation.New(testConfig(config.EnvProd)).Register(tc.module)
			if err := k.Boot(context.Background()); err != nil {
				t.Fatalf("Boot: %v", err)
			}
			handler := k.Handler()

			ready := httptest.NewRecorder()
			handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/_arandu/health", nil))
			if ready.Code != http.StatusServiceUnavailable {
				t.Errorf("readiness = %d, want 503: traffic keeps arriving at an instance that cannot serve it", ready.Code)
			}
			if !strings.Contains(ready.Body.String(), tc.wants) {
				t.Errorf("readiness body = %q, want it to name %q", ready.Body.String(), tc.wants)
			}

			live := httptest.NewRecorder()
			handler.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/_arandu/live", nil))
			if live.Code != http.StatusOK {
				t.Fatalf("liveness = %d, want 200: the process is killed over a condition a restart does not fix", live.Code)
			}
		})
	}
}

// TestBothChecksOfOneModuleAreConsulted holds the rule that a module implementing
// both interfaces passes both.
//
// Ready adds a condition to Health rather than replacing it. Were Ready to
// override, a module that gained a warmup check would silently stop reporting the
// database check it already had, and the author who added the method is the last
// person who would notice.
func TestBothChecksOfOneModuleAreConsulted(t *testing.T) {
	both := &stub{
		name:      "billing",
		healthErr: errors.New("database is away"),
		readyErr:  nil,
	}
	k := foundation.New(testConfig(config.EnvProd)).Register(both)
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	rec := httptest.NewRecorder()
	k.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_arandu/health", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness = %d, want 503: a passing Ready hid a failing Health", rec.Code)
	}
}

// TestLivenessConsultsNoModule is the other half of the design written down as an
// assertion: the liveness answer does not depend on anything a module reports.
//
// Every module here is failing every check it has, and the process still declines
// to ask to be restarted -- because none of these conditions is repaired by a
// restart. If a future change wires a module into the liveness handler, this is
// what stops it.
func TestLivenessConsultsNoModule(t *testing.T) {
	k := foundation.New(testConfig(config.EnvProd)).Register(
		&stub{name: "billing", healthErr: errors.New("database is away")},
		&stub{name: "search", readyErr: errors.New("index is rebuilding")},
		&readyOnly{name: "relay", readyErr: errors.New("outbox is five minutes behind")},
	)
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	rec := httptest.NewRecorder()
	k.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_arandu/live", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("liveness = %d, body = %q: a module reached the liveness answer", rec.Code, rec.Body.String())
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
		k := foundation.New(testConfig(env))
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

func TestDevelopmentReloadTagBelongsToItsApplication(t *testing.T) {
	first := foundation.New(testConfig(config.EnvDev)).
		Register(&reloadTagModule{name: "first"})
	if err := first.Boot(context.Background()); err != nil {
		t.Fatalf("Boot first application: %v", err)
	}
	firstHandler := first.Handler()

	second := foundation.New(testConfig(config.EnvDev)).
		Register(&reloadTagModule{name: "second"})
	if err := second.Boot(context.Background()); err != nil {
		t.Fatalf("Boot second application: %v", err)
	}

	rec := httptest.NewRecorder()
	firstHandler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `data-reload-owner="first"`) {
		t.Errorf("the first application response does not contain its reload tag: %q", body)
	}
	if strings.Contains(body, `data-reload-owner="second"`) {
		t.Errorf("the first application response contains the second application's reload tag: %q", body)
	}
}

func TestMigrationsAreCollectedInRegistrationOrder(t *testing.T) {
	k := foundation.New(testConfig(config.EnvProd)).
		Register(&stub{name: "auth"}, &stub{name: "billing"})

	collected := k.Migrations()

	if len(collected) != 2 {
		t.Fatalf("collected %d migrations, want 2", len(collected))
	}
	if collected[0].GetName() != "auth_0001" || collected[1].GetName() != "billing_0001" {
		t.Fatalf("order = %s, %s: registration order decides collection order",
			collected[0].GetName(), collected[1].GetName())
	}
}

func TestPublicationsAreCollectedInRegistrationOrder(t *testing.T) {
	k := foundation.New(testConfig(config.EnvProd)).
		Register(
			&publisher{name: "ui", tag: foundation.PublishView, to: "resources/views"},
			&publisher{name: "billing", tag: foundation.PublishMigration, to: "database/migrations"},
			&stub{name: "auth"},
		)

	collected, err := k.Publications()
	if err != nil {
		t.Fatalf("Publications: %v", err)
	}

	if len(collected) != 2 {
		t.Fatalf("collected %d publications, want 2: a module that publishes nothing contributes nothing",
			len(collected))
	}
	if collected[0].To != "resources/views" || collected[1].To != "database/migrations" {
		t.Fatalf("order = %s, %s: registration order decides collection order",
			collected[0].To, collected[1].To)
	}
	if collected[0].Tag != foundation.PublishView || collected[1].Tag != foundation.PublishMigration {
		t.Fatalf("tags = %s, %s: the tag a module declared is the tag collected",
			collected[0].Tag, collected[1].Tag)
	}
}

// TestPublicationsRefusesAModuleThatDeclaresAnUnknownTag: the closed set is
// enforced where every caller passes, and the refusal names the module so the
// person publishing knows which one to open.
func TestPublicationsRefusesAModuleThatDeclaresAnUnknownTag(t *testing.T) {
	k := foundation.New(testConfig(config.EnvProd)).
		Register(&publisher{name: "panelkit", tag: foundation.PublishTag("panel"), to: "resources/panels"})

	collected, err := k.Publications()
	if err == nil {
		t.Fatalf("a tag from outside the six was collected: %v", collected)
	}
	if !strings.Contains(err.Error(), "panelkit") {
		t.Errorf("the refusal does not name the module: %v", err)
	}
	if collected != nil {
		t.Errorf("a refused collection still answered %v", collected)
	}
}

// publisher is a module whose only optional interface is Publishable.
type publisher struct {
	name string
	tag  foundation.PublishTag
	to   string
}

func (m *publisher) Name() string { return m.name }

func (m *publisher) Routes(*fhttp.Router) {}

func (m *publisher) Publishes() []foundation.Publication {
	return []foundation.Publication{{
		Tag:   m.tag,
		Files: fstest.MapFS{"page.kyse.go": &fstest.MapFile{Data: []byte("package views\n")}},
		To:    m.to,
	}}
}

// TestShutdownClosesInReverseOrder: a module registered later may depend on an
// earlier one, so closing forward would tear down a dependency still in use.
func TestShutdownClosesInReverseOrder(t *testing.T) {
	var order []string
	first := &stub{name: "database", closeOrder: &order}
	second := &stub{name: "cache", closeOrder: &order}
	k := foundation.New(testConfig(config.EnvProd)).Register(first, second)
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
	k := foundation.New(testConfig(config.EnvProd))

	if err := k.Run(context.Background()); err == nil {
		t.Fatal("Run before Boot was accepted: it would serve no routes at all")
	}
}

func TestFormatRoutesGroupsByModule(t *testing.T) {
	k := foundation.New(testConfig(config.EnvProd)).
		Register(&stub{name: "billing"}, &stub{name: "auth"})
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	out := foundation.FormatRoutes(k.Routes())

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

func (p *loggerProbe) Routes(r *fhttp.Router) {
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
	k := foundation.New(testConfig(config.EnvProd)).Register(probe)
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
	if got := foundation.FormatRoutes(nil); !strings.Contains(got, "no routes") {
		t.Fatalf("FormatRoutes(nil) = %q", got)
	}
}

// TestTheConsoleIsClosedInProduction guards a hole of the worst kind: one where
// the code reads as if it were closed.
//
// The recorder exists whenever a tracing secret is configured -- that is what
// makes production tracing possible at all. Mounting the routes from the same
// condition, with the secret checked only by the middleware that decides whether
// to RECORD, lets anyone GET /_arandu/debug with no session, no cookie and no
// header, and read the buffer: SQL with its bound arguments, dumps, event
// payloads, across every tenant.
func TestTheConsoleIsClosedInProduction(t *testing.T) {
	cfg := testConfig(config.EnvProd)
	cfg.Observability.TracingSecret = "s3cret-operator-only"
	k := foundation.New(cfg)
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { _ = k.Shutdown() })
	handler := k.Handler()

	// Anonymous, which is what an internet scan looks like.
	for _, path := range []string{
		observability.ConsolePath,
		observability.ConsolePath + "/anything",
		observability.ConsolePath + "?format=json",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s answered %d to an anonymous request in production", path, rec.Code)
		}
	}

	// A wrong secret behaves exactly like none, or the endpoint becomes an
	// oracle for guessing it.
	wrong := httptest.NewRequest(http.MethodGet, observability.ConsolePath, nil)
	wrong.Header.Set(observability.TracingHeader, "not-the-secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, wrong)
	if rec.Code != http.StatusNotFound {
		t.Errorf("a wrong secret answered %d", rec.Code)
	}

	// The operator, with the secret, still gets in -- otherwise the feature is
	// gone rather than fixed.
	right := httptest.NewRequest(http.MethodGet, observability.ConsolePath, nil)
	right.Header.Set(observability.TracingHeader, "s3cret-operator-only")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, right)
	if rec.Code != http.StatusOK {
		t.Errorf("the operator with the secret got %d", rec.Code)
	}

	// And health stays open, because a load balancer has no header.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_arandu/health", nil))
	if rec.Code == http.StatusNotFound {
		t.Error("the health check was gated too")
	}
}

// TestAnEmptySecretDoesNotOpenTheConsole: an empty secret is the zero value of
// the configuration, and treating it as "no gate" would open the console for
// every application that never set one.
func TestAnEmptySecretDoesNotOpenTheConsole(t *testing.T) {
	k := foundation.New(testConfig(config.EnvProd))
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { _ = k.Shutdown() })

	for _, header := range []string{"", "anything"} {
		r := httptest.NewRequest(http.MethodGet, observability.ConsolePath, nil)
		if header != "" {
			r.Header.Set(observability.TracingHeader, header)
		}
		rec := httptest.NewRecorder()
		k.Handler().ServeHTTP(rec, r)
		if rec.Code != http.StatusNotFound {
			t.Errorf("with no secret configured and header %q, the console answered %d", header, rec.Code)
		}
	}
}

// backgroundSpy records whether its loop was started. Atomic, because Start
// runs on the goroutine that serves and the test reads from its own.
type backgroundSpy struct {
	booted  atomic.Bool
	started atomic.Bool
}

func (*backgroundSpy) Name() string         { return "spy" }
func (*backgroundSpy) Routes(*fhttp.Router) {}

func (b *backgroundSpy) Boot(context.Context) error { b.booted.Store(true); return nil }

func (b *backgroundSpy) Start(context.Context) error { b.started.Store(true); return nil }

// TestBootDoesNotStartBackgroundLoops keeps Boot free of the loops.
//
// Every command boots -- `aru work`, `aru routes`, `aru schedule:list`,
// `aru schedule:run`. If the scheduler and the relay started their loops in
// Boot, every worker replica would run a scheduler of its own, and
// `aru schedule:run` -- the command for running exactly one task by hand --
// would start the loop that runs all of them. The lock makes that harmless, not
// correct.
func TestBootDoesNotStartBackgroundLoops(t *testing.T) {
	spy := &backgroundSpy{}
	k := foundation.New(testConfig(config.EnvProd)).Register(spy)

	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if !spy.booted.Load() {
		t.Error("the module was not booted")
	}
	if spy.started.Load() {
		t.Error("Boot started the background loop; only Run may do that")
	}
}

// TestRunStartsBackgroundLoops is the other half: the process that serves does
// run them, or a scheduled task silently never happens.
func TestRunStartsBackgroundLoops(t *testing.T) {
	spy := &backgroundSpy{}
	cfg := testConfig(config.EnvProd)
	cfg.App.HTTPAddr = "127.0.0.1:0"
	k := foundation.New(cfg).Register(spy)

	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- k.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for !spy.started.Load() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	stop()
	<-done

	if !spy.started.Load() {
		t.Fatal("Run did not start the background loop")
	}
}

// TestTheFrameworksOwnRoutesDoNotSpendTheApplicationsBudget.
//
// The development reload asks once a second which process is answering. That ran
// through the rate limit an application mounts for its own visitors -- sixty of
// a three-hundred-per-minute budget per open tab, shared between tabs because
// the key falls back to the client address for a request carrying no session.
//
// Ordinary browsing with a couple of tabs open therefore answered
// "too many requests: wait 32 seconds and try again", in plain text, on a page
// nobody had hammered. Reported from a browser, on /auth/login, while navigating
// normally.
func TestTheFrameworksOwnRoutesDoNotSpendTheApplicationsBudget(t *testing.T) {
	var counted int
	counting := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counted++
			next.ServeHTTP(w, r)
		})
	}

	k := foundation.New(testConfig(config.EnvDev))
	k.Use(counting)
	if err := k.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler := k.Handler()

	for _, path := range []string{
		"/_arandu/health",
		"/_arandu/live",
		"/_arandu/reload",
		observability.ConsolePath,
	} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	if counted != 0 {
		t.Errorf("%d of the framework's own requests were charged to the application", counted)
	}

	// And the application's own traffic still is.
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if counted != 1 {
		t.Errorf("the application's middleware ran %d times for one request to /, want 1", counted)
	}
}

// hangingModule is a dependency that stopped answering rather than one that
// failed: its check returns only once the context it was given is done.
type hangingModule struct{ name string }

func (m *hangingModule) Name() string { return m.name }

func (*hangingModule) Routes(*fhttp.Router) {}

func (*hangingModule) Health(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// TestReadinessDoesNotWaitForACheckThatStoppedAnswering.
//
// A dependency that hangs is the failure a check cannot report by returning: it
// never returns. Without a bound of its own the handler waits exactly as long as
// the dependency does, whoever is probing gives up first, and the record is an
// unanswered request -- which is what a dead process also looks like, and the
// two call for opposite actions. Bounded, the same outage is a 503 naming the
// module, which withholds traffic and leaves the process alone.
//
// The wait here is the regression guard, not the assertion: it exists so that a
// handler which reverts to waiting forever fails this test instead of hanging
// the suite.
func TestReadinessDoesNotWaitForACheckThatStoppedAnswering(t *testing.T) {
	k := foundation.New(testConfig(config.EnvProd)).Register(&hangingModule{name: "billing"})
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	handler := k.Handler()

	// Liveness first, and it must answer while the dependency is still hanging:
	// a wedged dependency is the case where restarting is most tempting and least
	// useful, and an endpoint that blocks on it asks for exactly that restart.
	live := httptest.NewRecorder()
	handler.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/_arandu/live", nil))
	if live.Code != http.StatusOK {
		t.Fatalf("liveness = %d while a dependency hangs, want 200", live.Code)
	}

	rec := httptest.NewRecorder()
	answered := make(chan struct{})
	go func() {
		defer close(answered)
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_arandu/health", nil))
	}()

	select {
	case <-answered:
	case <-time.After(30 * time.Second):
		t.Fatal("readiness never answered: a dependency that hangs holds the endpoint open, so the probe records a timeout instead of the 503 that names the module")
	}

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "billing") {
		t.Errorf("body = %q, want it to name the module that stopped answering", rec.Body.String())
	}
}

// probeSecrets is what a driver puts in an error and what an anonymous caller
// must not be able to read back off a probe.
var probeSecrets = []string{
	"db-primary.internal",
	"5432",
	"billing_production",
	"s3cret-password",
	"PostgreSQL 16.2",
}

// leakyModule fails the way a real driver fails: naming the host, the port, the
// database, the credential and the server version.
type leakyModule struct{ name string }

func (m *leakyModule) Name() string { return m.name }

func (*leakyModule) Routes(*fhttp.Router) {}

func (*leakyModule) Health(context.Context) error {
	return errors.New(`dial tcp db-primary.internal:5432: connect: connection refused ` +
		`(database "billing_production", user "arandu", password "s3cret-password", server "PostgreSQL 16.2")`)
}

// TestTheProbesRevealNothingButTheModuleName.
//
// Both probes are reachable with no session, no cookie and no header -- a load
// balancer carries none of those, which is why nothing gates them -- so whatever
// they write is what an anonymous caller learns. The error a check returns is
// the tempting thing to write, and it is a map of the infrastructure: the host,
// the port, the database name, sometimes the credential and the version.
//
// So the check is the whole response and not the body alone: a header is as
// readable as a body, and something copied into one is copied just as easily
// into the other.
func TestTheProbesRevealNothingButTheModuleName(t *testing.T) {
	k := foundation.New(testConfig(config.EnvProd)).Register(&leakyModule{name: "billing"})
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	handler := k.Handler()

	for _, path := range []string{"/_arandu/health", "/_arandu/live"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		var response strings.Builder
		response.WriteString(rec.Body.String())
		for name, values := range rec.Header() {
			response.WriteString(" " + name + ": " + strings.Join(values, " "))
		}

		for _, secret := range probeSecrets {
			if strings.Contains(response.String(), secret) {
				t.Errorf("%s answered with %q, which an unauthenticated caller must not learn: %q",
					path, secret, response.String())
			}
		}
	}

	// And the module name still gets through, or this test would pass on an
	// endpoint that answers nothing at all.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_arandu/health", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "billing") {
		t.Fatalf("status = %d, body = %q: the probe must still name the failing module", rec.Code, rec.Body.String())
	}
}
