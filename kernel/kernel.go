// Package kernel boots the application.
//
// It is the single place where an application is composed. One difference
// matters: the Kernel boots ONCE, at process start, not per request, so
// nothing here may assume request scope.
package kernel

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/arandu-io/framework/config"
	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/observability"
)

// Timeouts of the HTTP server. They are set rather than left at zero because a
// server without timeouts is one slow client away from running out of file
// descriptors.
const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 20 * time.Second
)

// Kernel holds the composed application: configuration, modules, the global
// middleware pipeline and the router.
type Kernel struct {
	cfg      config.Config
	log      *slog.Logger
	router   *httpx.Router
	modules  []Module
	pipeline []httpx.Middleware
	srv      *http.Server
	booted   bool
	// recorder is the ring buffer behind the console. It exists in development
	// and under a tracing secret, and is nil otherwise -- which is what makes
	// the console cost nothing in production rather than cost little.
	recorder *observability.Recorder
}

// New assembles the kernel. It opens no connection and listens on no port --
// that is Boot and Run.
func New(cfg config.Config) *Kernel {
	log := observability.NewLogger(string(cfg.Env), cfg.LogLevel)
	k := &Kernel{
		cfg:    cfg,
		log:    log,
		router: httpx.NewRouter(),
	}

	// The kernel owns the recorder because it is what mounts the console route.
	// One owner: an application that built its own would end up with a console
	// showing a different buffer than the one the middleware fills.
	if cfg.IsDev() || cfg.TracingSecret != "" {
		k.recorder = observability.NewRecorder(observability.DefaultRecorderSize)
	}
	return k
}

// Recorder returns the buffer behind /_arandu/debug, or nil when nothing is
// recording.
//
// Pass it to middleware.Observe, and to the background loops that deserve the
// same page:
//
//	k.Use(middleware.Observe(k.Recorder(), cfg.TracingSecret))
//	w := jobs.NewWorker(store, jobs.WorkerOptions{Recorder: k.Recorder()})
//	scheduler.NewModule(k.Tasks(), scheduler.Options{Recorder: k.Recorder()})
//
// The nil is the point. Outside development, without a tracing secret, there is
// no recorder -- so those loops build no Collector, record nothing, and cost
// nothing. They used to build one unconditionally and throw it away.
//
// It is not wired automatically because the pipeline is assembled in the
// application, in the open, and a middleware that reached back into the kernel
// for state would be the kind of hidden coupling the explicit wiring exists to
// avoid.
func (k *Kernel) Recorder() *observability.Recorder { return k.recorder }

// Config returns the configuration the kernel was built with.
func (k *Kernel) Config() config.Config { return k.cfg }

// Logger returns the root logger. Request handlers must use
// observability.Log(ctx) instead, which carries the request id.
func (k *Kernel) Logger() *slog.Logger { return k.log }

// Register adds modules in the order they will be booted. Order matters: a
// module may depend on another one already being up.
func (k *Kernel) Register(mods ...Module) *Kernel {
	k.modules = append(k.modules, mods...)
	return k
}

// Use adds global middleware to the pipeline. The pipeline order is the order of
// execution on the way in, and its reverse on the way out.
func (k *Kernel) Use(mw ...httpx.Middleware) *Kernel {
	k.pipeline = append(k.pipeline, mw...)
	return k
}

// Boot initializes modules and registers routes. It fails fast: if any module
// fails to boot the process does not come up. There is no silent degraded mode.
func (k *Kernel) Boot(ctx context.Context) error {
	if k.booted {
		return errors.New("arandu: kernel already booted")
	}

	// The renderer is found before any route is registered, because a route is
	// wired with the renderer its handlers will use. See RendererProvider.
	if err := k.findRenderer(); err != nil {
		return err
	}

	seen := make(map[string]struct{}, len(k.modules))
	for _, m := range k.modules {
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
			k.log.Info("module booted", "module", name, "duration", time.Since(start))
		}

		m.Routes(k.router.ForModule(name))
		k.log.Debug("routes registered", "module", name)
	}

	k.mountInternalRoutes()
	k.booted = true
	return nil
}

// findRenderer asks the modules which one brings the view layer.
func (k *Kernel) findRenderer() error {
	var found httpx.Renderer
	var by string

	for _, m := range k.modules {
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
		k.router = k.router.WithRenderer(found)
		k.log.Debug("view renderer wired", "module", by)
	}
	return nil
}

// startBackground starts the loops of every Background module.
//
// A loop that fails to start stops the process, for the same reason a module
// that fails to boot does: an application whose scheduler silently did not
// start looks healthy and does no scheduled work, and nobody finds out until
// the invoices are a day late.
func (k *Kernel) startBackground(ctx context.Context) error {
	for _, m := range k.modules {
		b, ok := m.(Background)
		if !ok {
			continue
		}
		if err := b.Start(ctx); err != nil {
			return fmt.Errorf("arandu: starting module %q: %w", m.Name(), err)
		}
		k.log.Info("background loop started", "module", m.Name())
	}
	return nil
}

// mountInternalRoutes exposes the framework's own routes.
//
// /_arandu/health answers everywhere. The console answers in development, and
// outside it only to a request carrying the tracing secret -- which is not the
// same as "only when a tracing secret is configured".
//
// That distinction was a hole. The recorder exists whenever the secret is set,
// so the routes were mounted in production, and the secret was checked only by
// the middleware that decides whether to RECORD. Anyone could then read the
// buffer: SQL with bound arguments, dumps, event payloads, across every tenant,
// with no session and no header. Found by audit, reproduced over a real socket.
func (k *Kernel) mountInternalRoutes() {
	internal := k.router.ForModule("arandu")
	internal.Get("/_arandu/health", k.handleHealth)

	// Development only, and mounted from the same condition that injects the
	// script -- so there is no arrangement in which a production page listens
	// for a stream nothing answers. See reload.go.
	if k.cfg.IsDev() {
		internal.Get(reloadPath, k.handleReload)
		for _, m := range k.modules {
			if t, ok := m.(ReloadTagger); ok {
				reloadTag = []byte(t.ReloadTag(reloadPath))
				break
			}
		}
	}

	if k.recorder == nil {
		return
	}
	console := observability.NewConsole(k.recorder, k.cfg.Editor)
	handler := console.Handler
	if !k.cfg.IsDev() {
		handler = requireTracingSecret(k.cfg.TracingSecret, handler)
	}
	internal.Get(observability.ConsolePath, handler)
	internal.Get(observability.ConsolePath+"/{id}", handler)
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
func (k *Kernel) handleHealth(w http.ResponseWriter, r *http.Request) {
	for _, m := range k.modules {
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
func (k *Kernel) Handler() http.Handler {
	// Live reload is outermost after the logger, so it sees the finished
	// document rather than a handler's intention to write one.
	outer := append([]httpx.Middleware{observability.RootLogger(k.log)}, devReload(k.cfg.IsDev())...)
	return httpx.Chain(k.router, append(outer, k.pipeline...)...)
}

// Run starts the server and blocks until SIGINT or SIGTERM, then shuts down
// gracefully.
func (k *Kernel) Run(ctx context.Context) error {
	if !k.booted {
		return errors.New("arandu: Run called before Boot")
	}

	// The background loops start here rather than at boot, so only the process
	// that serves runs them. See kernel.Background.
	if err := k.startBackground(ctx); err != nil {
		return err
	}

	k.srv = &http.Server{
		Addr:              k.cfg.HTTPAddr,
		Handler:           k.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(k.log.Handler(), slog.LevelError),
	}

	errc := make(chan error, 1)
	go func() {
		k.log.Info("server listening", "addr", k.cfg.HTTPAddr, "env", k.cfg.Env)
		if err := k.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
		k.log.Info("shutdown started")
	case <-ctx.Done():
		k.log.Info("shutdown started", "reason", ctx.Err())
	}

	return k.Shutdown()
}

// Shutdown stops the server and closes the modules in reverse registration
// order, which is the only order that respects dependencies between them.
func (k *Kernel) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	var errs []error
	if k.srv != nil {
		if err := k.srv.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	for i := len(k.modules) - 1; i >= 0; i-- {
		if c, ok := k.modules[i].(Closable); ok {
			if err := c.Close(ctx); err != nil {
				errs = append(errs, fmt.Errorf("closing %s: %w", k.modules[i].Name(), err))
			}
		}
	}
	return errors.Join(errs...)
}

// Migrations collects the migrations of every module, in registration order.
// Hand the result to data.Migrate.
func (k *Kernel) Migrations() []Migration {
	var out []Migration
	for _, m := range k.modules {
		if mm, ok := m.(Migratable); ok {
			out = append(out, mm.Migrations()...)
		}
	}
	return out
}

// Tasks collects the scheduled work from every registered module, in
// registration order.
//
// Same shape as Migrations(): the module declares, the kernel collects, and the
// scheduler module runs. Pass it to scheduler.NewModule, which is why that one
// is registered last.
func (k *Kernel) Tasks() []Task {
	var out []Task
	for _, m := range k.modules {
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
func (k *Kernel) Diagnose(ctx context.Context) []string {
	var out []string
	for _, m := range k.modules {
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
func (k *Kernel) Routes() []*httpx.Route { return k.router.Routes() }

// FormatRoutes renders the route table for the terminal, grouped by module and
// sorted by pattern. It is here, and not in the CLI, so that every project
// prints the same table.
func FormatRoutes(routes []*httpx.Route) string {
	sorted := append([]*httpx.Route{}, routes...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Module != sorted[j].Module {
			return sorted[i].Module < sorted[j].Module
		}
		if sorted[i].Pattern != sorted[j].Pattern {
			return sorted[i].Pattern < sorted[j].Pattern
		}
		return sorted[i].Method < sorted[j].Method
	})

	var b strings.Builder
	module := ""
	for _, r := range sorted {
		if r.Module != module {
			module = r.Module
			fmt.Fprintf(&b, "\n%s\n", module)
		}
		// The name column is what `aru route:list` shows, and what a
		// developer copies into route("...") instead of typing the path.
		if name := r.RouteName(); name != "" {
			fmt.Fprintf(&b, "  %-7s %-34s %s\n", r.Method, r.Pattern, name)
		} else {
			fmt.Fprintf(&b, "  %-7s %s\n", r.Method, r.Pattern)
		}
	}
	if b.Len() == 0 {
		return "no routes registered\n"
	}
	return strings.TrimLeft(b.String(), "\n")
}
