// The composition vocabulary, answered by github.com/arandu-io/hesape/foundation.
//
// Everything a module declares -- the optional interfaces, the scheduled task,
// the migration -- is an alias to the hesape declaration, so a module written
// against either name is one type to the compiler. Three names are declared
// here instead, and each has a reason recorded on it: Module, RendererProvider
// and Locker.

package foundation

import (
	"context"
	"time"

	"github.com/arandu-io/framework/http"
	hfoundation "github.com/arandu-io/hesape/foundation"
)

// Module is the only unit of composition in the framework.
//
// A module is a directory. It registers its own routes, its own migrations and
// its own dependency graph. There is no injection container and no reflection
// based resolution: the wiring is explicit, and the CLI generates the file that
// instantiates everything.
//
// Every third-party module implements this interface and nothing else. It is the
// public contract of the framework -- change it and the whole ecosystem breaks,
// so change it with great care.
//
// # Why this one is declared and not aliased
//
// hesape/foundation.Module names a *routing.Router, and this one names a
// *http.Router. Those are two types: http.Router is the one envelope the
// request bridge could not turn into an alias -- it carries the renderer and
// the flash, and hesape/routing.Router deliberately carries neither -- so the
// two interfaces cannot be the same interface while that envelope exists.
//
// Aliasing this name to the hesape one would change the signature every module
// in every project is written against, in the two repositories this framework
// ships and in every application built on it. A bridge that changes a signature
// is not a bridge. It becomes an alias the day http.Router stops being an
// envelope, and not before.
type Module interface {
	// Name is the stable identifier of the module: a lowercase slug, no spaces.
	Name() string

	// Routes registers the module's HTTP routes.
	Routes(r *http.Router)
}

// Bootable is optional: implement it when the module needs to prepare state at
// boot -- open a pool, warm a cache, register codecs.
//
// Boot wires; it does not run. A module that needs a loop of its own implements
// Background instead, and validates in Boot whatever would make that loop fail.
type Bootable = hfoundation.Bootable

// Background is optional: the module runs a loop of its own -- the scheduler
// and the outbox relay do.
//
// Start is called by Run, never by Boot, and that distinction is the difference
// between a process that serves and a process that does something else.
//
// The alias drops one thing this interface used to say: it embedded Module,
// and hesape/foundation.Background declares Start alone. Nothing changes at a
// call site -- Register takes a Module, so anything the Application asks for a
// loop is already one -- and the embed was a second statement of a fact the
// registration already enforced.
type Background = hfoundation.Background

// RendererProvider is optional: the module supplies the view renderer.
//
// The view package implements it. This package cannot import that package -- it
// implements Module, so the import would be a cycle -- and an application
// calling a wiring function by hand is a line somebody forgets. An optional
// interface solves both: the Application asks every module whether it brings a
// renderer, before any route is registered.
//
// Two modules providing one is a wiring mistake, and the Application refuses to
// boot rather than pick one.
//
// # Why this one is declared and not aliased
//
// hesape/foundation has no counterpart: its doc says so in as many words, and
// the reason is that declaring one from up there would be a second definition
// of the boot sequence that looks for it. The renderer type is not the
// divergence -- http.Renderer is an alias for hesape/http.Renderer, which is
// what hesape/view.Module.Renderer already returns, so that module satisfies
// this interface structurally and without importing this package.
type RendererProvider interface {
	Renderer() http.Renderer
}

// Closable is optional: implement it to release resources on shutdown.
type Closable = hfoundation.Closable

// Diagnostic is optional: the module reports what it knows about the state of
// the system, in sentences a person can act on.
//
// It feeds the error page. The most useful hint is often about something that
// happened outside the failing request -- the outbox stuck for four minutes, a
// job that has not run -- and a page that only looks at the request cannot see
// any of it.
type Diagnostic = hfoundation.Diagnostic

// Locker is a distributed lock, for work that must happen once across replicas.
//
// It lives here because two things need it -- the outbox relay and the
// scheduler -- and two identical interfaces in two packages is the duplication
// that the second one would create.
//
// NewLocker builds one over a lock issuer, and is the only thing here that
// produces this shape. Nil is correct for a single replica and wrong for two.
// What it costs is duplicate work, which every task here has to tolerate
// anyway.
//
// # Why it is still here
//
// It is retired in favour of one kind of lock in the collection, cache.Lock.
// The events bridge did not delete it, and the reason is that the conversion
// only runs one way.
//
// A cache.Locks makes a Locker: NewLocker does it in a line, because naming a
// lock and running a function under it is exactly what Run is. The other
// direction is closed. A Locker takes a lock and gives it back inside a single
// call and never yields the handle, so there is no owner token to read and
// nothing to release from anywhere else -- and those are the operations a
// cache.Locks is. hesape/events.RelayOptions takes the concrete issuer, so
// there is still nothing to alias this name to.
//
// events.Locker is an alias to this name, and the scheduler takes this one, so
// a single value wires into both. Deleting the declaration deletes that wiring
// rather than the duplication.
//
// It moved with the rest of the package instead, and stays one declaration.
type Locker interface {
	Run(ctx context.Context, name string, ttl time.Duration, fn func(context.Context) error) error
}

// Scope says whether a task runs once or once per tenant.
type Scope = hfoundation.Scope

const (
	// Global runs the task once for the whole instance.
	//
	// It gets the zero Grant, because SystemGrant refuses an empty tenant -- so
	// a global task cannot pass any Check and cannot reach a repository. That
	// is a constraint rather than an oversight: global work is cleaning
	// temporary files, warming a cache, checking a certificate. Work that reads
	// a customer's rows is PerTenant, and having to say so is the point.
	Global = hfoundation.Global
	// PerTenant expands the task to every active tenant, each with its own
	// Grant and its own lock.
	PerTenant = hfoundation.PerTenant
)

// Task is scheduled work.
//
// The shape mirrors Migrations(): the module declares, the Application
// collects, and nothing runs until something asks. What a module never does is
// start its own goroutine.
//
// The alias holds because Task.Action and Task.Run name security.Action and
// security.Grant, and both of those are already aliases for the hesape/auth
// types the hesape declaration uses.
type Task = hfoundation.Task

// Schedulable is optional: the module declares its scheduled work.
type Schedulable = hfoundation.Schedulable

// Migratable is optional: the module declares its migrations, and the
// Application collects them from every registered module in registration order.
type Migratable = hfoundation.Migratable

// Migration is a versioned, immutable-once-published schema change.
//
// It is an alias, not a copy: the migration runner lives in hesape, and a module
// must be able to hand its migrations straight to it. Both sides resolve to
// hesape/database/migrations.Migration, which is what data.Migration already
// aliases.
//
// It is an interface -- embed migrations.BaseMigration and only GetName and Up
// are left to write.
type Migration = hfoundation.Migration

// Health is optional and feeds `aru doctor` and the /_arandu/health endpoint.
type Health = hfoundation.Health

// ReloadTagger is what a module implements to supply the development
// live-reload tag.
//
// Optional, and asked for the same way the renderer is: this package cannot
// import the view package -- that package imports this one in order to be a
// module -- so what it needs arrives through an interface declared there and
// satisfied here. The Application supplies the address of its own endpoint,
// because the route is its own, and two constants for one address is how a
// client and a server come to disagree about it.
type ReloadTagger = hfoundation.ReloadTagger
