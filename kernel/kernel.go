// Package kernel boots the application.
//
// It is the equivalent of Laravel's bootstrap/app.php: the single place where an
// application is composed. One difference matters: the Kernel boots ONCE, at
// process start, not per request, so nothing here may assume request scope.
package kernel

import (
	"context"
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

// Recorder returns the request buffer behind /_arandu/debug, or nil outside
// development.
//
// Pass it to middleware.Observe. It is not wired automatically because the
// pipeline is assembled in the application, in the open, and a middleware that
// reached back into the kernel for state would be the kind of hidden coupling
// the explicit wiring exists to avoid.
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

// mountInternalRoutes exposes the framework's own routes. In production only
// /_arandu/health answers; everything else requires Env dev.
func (k *Kernel) mountInternalRoutes() {
	internal := k.router.ForModule("arandu")
	internal.Get("/_arandu/health", k.handleHealth)
	if k.recorder != nil {
		console := observability.NewConsole(k.recorder, k.cfg.Editor)
		internal.Get(observability.ConsolePath, console.Handler)
		internal.Get(observability.ConsolePath+"/{id}", console.Handler)
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
	return httpx.Chain(k.router,
		append([]httpx.Middleware{observability.RootLogger(k.log)}, k.pipeline...)...)
}

// Run starts the server and blocks until SIGINT or SIGTERM, then shuts down
// gracefully.
func (k *Kernel) Run(ctx context.Context) error {
	if !k.booted {
		return errors.New("arandu: Run called before Boot")
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
func (k *Kernel) Routes() []httpx.Route { return k.router.Routes() }

// FormatRoutes renders the route table for the terminal, grouped by module and
// sorted by pattern. It is here, and not in the CLI, so that every project
// prints the same table.
func FormatRoutes(routes []httpx.Route) string {
	sorted := append([]httpx.Route{}, routes...)
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
		fmt.Fprintf(&b, "  %-7s %s\n", r.Method, r.Pattern)
	}
	if b.Len() == 0 {
		return "no routes registered\n"
	}
	return strings.TrimLeft(b.String(), "\n")
}
