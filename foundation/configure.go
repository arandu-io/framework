package foundation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/arandu-io/framework/foundation/bootstrap"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/hesape/cache"
	"github.com/arandu-io/hesape/console"
	"github.com/arandu-io/hesape/console/scheduling"
	"github.com/arandu-io/hesape/exception"
	hhttp "github.com/arandu-io/hesape/http"
	httpmiddleware "github.com/arandu-io/hesape/http/middleware"
	"github.com/arandu-io/hesape/queue"
	hmiddleware "github.com/arandu-io/hesape/routing/middleware"
	"github.com/arandu-io/hesape/translation"
)

// ApplicationBuilder configures an Application: its providers, its routes,
// its middleware, its exceptions, its commands, its schedule, its job handlers
// and its seeders. It is what an application's bootstrap/app.go is made of:
//
//	return foundation.Configure(drivers).
//		WithProviders(view.NewModule(), app).
//		WithRouting(func(r *http.Router) { routes.Web(r, app.Routes()) }).
//		WithMiddleware(func(m *foundation.Middleware) { m.ValidateCSRFTokensExcept("/webhooks/") }).
//		WithCommands(routes.Commands(app)...).
//		WithSchedule(routes.Schedule(app)...).
//		WithJobs(app.RegisterJobs).
//		WithSeeders(app.Seed).
//		Create()
//
// Every call is typed and written by hand. Nothing is resolved from a
// container, nothing is discovered and nothing is registered globally: what
// the application is, is what this chain says, read top to bottom.
//
// A builder is used once, by one goroutine. A With call that takes a function
// composes with an earlier one, in order; one that sets a single value refuses
// a second call when Create runs, because two answers to one question is the
// one that loses being dropped in silence.
type ApplicationBuilder struct {
	drivers *Drivers

	providers  []Module
	web        []func(*fhttp.Router)
	middleware []func(*Middleware)
	exceptions []func(*exception.Handler)
	commands   []console.Command
	schedule   []Task
	jobs       []func(*queue.Worker) error
	seed       func(context.Context, []string) (string, error)
	tenants    func(context.Context) ([]string, error)
	prunables  map[string]func(context.Context) (int, error)
	translator *translation.Translator

	version, commit string

	errs []error
}

// Configure starts the configuration of an Application over the drivers the
// framework built from the typed configuration.
func Configure(d *Drivers) *ApplicationBuilder {
	return &ApplicationBuilder{drivers: d}
}

// WithProviders registers modules, in the order they boot. An application's own
// providers live in app/Providers, each a Module listed here; the view module
// and any installed module are listed the same way.
func (b *ApplicationBuilder) WithProviders(mods ...Module) *ApplicationBuilder {
	b.providers = append(b.providers, mods...)
	return b
}

// WithRouting registers the application's routes, under the module "web",
// after every provider has booted. A second call adds its routes after the
// first.
func (b *ApplicationBuilder) WithRouting(web func(r *fhttp.Router)) *ApplicationBuilder {
	if web != nil {
		b.web = append(b.web, web)
	}
	return b
}

// WithMiddleware adjusts the global pipeline the framework composes from the
// configuration. See Middleware for the order and what can be changed.
func (b *ApplicationBuilder) WithMiddleware(using func(m *Middleware)) *ApplicationBuilder {
	if using != nil {
		b.middleware = append(b.middleware, using)
	}
	return b
}

// WithExceptions is handed the one handler that answers a failed request, to
// register the application's own answers on it: what is never reported, what
// is drawn the application's way, what is answered as a document.
func (b *ApplicationBuilder) WithExceptions(using func(e *exception.Handler)) *ApplicationBuilder {
	if using != nil {
		b.exceptions = append(b.exceptions, using)
	}
	return b
}

// WithCommands adds the application's own console commands -- the list in
// routes/console.go, whose code lives in app/Console/Commands. They run after
// the application boots. A name the framework or a module already answers is
// refused by Create, naming both declarers.
func (b *ApplicationBuilder) WithCommands(commands ...console.Command) *ApplicationBuilder {
	b.commands = append(b.commands, commands...)
	return b
}

// WithSchedule adds the application's own scheduled tasks, beside the ones the
// registered modules declare. The scheduler runs them in the serving process.
func (b *ApplicationBuilder) WithSchedule(tasks ...Task) *ApplicationBuilder {
	b.schedule = append(b.schedule, tasks...)
	return b
}

// WithTenants answers the tenants a per-tenant task expands to, each run with
// its own Grant. Without it a per-tenant task does not run, and the scheduler
// reports that rather than skipping in silence.
func (b *ApplicationBuilder) WithTenants(tenants func(ctx context.Context) ([]string, error)) *ApplicationBuilder {
	if b.tenants != nil {
		b.errs = append(b.errs, errors.New("arandu: WithTenants was called twice, and a task can expand to one list of tenants"))
	}
	b.tenants = tenants
	return b
}

// WithJobs registers the application's job handlers on the worker queue:work
// starts: every kind of work the application does in the background, built
// over the services its providers made. A worker left with no handler is
// refused rather than started.
//
// The registration runs once when Create runs, on a worker that is then
// dropped, so a registration that fails -- or a name given two handlers --
// stops every command at the boot rather than the worker alone, later.
func (b *ApplicationBuilder) WithJobs(register func(w *queue.Worker) error) *ApplicationBuilder {
	if register != nil {
		b.jobs = append(b.jobs, register)
	}
	return b
}

// WithSeeders gives db:seed and migrate --seed the application's seeders:
// seed runs the one args[0] names, or the root one when args is empty, with
// the rest of args as its own, and answers the name that ran. It is where the
// application decides what a seeder is allowed to touch.
func (b *ApplicationBuilder) WithSeeders(seed func(ctx context.Context, args []string) (string, error)) *ApplicationBuilder {
	if b.seed != nil {
		b.errs = append(b.errs, errors.New("arandu: WithSeeders was called twice, and db:seed runs one set of seeders"))
	}
	b.seed = seed
	return b
}

// WithPrunables answers what model:prune prunes: each entry removes what has
// expired and answers how many rows it removed.
func (b *ApplicationBuilder) WithPrunables(prunables map[string]func(ctx context.Context) (int, error)) *ApplicationBuilder {
	if b.prunables == nil {
		b.prunables = map[string]func(context.Context) (int, error){}
	}
	for name, prune := range prunables {
		if _, taken := b.prunables[name]; taken {
			b.errs = append(b.errs, fmt.Errorf("arandu: the prunable %q was given twice", name))
		}
		b.prunables[name] = prune
	}
	return b
}

// WithTranslator is the application's catalogue, which every request carries
// beside the application name, so a component draws its own words in the
// request's language. See Application.WithTranslator.
func (b *ApplicationBuilder) WithTranslator(t *translation.Translator) *ApplicationBuilder {
	if b.translator != nil {
		b.errs = append(b.errs, errors.New("arandu: WithTranslator was called twice, and a request carries one catalogue"))
	}
	b.translator = t
	return b
}

// WithVersion is what the version command prints: the version and the commit
// the build stamped.
func (b *ApplicationBuilder) WithVersion(version, commit string) *ApplicationBuilder {
	b.version, b.commit = version, commit
	return b
}

// Create composes the Application.
//
// In order: the drivers' own module, so the database is reported on the
// readiness probe and closed last; the global pipeline; the providers; the
// routes; the queue's module, which brings the jobs table of the queue the
// drivers built; the shared store's health module when there is one; and the
// scheduler, last, over the tasks every module declared and the application's
// schedule. Then the console: the built-in commands, the application's and
// every Commandable module's, each name declared once.
//
// It boots nothing and listens on nothing; HandleCommand decides what the
// command needs. It fails when the composition is contradictory -- a command
// name declared twice, a single-valued With called twice -- and closes the
// drivers then, so a refused composition leaves nothing open.
func (b *ApplicationBuilder) Create() (*Application, error) {
	d := b.drivers
	if d == nil {
		return nil, errors.New("arandu: Configure needs the drivers NewDrivers built")
	}
	if len(b.errs) > 0 {
		_ = d.Close()
		return nil, errors.Join(b.errs...)
	}
	s := d.Settings
	fw := s.Framework

	a := New(fw)
	a.WithTranslator(b.translator)
	a.Register(driversModule{drivers: d})

	// The one handler that answers a failed request: whether the debug page may
	// exist, which editor a frame links to, and which frames are this
	// application's, read from the module the binary was built from.
	exceptions := bootstrap.HandleExceptions(fw, mainModule(), a.Diagnose)
	for _, using := range b.exceptions {
		using(exceptions)
	}

	m := &Middleware{throttle: cache.PerMinute(defaultThrottle)}
	for _, using := range b.middleware {
		using(m)
	}
	a.Use(m.pipeline(a, d, exceptions)...)

	a.Register(b.providers...)
	if len(b.web) > 0 {
		a.Register(webModule{routes: b.web})
	}
	a.Register(queueModule{queue.NewModule(d.Queue)})
	if d.SharedCache != nil {
		a.Register(NewCacheModule("cache", d.SharedCache))
	}

	// The scheduler goes last: it collects what the modules above declared.
	// Its singleton claims are taken where every replica sees them, and nowhere
	// when nothing is shared, which is a single replica's honest answer.
	tasks := scheduling.NewTaskModule(append(a.Tasks(), b.schedule...), scheduling.TaskOptions{
		Locks:    d.Locks,
		Tenants:  b.tenants,
		Recorder: a.Recorder(),
	})
	a.Register(taskModule{tasks})

	if !s.Filesystems.IsObjectStorage() && !s.App.IsDev() {
		a.log.Warn("files are kept on the local disk, which a second replica does not see: FILESYSTEM_DISK=local outside development",
			"root", s.Filesystems.Root)
	}

	jobs, err := b.registerJobs(d)
	if err != nil {
		_ = d.Close()
		return nil, err
	}

	commands := builtins(a, builtinParts{
		drivers:   d,
		tasks:     tasks,
		seed:      b.seed,
		jobs:      jobs,
		prunables: b.prunables,
		version:   b.version,
		commit:    b.commit,
	})
	for _, c := range b.commands {
		commands = append(commands, declared{by: "the application", boots: true, command: c})
	}
	commands = append(commands, a.moduleCommands()...)

	k, err := compose(commands, commandAliases)
	if err != nil {
		_ = d.Close()
		return nil, err
	}
	k.environment = s.App.Env
	k.locks = d.Locks
	k.close = d.Close
	a.kernel = k
	return a, nil
}

// registerJobs proves every WithJobs registration on a worker that is dropped,
// and answers the registration queue:work runs, or nil when there is none --
// which is what makes queue:work refuse to start a worker with nothing to run.
func (b *ApplicationBuilder) registerJobs(d *Drivers) (func(*queue.Worker), error) {
	if len(b.jobs) == 0 {
		return nil, nil
	}
	probe := queue.NewWorker(d.Queue, queue.WorkerOptions{})
	for _, register := range b.jobs {
		if err := register(probe); err != nil {
			return nil, fmt.Errorf("arandu: registering the job handlers: %w", err)
		}
	}
	// Proven above, so what the worker is handed cannot fail: the same
	// functions over the same services.
	return func(w *queue.Worker) {
		for _, register := range b.jobs {
			_ = register(w)
		}
	}, nil
}

// mainModule is the module path of the binary, which the error page uses to
// tell the application's frames from the framework's. It is read from the build
// rather than written in the application, because a constant could only be
// right for the project it was written in.
func mainModule() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		return info.Main.Path
	}
	return ""
}

// defaultThrottle is the global budget, per minute, per session or address.
const defaultThrottle = 300

// Middleware is the global pipeline the framework composes from the
// configuration, and what an application changes about it.
//
// The order is the order of execution, outermost first:
//
//	LocalizeSentences          the Sentences given, so every refusal below reads them
//	exception.Recover          a panic anywhere below answers with a page
//	what Prepend added
//	ValidatePostSize           413 for a body that declares itself too large
//	LimitBodySize              and a cut for one that declares nothing
//	TrustProxies               the visitor's address, from a proxy TRUSTED_PROXIES lists
//	Observe                    the request id, the logger, the debug console's recording
//	SecurityHeaders            the content security policy and the rest
//	what BeforeThrottle added
//	Throttle                   the global budget, per session or address
//	CSRFProtect                every write checked, every page given its token
//	OverrideMethod             a form's _method
//	what Append added
//
// Recover is first after the sentences and stays there: a panic in a
// middleware above it escapes without a page, and LocalizeSentences only puts
// a value on the context. Each layer takes its settings from the configuration
// -- the body limit, the trusted proxies, the environment, the session store, the
// CSRF issuer -- so what an application adjusts is only what is a decision.
type Middleware struct {
	sentences      exception.Sentences
	observe        func(dev bool, tracingSecret string, recorder *observability.Recorder) func(http.Handler) http.Handler
	prepend        []fhttp.Middleware
	beforeThrottle []fhttp.Middleware
	append         []fhttp.Middleware
	csrfExcept     []string
	throttle       cache.Limit
	imageOrigins   []string
}

// Sentences are the words a person reads when a request is refused -- the
// status pages, the problem documents, every Refuse -- chosen per request, for
// the language of the application. They are put on the request outside
// Recover, because a refusal the exception handler answers reads the request
// Recover was handed, and Sentences mounted anywhere further in never reach it.
// A second call replaces the first; nil leaves the standard sentences.
func (m *Middleware) Sentences(s exception.Sentences) *Middleware {
	m.sentences = s
	return m
}

// Observer replaces Observe in its place in the pipeline: what puts the
// request id and the request's logger on the context and records the request
// for the debug console. It is handed what Observe is handed -- whether this is
// development, the tracing secret and the console's recorder, nil when nothing
// is recorded -- so an application's own observer, one that redacts what a
// path carries before it is logged, records exactly where Observe would. Nil
// keeps Observe.
func (m *Middleware) Observer(observe func(dev bool, tracingSecret string, recorder *observability.Recorder) func(http.Handler) http.Handler) *Middleware {
	m.observe = observe
	return m
}

// Prepend adds middleware right after Recover, before the body limits.
func (m *Middleware) Prepend(mw ...fhttp.Middleware) *Middleware {
	m.prepend = append(m.prepend, mw...)
	return m
}

// BeforeThrottle adds middleware after the visitor's address is known and
// before the global budget is counted: after TrustProxies, Observe and
// SecurityHeaders, before Throttle and CSRFProtect. It is the place for what
// has to hold on a refused request too -- a no-store on a path that carries a
// bearer link is still needed on the 429 that turns it away -- and for what
// reads the real address.
func (m *Middleware) BeforeThrottle(mw ...fhttp.Middleware) *Middleware {
	m.beforeThrottle = append(m.beforeThrottle, mw...)
	return m
}

// Append adds middleware after OverrideMethod, right before the router.
func (m *Middleware) Append(mw ...fhttp.Middleware) *Middleware {
	m.append = append(m.append, mw...)
	return m
}

// ValidateCSRFTokensExcept leaves the paths under the given prefixes to their
// own guard: a provider posting a webhook carries no session and no token, and
// every route under the prefix verifies the provider's signature instead. A
// request with a bearer token and no session cookie is already left to the
// token guard.
func (m *Middleware) ValidateCSRFTokensExcept(prefixes ...string) *Middleware {
	m.csrfExcept = append(m.csrfExcept, prefixes...)
	return m
}

// Throttle sets the global budget, in place of three hundred requests a minute.
// The key is the session while the store still holds it, and the address
// otherwise.
func (m *Middleware) Throttle(limit cache.Limit) *Middleware {
	m.throttle = limit
	return m
}

// ImageOrigins are the origins the content security policy lets a page load
// images from, beside this one.
func (m *Middleware) ImageOrigins(origins ...string) *Middleware {
	m.imageOrigins = append(m.imageOrigins, origins...)
	return m
}

// pipeline is the composed list, in the order Middleware documents.
func (m *Middleware) pipeline(a *Application, d *Drivers, exceptions *exception.Handler) []fhttp.Middleware {
	s := d.Settings
	dev := s.App.IsDev()
	observe := m.observe
	if observe == nil {
		observe = middleware.Observe
	}

	out := []fhttp.Middleware{exception.LocalizeSentences(m.sentences), exception.Recover(exceptions)}
	out = append(out, m.prepend...)
	out = append(out,
		httpmiddleware.ValidatePostSize(s.HTTP.MaxBodyBytes),
		httpmiddleware.LimitBodySize(s.HTTP.MaxBodyBytes),
		httpmiddleware.TrustProxies(s.HTTP.TrustedProxies),
		// The recorder is nil outside development without a tracing secret,
		// and then nothing is recorded.
		observe(dev, s.Framework.Observability.TracingSecret, a.Recorder()),
		httpmiddleware.SecurityHeaders(dev, m.imageOrigins...),
	)
	out = append(out, m.beforeThrottle...)
	out = append(out,
		// The refusal adds HX-Refresh, without which somebody over the limit
		// presses the button and the screen does not change.
		hmiddleware.Throttle(d.Limiter, m.throttle, middleware.KeyBySession(d.Sessions), hhttp.Refuse),
		middleware.CSRFProtect(d.CSRF, d.Sessions.IDFromRequest, middleware.CSRFExcept(m.csrfExcept...)),
		httpmiddleware.OverrideMethod(),
	)
	return append(out, m.append...)
}

// webModule is the application's routes as a module.
type webModule struct{ routes []func(*fhttp.Router) }

func (webModule) Name() string { return "web" }

// Routes registers every WithRouting function, in order.
func (m webModule) Routes(r *fhttp.Router) {
	for _, web := range m.routes {
		web(r)
	}
}

// queueModule is the queue's module under this package's contract: the jobs
// table of the driver the drivers built, its readiness and its backlog.
type queueModule struct{ *queue.Module }

// Routes registers nothing: a queue is not reachable over HTTP.
func (queueModule) Routes(*fhttp.Router) {}

// taskModule is the scheduler under this package's contract.
type taskModule struct{ *scheduling.TaskModule }

// Routes registers nothing: a scheduled task is not reachable over HTTP.
func (taskModule) Routes(*fhttp.Router) {}
