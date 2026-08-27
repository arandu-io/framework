// The Application. It was kernel.Kernel until this package landed, and
// github.com/arandu-io/framework/kernel is the old name pointing here.

package foundation

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/arandu-io/framework/foundation/bootstrap"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	internalroutes "github.com/arandu-io/framework/internal/routes"
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/routing"
)

// Limits of the HTTP server. They are set rather than left at the zero value
// because a server without timeouts is one slow client away from running out of
// file descriptors.
//
// readHeaderTimeout ends the header and readTimeout ends the body. Without the
// second one a request whose headers arrive promptly can be followed by a body
// delivered one byte at a time, and the handler waits for as long as the client
// cares to keep sending: the header deadline has already passed by then, and
// nothing replaces it.
//
// writeTimeout is a deadline on the whole response, counted from the moment the
// headers finished arriving, so it covers reading the body and running the
// handler as well as writing. That is why it is larger than readTimeout -- a
// value below it would make a slow upload fail on a write it had not reached
// yet, which reads as a broken handler rather than as a slow client.
//
// Being a deadline on the whole response makes it a ceiling on a streaming one
// too. A handler that means to hold the connection open lifts it for its own
// request:
//
//	http.NewResponseController(w).SetWriteDeadline(time.Time{})
//
// which reaches the connection through the Unwrap methods the response writers
// of this framework carry.
//
// maxHeaderBytes is a sixteenth of the net/http default of 1 MB, which nothing a
// browser sends approaches: cookies are capped at 4 KB each, and a handful of
// them beside an authorization header fit in a fraction of this. Naming it is
// the point -- how much memory an unauthenticated request can make the server
// buffer is a decision, and left unset it is net/http's rather than this one's.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 60 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 20 * time.Second
	maxHeaderBytes    = 64 << 10
)

// Application holds the composed application: configuration, modules, the global
// middleware pipeline and the router.
type Application struct {
	cfg      bootstrap.Configuration
	log      *slog.Logger
	router   *fhttp.Router
	modules  []Module
	pipeline []fhttp.Middleware
	srv      *http.Server

	// flash carries the messages of a rejected form across the redirect that
	// answers it. The Application builds it rather than the project, for the
	// reason the renderer is found rather than passed: it takes no decision --
	// the key and the environment are already here -- and a wiring line an
	// application can leave out is one an application leaves out, with a form
	// that comes back blank as the only symptom.
	flash *security.Flash

	booted bool
	// reloadTag comes from this application's view module. Keeping it on the
	// application prevents another application in the same process from changing
	// the markup injected into responses already served by this one.
	reloadTag []byte
	// recorder is the ring buffer behind the console. It exists in development
	// and under a tracing secret, and is nil otherwise -- which is what makes
	// the console cost nothing in production rather than cost little.
	recorder *observability.Recorder
	// gauges is the current value of the numbers the process owns. Unlike the
	// recorder it always exists, because what it costs does not grow with
	// traffic: it holds one int64 per metric per tenant and replaces it in
	// place, so a process serving for a month holds what it held on the first
	// request.
	gauges *observability.Gauges
}

// New assembles the Application. It opens no connection and listens on no port
// -- that is Boot and Run.
func New(cfg bootstrap.Configuration) *Application {
	log := observability.NewLogger(string(cfg.App.Env), cfg.Observability.LogLevel)

	a := &Application{
		cfg: cfg,
		log: log,
	}

	// The flash is built here, before any route exists, because the router is
	// wired with it and a route is wired with the router it was registered on.
	// Secure outside development, for the reason the session cookie is: it
	// carries what somebody typed into a form, and without the attribute it
	// travels over plain HTTP.
	a.flash = security.NewFlash(cfg.App.Key, !a.isDev())
	a.router = fhttp.NewRouter().WithFlash(a.flash)

	// The Application owns the recorder because it mounts the console route.
	// One owner: an application that built its own would end up with a console
	// showing a different buffer than the one the middleware fills.
	if a.isDev() || cfg.Observability.TracingSecret != "" {
		a.recorder = observability.NewRecorder(observability.DefaultRecorderSize)
	}
	// The registry is owned here for the reason above and built unconditionally,
	// which the recorder is not. Whatever writes a number does it from wherever
	// it runs -- a socket server, a worker -- and asking those to check whether
	// the console happens to exist would put the debug surface's lifetime into
	// code that has nothing to do with it.
	a.gauges = observability.NewGauges()
	return a
}

// isDev reports whether the debug surface is allowed to exist.
//
// It asks the environment and not the debug flag. The two are set together on
// an ordinary machine and come apart on a staging deployment, where debug may
// be on and the console, the reload stream and the insecure cookie must not be.
func (a *Application) isDev() bool { return a.cfg.App.Env.Is(config.EnvDev) }

// Recorder returns the buffer behind /_arandu/debug, or nil when nothing is
// recording.
//
// Pass it to middleware.Observe, and to the background loops that deserve the
// same page:
//
//	app.Use(middleware.Observe(dev, cfg.Observability.TracingSecret, app.Recorder()))
//	w := jobs.NewWorker(store, jobs.WorkerOptions{Recorder: app.Recorder()})
//	scheduler.NewModule(app.Tasks(), scheduler.Options{Recorder: app.Recorder()})
//
// The nil is the point. Outside development, without a tracing secret, there is
// no recorder -- so those loops build no Collector, record nothing, and cost
// nothing.
//
// It is not wired automatically because the pipeline is assembled in the
// project, in the open, and a middleware that reached back into the Application
// for state would be the kind of hidden coupling the explicit wiring exists to
// avoid.
func (a *Application) Recorder() *observability.Recorder { return a.recorder }

// Gauges returns the registry the console draws, which is never nil.
//
// Hand it to whatever holds a number worth seeing on /_arandu/debug -- a socket
// server, a worker, a pool.
//
// One registry, for the reason there is one recorder: a caller that built its
// own would write numbers the console has no way to reach, and the page would
// be empty while the numbers were right.
//
// It stores and does not measure. What a name means is known to whatever writes
// it, and the registry keeps one reading per name -- no history, no rate.
func (a *Application) Gauges() *observability.Gauges { return a.gauges }

// Config returns the configuration the Application was built with.
func (a *Application) Config() bootstrap.Configuration { return a.cfg }

// Logger returns the root logger. Request handlers must use
// observability.Log(ctx) instead, which carries the request id.
func (a *Application) Logger() *slog.Logger { return a.log }

// Register adds modules in the order they will be booted. Order matters: a
// module may depend on another one already being up.
func (a *Application) Register(mods ...Module) *Application {
	a.modules = append(a.modules, mods...)
	return a
}

// Use adds global middleware to the pipeline. The pipeline order is the order of
// execution on the way in, and its reverse on the way out.
func (a *Application) Use(mw ...fhttp.Middleware) *Application {
	a.pipeline = append(a.pipeline, mw...)
	return a
}

// Boot initializes modules and registers routes. It fails fast: if any module
// fails to boot the process does not come up. There is no silent degraded mode.
func (a *Application) Boot(ctx context.Context) error {
	if a.booted {
		return errors.New("arandu: application already booted")
	}

	// The renderer is found before any route is registered, because a route is
	// wired with the renderer its handlers will use. See RendererProvider.
	if err := a.findRenderer(); err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(a.modules))
	for _, m := range a.modules {
		name := m.Name()
		if name == "" {
			return fmt.Errorf("arandu: module %T returned an empty Name()", m)
		}
		if _, dup := seen[name]; dup {
			return fmt.Errorf("arandu: module %q registered twice", name)
		}
		seen[name] = struct{}{}

		if b, ok := m.(Bootable); ok {
			start := time.Now()
			if err := b.Boot(ctx); err != nil {
				return fmt.Errorf("arandu: booting module %q: %w", name, err)
			}
			a.log.Info("module booted", "module", name, "duration", time.Since(start))
		}

		before := len(a.router.Routes())
		m.Routes(a.router.ForModule(name))
		if err := validateReservedRoutes(m, a.router.Routes()[before:]); err != nil {
			return err
		}
		a.log.Debug("routes registered", "module", name)
	}

	a.mountInternalRoutes()
	a.booted = true
	return nil
}

// validateReservedRoutes keeps application and third-party modules out of the
// HTTP namespace whose requests bypass the application's middleware. First-party
// owners carry a marker from an internal package that external modules cannot
// import.
func validateReservedRoutes(module Module, registered []*fhttp.Route) error {
	if internalroutes.OwnsReservedNamespace(module) {
		return nil
	}
	for _, route := range registered {
		if strings.HasPrefix(route.Pattern, internalPrefix) {
			return fmt.Errorf("arandu: module %q registered route %q under the reserved %s namespace",
				module.Name(), route.Pattern, internalPrefix)
		}
	}
	return nil
}

// findRenderer asks the modules which one brings the view layer.
func (a *Application) findRenderer() error {
	var found fhttp.Renderer
	var by string

	for _, m := range a.modules {
		p, ok := m.(RendererProvider)
		if !ok {
			continue
		}
		if found != nil {
			return fmt.Errorf("arandu: modules %q and %q both provide a view renderer -- register one", by, m.Name())
		}
		found, by = p.Renderer(), m.Name()
	}

	if found != nil {
		a.router = a.router.WithRenderer(found)
		a.log.Debug("view renderer wired", "module", by)
	}
	return nil
}

// startBackground starts the loops of every Background module.
//
// A loop that fails to start stops the process, for the same reason a module
// that fails to boot does: an application whose scheduler silently did not
// start looks healthy and does no scheduled work, and nobody finds out until
// the invoices are a day late.
func (a *Application) startBackground(ctx context.Context) error {
	for _, m := range a.modules {
		b, ok := m.(Background)
		if !ok {
			continue
		}
		if err := b.Start(ctx); err != nil {
			return fmt.Errorf("arandu: starting module %q: %w", m.Name(), err)
		}
		a.log.Info("background loop started", "module", m.Name())
	}
	return nil
}

// mountInternalRoutes exposes the framework's own routes.
//
// /_arandu/health answers everywhere. The console answers in development, and
// outside it only to a request carrying the tracing secret -- which is not the
// same as "only when a tracing secret is configured".
//
// The distinction is a hole if it is missed. The recorder exists whenever the
// secret is set, so mounting the routes on that condition alone puts them in
// production with the secret checked only by the middleware that decides whether
// to RECORD -- and anyone can then read the buffer: SQL with bound arguments,
// dumps, event payloads, across every tenant, with no session and no header.
func (a *Application) mountInternalRoutes() {
	internal := a.router.ForModule("arandu")
	internal.Get(internalPrefix+"health", a.handleHealth).Name("arandu.health")

	// Development only, and mounted from the same condition that injects the
	// script -- so there is no arrangement in which a production page listens
	// for a stream nothing answers. See reload.go.
	if a.isDev() {
		internal.Get(reloadPath, a.handleReload).Name("arandu.reload")
		for _, m := range a.modules {
			if t, ok := m.(ReloadTagger); ok {
				a.reloadTag = []byte(t.ReloadTag(reloadPath))
				break
			}
		}
	}

	if a.recorder == nil {
		return
	}
	console := observability.NewConsole(a.recorder, a.cfg.Observability.Editor, a.gauges)
	handler := console.Handler
	if !a.isDev() {
		handler = requireTracingSecret(a.cfg.Observability.TracingSecret, handler)
	}
	internal.Get(observability.ConsolePath, handler).Name("arandu.debug.index")
	internal.Get(observability.ConsolePath+"/{id}", handler).Name("arandu.debug.show")
}

// requireTracingSecret gates the console outside development.
//
// It answers 404 rather than 401, because a 401 confirms that the console is
// there. Somebody scanning for /_arandu/debug learns nothing from a 404 that
// they did not already know.
//
// The comparison is constant-time: a byte-by-byte one leaks the secret to
// anybody willing to measure, and this secret is the whole gate.
func requireTracingSecret(secret string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// An empty secret cannot authorize anything. It is also the zero value
		// of the configuration, so treating it as "no gate" would open the
		// console for every application that never set one.
		if secret == "" {
			http.NotFound(w, r)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get(observability.TracingHeader)), []byte(secret)) != 1 {
			http.NotFound(w, r)
			return
		}
		next(w, r)
	}
}

// handleHealth answers 200 only when every module that implements Health is
// healthy. It names the failing module in the body, because a load balancer
// probe that only says "unhealthy" costs an hour of guessing.
func (a *Application) handleHealth(w http.ResponseWriter, r *http.Request) {
	for _, m := range a.modules {
		h, ok := m.(Health)
		if !ok {
			continue
		}
		if err := h.Health(r.Context()); err != nil {
			observability.Log(r.Context()).Error("health check failed", "module", m.Name(), "error", err)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "module %s unavailable", m.Name())
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// Handler returns the composed handler: the router wrapped in the global
// pipeline, with the application logger installed above everything else. Useful
// for tests, which drive the whole stack without a socket.
//
// The logger has to be outermost. Without it, every Log(ctx) call in a request
// would fall back to slog.Default() and ignore the configured handler and level,
// which in production means request logs in the wrong format.
func (a *Application) Handler() http.Handler {
	// Live reload is outermost after the logger, so it sees the finished
	// document rather than a handler's intention to write one.
	outer := append([]fhttp.Middleware{observability.RootLogger(a.log)}, devReload(a.isDev(), a.reloadTag)...)

	// The flash is consumed above the application's own pipeline and below the
	// logger, and the Application installs it rather than bootstrap/app.go for
	// the reason the field on the Application says: there is nothing to decide.
	//
	// exceptInternal for the same reason everything else is: the debug console
	// is a page a browser asks for with text/html, so without this it would
	// spend the one-shot flash on a request that draws none of it, and the form
	// the person was sent back to would come back blank -- a bug reachable only
	// with the console open, which is exactly when somebody is debugging
	// something else.
	outer = append(outer, exceptInternal(middleware.Flash(a.flash)))

	// The application's middleware runs on the application's routes. What this
	// framework mounts under internalPrefix is not the application's traffic and
	// must not be measured as if it were.
	//
	// It was. The development reload asks once a second which process is
	// answering, and that ran through the rate limit an application mounts for
	// its own visitors -- 60 of a 300-per-minute budget per open tab, shared,
	// because the key falls back to the address for a request with no session.
	// Ordinary browsing with two tabs open answered "too many requests: wait 32
	// seconds", on a page nobody had hammered.
	app := make([]fhttp.Middleware, 0, len(a.pipeline))
	for _, mw := range a.pipeline {
		app = append(app, exceptInternal(mw))
	}

	return fhttp.Chain(a.router, append(outer, app...)...)
}

// internalPrefix is what this framework mounts for itself: the health probe,
// the debug console, the development reload.
//
// It is declared surface rather than a hole in the inventory: every route under
// it is registered on the router like any other, so `aru routes` prints it and
// the error page names it. What it does not answer to is the application's
// policy. exceptInternal takes every application middleware off it, and the
// framework gates each endpoint itself -- the probe by nothing, because it reads
// no data and holds no session; the console by its own secret, in constant time;
// the reload by the environment; the assets by the hash in the path.
//
// The namespace has more than one first-party owner. The Application mounts the
// probe, reload and console; the view module mounts the content-addressed asset
// route. validateReservedRoutes distinguishes those owners with a marker from a
// Go internal package and refuses every other module at boot, before the handler
// can be served.
const internalPrefix = "/_arandu/"

// exceptInternal runs an application's middleware everywhere except on the
// framework's own routes.
//
// The framework's endpoints answer to the framework: the health probe must not
// be rate limited by the application it reports on, the debug console is gated
// by its own secret, and the reload is a question the page asks about the
// process rather than a request the visitor made. None of them touch the
// database, hold a session, or write anything.
//
// The prefix is the boundary because it already is one everywhere else -- one
// name for what belongs to the framework, checked in one place.
func exceptInternal(mw fhttp.Middleware) fhttp.Middleware {
	return func(next http.Handler) http.Handler {
		wrapped := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, internalPrefix) {
				next.ServeHTTP(w, r)
				return
			}
			wrapped.ServeHTTP(w, r)
		})
	}
}

// newServer builds the server Run listens with. It is separate from Run so that
// the limits above can be asserted without binding a port: a field left off this
// literal is a limit the process silently does not have.
func (a *Application) newServer() *http.Server {
	return &http.Server{
		Addr:              a.cfg.App.HTTPAddr,
		Handler:           a.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(a.log.Handler(), slog.LevelError),
	}
}

// Run starts the server and blocks until SIGINT or SIGTERM, then shuts down
// gracefully.
func (a *Application) Run(ctx context.Context) error {
	if !a.booted {
		return errors.New("arandu: Run called before Boot")
	}

	// The background loops start here rather than at boot, so only the process
	// that serves runs them. See Background.
	if err := a.startBackground(ctx); err != nil {
		return err
	}

	a.srv = a.newServer()

	errc := make(chan error, 1)
	go func() {
		a.log.Info("server listening", "addr", a.cfg.App.HTTPAddr, "env", a.cfg.App.Env)
		if err := a.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stop)

	select {
	case err := <-errc:
		return err
	case <-stop:
		a.log.Info("shutdown started")
	case <-ctx.Done():
		a.log.Info("shutdown started", "reason", ctx.Err())
	}

	return a.Shutdown()
}

// Shutdown stops the server and closes the modules in reverse registration
// order, which is the only order that respects dependencies between them.
func (a *Application) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	var errs []error
	if a.srv != nil {
		if err := a.srv.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(a.modules) - 1; i >= 0; i-- {
		if c, ok := a.modules[i].(Closable); ok {
			if err := c.Close(ctx); err != nil {
				errs = append(errs, fmt.Errorf("closing %s: %w", a.modules[i].Name(), err))
			}
		}
	}
	return errors.Join(errs...)
}

// Migrations collects the migrations of every module, in registration order.
// Hand the result to migrations.Migrator.RunPending.
//
// This is the module half of migration discovery, and migrations.Register is the
// other half: a module is a value the Application already holds, so it is asked;
// an application's own migrations live in a package nothing calls, so they
// announce themselves from init(). What the two must never both hold is one
// migration -- the registry orders by name and this orders by registration, so a
// migration in both has two positions and no rule that says which one wins.
func (a *Application) Migrations() []Migration {
	var out []Migration
	for _, m := range a.modules {
		if mm, ok := m.(Migratable); ok {
			out = append(out, mm.Migrations()...)
		}
	}
	return out
}

// Tasks collects the scheduled work from every registered module, in
// registration order.
//
// Same shape as Migrations(): the module declares, the Application collects,
// and the scheduler module runs. Pass it to scheduler.NewModule, which is why
// that one is registered last.
func (a *Application) Tasks() []Task {
	var out []Task
	for _, m := range a.modules {
		s, ok := m.(Schedulable)
		if !ok {
			continue
		}
		out = append(out, s.Schedule()...)
	}
	return out
}

// Diagnose asks every module that implements Diagnostic what is wrong right
// now, and returns what they say.
//
// Pass it to errorpage.Options.Diagnose. It is not wired automatically because
// the pipeline is assembled in the open, in the application.
func (a *Application) Diagnose(ctx context.Context) []string {
	var out []string
	for _, m := range a.modules {
		d, ok := m.(Diagnostic)
		if !ok {
			continue
		}
		out = append(out, d.Diagnose(ctx)...)
	}
	return out
}

// Routes returns the registered routes. It is empty before Boot, because a
// module only registers its routes when it boots.
func (a *Application) Routes() []*fhttp.Route { return a.router.Routes() }

// FormatRoutes renders the route table for the terminal, grouped by module and
// sorted by pattern. It is here, and not in the CLI, so that every project
// prints the same table.
//
// The table itself is hesape/routing.FormatRoutes, and this is a call through
// rather than a copy: fhttp.Route is an alias for routing.Route, so the slice
// needs no translation and there is one implementation of the format. A wrapper
// and not an alias, because Go has no alias form for a function.
func FormatRoutes(routes []*fhttp.Route) string { return routing.FormatRoutes(routes) }
