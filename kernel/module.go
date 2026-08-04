package kernel

import (
	"context"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/security"
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
type Module interface {
	// Name is the stable identifier of the module: a lowercase slug, no spaces.
	Name() string

	// Routes registers the module's HTTP routes.
	Routes(r *httpx.Router)
}

// Bootable is optional: implement it when the module needs to prepare state at
// boot -- open a pool, warm a cache, register codecs.
type Bootable interface {
	Boot(ctx context.Context) error
}

// Closable is optional: implement it to release resources on shutdown.
type Closable interface {
	Close(ctx context.Context) error
}

// Diagnostic is optional: the module reports what it knows about the state of
// the system, in sentences a person can act on.
//
// It feeds the error page. The most useful hint is often about something that
// happened outside the failing request -- the outbox stuck for four minutes, a
// job that has not run -- and a page that only looks at the request cannot see
// any of it.
//
// Return nothing when there is nothing wrong. A diagnosis that always says
// something is a diagnosis nobody reads.
type Diagnostic interface {
	Diagnose(ctx context.Context) []string
}

// Locker is a distributed lock, for work that must happen once across replicas.
//
// It lives here because two things need it -- the outbox relay and the
// scheduler -- and two identical interfaces in two packages is the duplication
// that the second one would create. github.com/arandu-io/kv implements it.
//
// Nil is correct for a single replica and wrong for two. What it costs is
// duplicate work, which every task here has to tolerate anyway.
type Locker interface {
	Run(ctx context.Context, name string, ttl time.Duration, fn func(context.Context) error) error
}

// Scope says whether a task runs once or once per tenant.
type Scope int

const (
	// Global runs the task once for the whole instance.
	//
	// It gets the zero Grant, because SystemGrant refuses an empty tenant
	// (RULE 14) -- so a global task cannot pass any Check and cannot reach a
	// repository. That is a constraint rather than an oversight: global work is
	// cleaning temporary files, warming a cache, checking a certificate. Work
	// that reads a customer's rows is PerTenant, and having to say so is the
	// point.
	Global Scope = iota
	// PerTenant expands the task to every active tenant, each with its own
	// Grant and its own lock.
	PerTenant
)

// Task is scheduled work.
//
// The shape mirrors Migrations(): the module declares, the kernel collects, and
// nothing runs until something asks. What a module never does is start its own
// goroutine.
type Task struct {
	// ID identifies the task in logs, in `aru schedule:list` and in the lock.
	// It is stable: changing it starts a new task rather than renaming one.
	ID string
	// Spec is a five-field cron expression: minute hour day month weekday.
	Spec string
	// Scope decides Global or PerTenant.
	Scope Scope
	// Timeout bounds one run, and is also the lock TTL: a process that dies
	// holding the lock releases it when the timeout passes.
	Timeout time.Duration
	// Singleton takes the distributed lock, so exactly one replica runs it.
	// Set it false only for work that is harmless to do N times.
	Singleton bool
	// Action is what the run is authorized for. It becomes the SystemGrant the
	// task receives, so a task reaches repositories the same way a request
	// does -- there is no unauthorized path from the scheduler either.
	Action security.Action
	// Run does the work. It gets the Grant built from Action and the tenant.
	Run func(ctx context.Context, g security.Grant) error
}

// Schedulable is optional: the module declares its scheduled work.
type Schedulable interface {
	Schedule() []Task
}

// Migratable is optional: the module declares its migrations, and the Kernel
// collects them from every registered module in registration order.
type Migratable interface {
	Migrations() []Migration
}

// Migration is a versioned, immutable-once-published schema change.
//
// It is an alias, not a copy: the migration runner lives in the data package,
// and a module must be able to hand its migrations straight to it.
type Migration = data.Migration

// Health is optional and feeds `aru doctor` and the /_arandu/health endpoint.
type Health interface {
	Health(ctx context.Context) error
}
