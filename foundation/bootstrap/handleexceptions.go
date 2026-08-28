package bootstrap

import (
	"context"

	"github.com/arandu-io/hesape/exception"
)

// HandleExceptions builds the handler that answers when something fails.
//
// It installs no process-wide hook and needs none. An error is a value that
// travels back through the call stack, and the one thing that does escape -- a
// panic -- is caught by exception.Recover, a middleware, at a place in the
// pipeline that is visible in bootstrap/app.go. So this bootstrapper builds the
// handler and returns it; installing it is the application wiring it, not a side
// effect nobody can see.
//
// # Installing it
//
// Two lines in bootstrap/app.go, and Recover stays outermost because a panic
// raised in any middleware above it escapes without a page:
//
//	h := bootstrap.HandleExceptions(fw, AppModule, app.Diagnose)
//	app.Use(exception.Recover(h), middleware.Observe(...), ...)
//
// Keeping h is the point of returning it. The handler is what the application
// registers its own answers on -- Error, Missing and Fatal for the failures it
// wants to draw itself, Views for error pages of its own, DontReport for the
// errors that must never reach the log -- and all of them are read on every
// request afterwards. http/middleware.Recover builds a handler from three
// fields and drops it, so none of that is reachable through it; it is the
// bridge, and it goes in v1.0.0.
//
// # Which page it draws
//
// The debug page is drawn when App.Debug is set, and that is the only thing
// read -- the environment is not consulted here. An application arriving from
// the middleware, which takes the flag as an argument, was most likely passing
// whether the environment is development. The two answer the same only while
// APP_DEBUG is left unset, so a deployment that sets it sees the page move: not
// development with APP_DEBUG set now draws the stack, and development with
// APP_DEBUG=false now draws the status page.
//
// # What the AppModule is for, and why it is not guessed
//
// The debug page separates the frames of your code from the frames of the
// framework, and it tells them apart by module path prefix. Passing it in is
// what makes that work in a project whose module is example.test/loja.
//
// A hard-coded constant cannot do it. With the implementation in the
// collection, every hesape frame would read as application code on the one
// screen where being wrong costs the most.
func HandleExceptions(cfg Configuration, appModule string, diagnose func(context.Context) []string) *exception.Handler {
	return exception.NewHandler(exception.Config{
		// Debug follows the application, and the application refuses to be in
		// debug in production -- hesape/config.App.Validate does that at Load,
		// which is why this can read the field rather than checking again.
		Dev: cfg.App.Debug,

		// The editor a stack frame links to, taken from the Configuration
		// rather than read again here. The variable has one reader, and this
		// page and the debug console link to the same editor because they are
		// handed the same value.
		Editor: cfg.Observability.Editor,

		AppModule: appModule,
		Diagnose:  diagnose,
	})
}
