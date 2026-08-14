// The boot sequence, answered by github.com/arandu-io/framework/foundation.

package kernel

import (
	"github.com/arandu-io/framework/config"
	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/framework/http"
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
// A wrapper and not an alias: Go has no alias form for a function. The
// signature is the one it has always had, and that is deliberate -- it is
// called from bootstrap/app.go in every project built on this framework, and a
// bridge that changes a signature is not a bridge.
func New(cfg config.Config) *Kernel { return foundation.New(cfg) }

// FormatRoutes renders the route table for the terminal, grouped by module and
// sorted by pattern. It is here, and not in the CLI, so that every project
// prints the same table.
//
// A wrapper for the same reason New is. What it reaches is
// hesape/routing.FormatRoutes, through foundation (docs/31:192).
func FormatRoutes(routes []*http.Route) string { return foundation.FormatRoutes(routes) }
