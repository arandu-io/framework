package webhook

import (
	"errors"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/validation"
	hwebhook "github.com/arandu-io/hesape/webhook"
)

// Module registers a native webhook module on the Framework router.
//
// It holds the native module and nothing else. The services, the publisher,
// the job handlers and the receive paths are read from the native module the
// application built; this type exists only because the kernel hands a
// registered module a *http.Router, which the native Routes does not take.
//
// Deprecated: register the native module as it is, with
// foundation.Component(native). The kernel hands it the router its Routes
// takes, and its migrations, schedule and routes are read from it directly.
// The native routes answer as an API -- a rejection is a 422 problem keyed by
// field, never a flash -- which is what a management route and a callback are.
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
// Deprecated: use foundation.Component(native).
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
// adapter does. A field in withheldFields is never part of what the flash
// carries back.
func (m *Module) Routes(r *http.Router) {
	for _, action := range m.native.HTTPActions() {
		r.Action(action.Method, action.Path, withholding(action.Action), action.Middleware...).Name(action.Name)
	}
}

// withheldFields are the fields of the management forms whose value never goes
// into the flash. An endpoint's headers are "Name: value" lines that carry the
// credentials the endpoint is called with -- an Authorization, an API key --
// and the flash cookie is signed, not encrypted.
var withheldFields = []string{"headers"}

// withholding removes withheldFields from the posted form when the action
// answers a rejected input, which is the one answer the router puts the form
// into the flash for. The body is read first, as the router would read it, so
// what is removed stays removed when the router reads it again.
func withholding(action func(*http.Context) error) func(*http.Context) error {
	return func(ctx *http.Context) error {
		err := action(ctx)
		var rejected validation.Errors
		if err == nil || !errors.As(err, &rejected) {
			return err
		}
		_ = ctx.Input(withheldFields[0])
		for _, field := range withheldFields {
			ctx.Request.PostForm.Del(field)
			ctx.Request.Form.Del(field)
		}
		return err
	}
}

// Migrations returns the native module's migrations, unchanged, for the
// application to collect and run outside the process start.
func (m *Module) Migrations() []foundation.Migration { return m.native.Migrations() }

// Schedule returns the native module's scheduled tasks, unchanged.
func (m *Module) Schedule() []foundation.Task { return m.native.Schedule() }
