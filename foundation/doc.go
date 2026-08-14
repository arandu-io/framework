// Package foundation boots the application.
//
// It is the single place where an application is composed. One difference
// matters: the Application is built ONCE, at process start, not per request, so
// nothing here may assume request scope.
//
// # What it answers to in Laravel
//
// Illuminate\Foundation, which is the one part of laravel/framework that ships
// nowhere else: there is no illuminate/foundation on Packagist, and there is no
// hesape/foundation counterpart for what is in this file either. [Application]
// is Illuminate\Foundation\Application, under the name a Laravel developer
// types. It was kernel.Kernel until this package landed, and
// github.com/arandu-io/framework/kernel is now the old name pointing here.
//
// # What is aliased and what is declared
//
// The composition vocabulary is github.com/arandu-io/hesape/foundation and is
// aliased, not restated: Bootable, Background, Closable, Diagnostic, Health,
// Schedulable, Migratable, Scope, Global, PerTenant, Task, Migration and
// ReloadTagger are the hesape types under the same names. A module written
// against either name is one type to the compiler.
//
// Three names are declared here, each for a reason recorded on the declaration
// in module.go:
//
//	Module            names a *http.Router, and hesape/foundation.Module names
//	                  a *routing.Router -- http.Router is the one envelope the
//	                  request bridge could not make an alias
//	RendererProvider  hesape/foundation has none, deliberately: it names the
//	                  boot sequence that looks for it, and that sequence is here
//	Locker            docs/31:191 retires it, and the events bridge reached that
//	                  line first and kept it -- hesape/cache.Locks cannot be
//	                  built from what github.com/arandu-io/kv implements
//
// [FormatRoutes] is neither: it is a call through to
// hesape/routing.FormatRoutes (docs/31:192), which the alias on http.Route
// makes possible without translating anything.
//
// # Builtins is not here, and needs a decision rather than code
//
// docs/31:190 gives this package a Builtins() []console.Command -- what a
// project's own binary answers on the command line without the aru CLI. It is
// not declared, because the shape cannot be derived from what an Application
// holds, and a half-design occupying the slot is worse than an empty one
// (RULE 9). What is undecided, precisely:
//
//   - Of the eleven commands bootstrap/console.go dispatches today, only serve,
//     routes and Version are answerable from an Application. migrate,
//     migrate:rollback, migrate:status and migrate:fresh need a *data.DB, which
//     an Application does not hold and does not open; db:seed is project code;
//     schedule:list and schedule:run need the scheduler module the project
//     registered last.
//   - work needs a queue store from github.com/arandu-io/queue, a separate
//     module that imports this one. The framework cannot import it back, so
//     either the command does not come from here or the Application grows a
//     registration hook -- which is a container by another name (ADR 0001).
//   - Whatever answers it has to take a *data.DB or a connection from
//     somewhere, and config.Config is what would carry it. That file is frozen
//     for this change, and it is the seventeen-typed-config front that decides
//     the shape.
//
// The decision belongs in an ADR, next to the one that folds the aru commands
// into console.Command values. Until it exists, a project's binary keeps its
// own switch and nothing here pretends otherwise.
package foundation
