// The boot sequence, answered by github.com/arandu-io/framework/foundation.

package kernel

import (
	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/framework/foundation/bootstrap"
	"github.com/arandu-io/framework/http"
	"github.com/arandu-io/hesape/cache"
)

// Kernel holds the composed application: configuration, modules, the global
// middleware pipeline and the router.
//
// It is foundation.Application under its old name. The alias is what keeps it
// one type rather than two: a *Kernel handed to something written against
// *foundation.Application is the same value, with the same methods -- Boot,
// Run, Handler, Shutdown, Register, Use, Migrations, Tasks, Diagnose, Routes,
// Recorder, Config, Logger -- and none of them are restated here.
type Kernel = foundation.Application

// New assembles the kernel. It opens no connection and listens on no port --
// that is Boot and Run.
//
// A wrapper and not an alias: Go has no alias form for a function.
//
// It takes the settings LoadConfiguration answers, one struct per component.
// The single struct it used to take is gone from the boot path, and keeping the
// old signature here would have meant a translation between the two -- a second
// answer to what an application is configured with, which is the thing this
// bridge exists to retire rather than to preserve.
func New(cfg bootstrap.Configuration) *Kernel { return foundation.New(cfg) }

// FormatRoutes renders the route table for the terminal, grouped by module and
// sorted by pattern. It is here, and not in the CLI, so that every project
// prints the same table.
//
// A wrapper for the same reason New is. What it reaches is
// hesape/routing.FormatRoutes, through foundation.
func FormatRoutes(routes []*http.Route) string { return foundation.FormatRoutes(routes) }

// NewLocker returns a Locker that takes each lock from locks.
//
// It is what makes a Singleton task and the outbox relay run on exactly one
// replica, and it is the only thing in the framework that produces the shape
// both of them take.
//
//	sched.Locker = kernel.NewLocker(cache.NewLocks(store))
//
// A wrapper for the same reason New is.
func NewLocker(locks *cache.Locks) Locker { return foundation.NewLocker(locks) }
