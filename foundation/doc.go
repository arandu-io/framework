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
// Schedulable, Migratable, Scope, Global, PerTenant, Task, Migration and
// ReloadTagger are the hesape types under the same names. A module written
// against either name is one type to the compiler.
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
// # There is no list of built-in commands
//
// This package answers no Builtins() []console.Command -- the set a project's
// own binary would dispatch without the aru CLI. Of the eleven commands a
// project dispatches today, only serve, routes and Version are answerable from
// an Application at all; the rest need a *data.DB it does not hold, project code
// it does not know, or a queue store from a module that imports this one.
// Letting them register themselves would be a container under another name, and
// there is no container.
//
// What is written instead is the switch in the skeleton's bootstrap/console.go.
// It is in the project's own repository, the whole set reads at once, and
// adding one is writing a case.
package foundation
