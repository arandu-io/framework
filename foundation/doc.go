// Package foundation boots the application.
//
// It is the single place where an application is composed. One difference
// matters: the Application is built ONCE, at process start, not per request, so
// nothing here may assume request scope.
//
// # Where Application came from
//
// [Application] was kernel.Kernel until this package landed, and
// github.com/arandu-io/framework/kernel is now the old name pointing here.
// Nothing in this file has a hesape/foundation counterpart.
//
// # What is aliased and what is declared
//
// The composition vocabulary is github.com/arandu-io/hesape/foundation and is
// aliased, not restated: Bootable, Background, Closable, Diagnostic, Health,
// Schedulable, Migratable, Commandable, CSRFExempt, Scope, Global, PerTenant,
// Task, Migration and ReloadTagger are the hesape types under the same names. A
// module written against either name is one type to the compiler.
//
// The publishing vocabulary arrives the same way: Publishable, Publication,
// PublishTag and the six tag constants are aliases, and [PublishTags] and
// [Publications] are call-throughs for the reason [FormatRoutes] is one. What
// is declared here instead is [Application.Publications], the collection over
// the registered modules, which has no counterpart up there for the reason
// RendererProvider has none: it names the boot sequence, and that is here.
//
// The names declared here each have a reason recorded on the declaration in
// module.go:
//
//	Module            names a *http.Router, and hesape/foundation.Module names
//	                  a *routing.Router -- http.Router is the one envelope the
//	                  request bridge could not make an alias
//	RendererProvider  hesape/foundation has none, deliberately: it names the
//	                  boot sequence that looks for it, and that sequence is here
//	Locker            it is retired, and the events bridge kept it --
//	                  hesape/cache.Locks cannot be built from a Locker, and it
//	                  is a *cache.Locks that hesape/events asks for
//	OccurrenceClaimer
//	                  the additive durable capability the scheduler discovers
//	                  dynamically on a Locker
//	OccurrenceClaimOutcome
//	                  separates acquired and already-claimed from storage errors
//	                  without inspecting their text
//	Ready             reports whether this process should receive traffic and
//	                  has no hesape/foundation counterpart
//
// [FormatRoutes] is neither: it is a call through to
// hesape/routing.FormatRoutes, which the alias on http.Route makes possible
// without translating anything.
//
// # The configuration and the console
//
// An application is configured in the shape of a builder and answers its
// command line through a kernel, and both are here:
//
//	drivers, err := foundation.NewDrivers(cfg.Settings)
//	app, err := foundation.Configure(drivers).WithProviders(...).WithRouting(...).Create()
//	os.Exit(app.HandleCommand(ctx, os.Args[1:]))
//
// [NewDrivers] builds every driver the typed configuration names -- the
// database, the cache stores and the shared lock, the session store and the
// CSRF issuer, the rate limiter, the queue, the mailer and the disks -- so an
// application changes a driver by changing its configuration, never by writing
// a factory. [Configure] composes the Application over them, and
// [Application.HandleCommand] dispatches against one list: the built-in
// commands, written in one file of this package, the application's own, and
// those of every [Commandable] module, each name declared once. It is not a
// registry and nothing is discovered: the built-ins are a closed set, the
// application hands its commands to WithCommands, and a module declares its own
// by implementing an interface the Application asks for by type assertion.
package foundation
