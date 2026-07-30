package kernel

import (
	"context"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/httpx"
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
