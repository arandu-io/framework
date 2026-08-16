// The composition vocabulary, answered by
// github.com/arandu-io/framework/foundation -- and through it, for everything
// except Module, RendererProvider and Locker, by
// github.com/arandu-io/hesape/foundation.

package kernel

import "github.com/arandu-io/framework/foundation"

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
type Module = foundation.Module

// Bootable is optional: implement it when the module needs to prepare state at
// boot -- open a pool, warm a cache, register codecs.
//
// Boot wires; it does not run. A module that needs a loop of its own implements
// Background instead, and validates in Boot whatever would make that loop fail.
type Bootable = foundation.Bootable

// Background is optional: the module runs a loop of its own -- the scheduler
// and the outbox relay do.
//
// Start is called by Run, never by Boot, and that distinction is the difference
// between a process that serves and a process that does something else.
//
// It no longer embeds Module: hesape/foundation.Background declares Start
// alone. Nothing changes at a call site, because Register takes a Module and
// anything the kernel asks for a loop is already one.
type Background = foundation.Background

// RendererProvider is optional: the module supplies the view renderer.
//
// The view package implements it. The kernel cannot import that package -- it
// implements Module, so the import would be a cycle -- and an application
// calling a wiring function by hand is a line somebody forgets. An optional
// interface solves both: the kernel asks every module whether it brings a
// renderer, before any route is registered.
//
// Two modules providing one is a wiring mistake, and the kernel refuses to boot
// rather than pick one.
type RendererProvider = foundation.RendererProvider

// Closable is optional: implement it to release resources on shutdown.
type Closable = foundation.Closable

// Diagnostic is optional: the module reports what it knows about the state of
// the system, in sentences a person can act on.
//
// It feeds the error page. The most useful hint is often about something that
// happened outside the failing request -- the outbox stuck for four minutes, a
// job that has not run -- and a page that only looks at the request cannot see
// any of it.
type Diagnostic = foundation.Diagnostic

// Locker is a distributed lock, for work that must happen once across replicas.
//
// It lives in foundation because two things need it -- the outbox relay and the
// scheduler -- and two identical interfaces in two packages is the duplication
// that the second one would create. github.com/arandu-io/kv implements it, and
// events.Locker is an alias to this name.
//
// Nil is correct for a single replica and wrong for two. What it costs is
// duplicate work, which every task here has to tolerate anyway.
type Locker = foundation.Locker

// Scope says whether a task runs once or once per tenant.
type Scope = foundation.Scope

const (
	// Global runs the task once for the whole instance.
	//
	// It gets the zero Grant, because SystemGrant refuses an empty tenant -- so
	// a global task cannot pass any Check and cannot reach a repository. That
	// is a constraint rather than an oversight: global work is cleaning
	// temporary files, warming a cache, checking a certificate. Work that reads
	// a customer's rows is PerTenant, and having to say so is the point.
	Global = foundation.Global
	// PerTenant expands the task to every active tenant, each with its own
	// Grant and its own lock.
	PerTenant = foundation.PerTenant
)

// Task is scheduled work.
//
// The shape mirrors Migrations(): the module declares, the kernel collects, and
// nothing runs until something asks. What a module never does is start its own
// goroutine.
type Task = foundation.Task

// Schedulable is optional: the module declares its scheduled work.
type Schedulable = foundation.Schedulable

// Migratable is optional: the module declares its migrations, and the Kernel
// collects them from every registered module in registration order.
type Migratable = foundation.Migratable

// Migration is a versioned, immutable-once-published schema change.
//
// It is an alias, not a copy: the migration runner lives in the data package,
// and a module must be able to hand its migrations straight to it.
type Migration = foundation.Migration

// Health is optional and feeds `aru doctor` and the /_arandu/health endpoint.
type Health = foundation.Health

// ReloadTagger is what a module implements to supply the development
// live-reload tag.
//
// Optional, and asked for the same way the renderer is: the kernel cannot
// import the view package -- that package imports this one in order to be a
// Module -- so what it needs arrives through an interface declared there and
// satisfied here. The kernel supplies the address of its own endpoint, because
// the route is its own, and two constants for one address is how a client and a
// server come to disagree about it.
type ReloadTagger = foundation.ReloadTagger
