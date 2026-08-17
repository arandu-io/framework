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
