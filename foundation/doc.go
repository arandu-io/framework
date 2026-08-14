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
// # Builtins does not exist, and that is decided
//
// docs/31:190 gave this package a Builtins() []console.Command -- what a
// project's own binary answers without the aru CLI. ADR 0050 refused it, and
// the shortest of its three reasons is that the name is not Illuminate's: ADR
// 0044 requires the Laravel name, and Builtins was invented by the
// specification.
//
// What Laravel has in that place is ArtisanServiceProvider, which registers 110
// commands into the container -- the mechanism ADR 0001 and ADR 0002 rejected.
// Of the eleven commands a project dispatches today, only serve, routes and
// Version are answerable from an Application at all; the rest need a *data.DB
// it does not hold, project code it does not know, or a queue store from a
// module that imports this one. Letting them register themselves would be a
// container under another name.
//
// What is written instead is the switch in the skeleton's bootstrap/console.go.
// It is in the project's own repository, the whole set reads at once, and
// adding one is writing a case.
package foundation
