package webhook

import (
	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/framework/http"
	hwebhook "github.com/arandu-io/hesape/webhook"
)

// Module registers a native webhook module on the Framework router.
//
// It holds the native module and nothing else. The services, the publisher,
// the job handlers and the receive paths are read from the native module the
// application built; this type exists only because the kernel hands a
// registered module a *http.Router, which the native Routes does not take.
type Module struct {
	native *hwebhook.Module
}

var (
	_ foundation.Module      = (*Module)(nil)
	_ foundation.Migratable  = (*Module)(nil)
	_ foundation.Schedulable = (*Module)(nil)
)

// NewModule returns the module that registers native on the Framework router.
//
// native is the module hesape's New built, with the guard its management
// routes sit behind already chosen. A nil native is refused with a panic,
// because it is a wiring mistake that would otherwise surface as a nil
// dereference when the kernel boots.
func NewModule(native *hwebhook.Module) *Module {
	if native == nil {
		panic("webhook: NewModule was given a nil module. Build it with github.com/arandu-io/hesape/webhook.New and pass the result")
	}
	return &Module{native: native}
}

// Name returns the native module's identifier, so routes, migrations and
// tasks are attributed to the same name whichever router registers them.
func (m *Module) Name() string { return m.native.Name() }

// Routes registers every action the native module lists, in its order, with
// the router's own action adapter.
//
// Each action keeps its method, its path, its middleware and its name exactly
// as the native module gives them, so a URL generated from a route name and
// the guard in front of a management route are the same as on the native
// router. What differs is who answers an error the action returns: this
// router's adapter, which sends a page that failed validation back with the
// messages in the flash, and answers every other status the way the native
// adapter does.
func (m *Module) Routes(r *http.Router) {
	for _, action := range m.native.HTTPActions() {
		r.Action(action.Method, action.Path, action.Action, action.Middleware...).Name(action.Name)
	}
}

// Migrations returns the native module's migrations, unchanged, for the
// application to collect and run outside the process start.
func (m *Module) Migrations() []foundation.Migration { return m.native.Migrations() }

// Schedule returns the native module's scheduled tasks, unchanged.
func (m *Module) Schedule() []foundation.Task { return m.native.Schedule() }
