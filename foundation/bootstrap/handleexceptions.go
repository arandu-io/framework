package bootstrap

import (
	"context"

	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/exception"
)

// HandleExceptions is Illuminate\Foundation\Bootstrap\HandleExceptions: it
// builds the handler that answers when something fails.
//
// Laravel's version installs three global hooks -- set_error_handler,
// set_exception_handler, register_shutdown_function -- because in PHP an
// uncaught error terminates the request wherever it happens and the only way to
// see it is to be registered globally.
//
// Go has no such hook and needs none. An error is a value that travels back
// through the call stack, and the one thing that does escape -- a panic -- is
// caught by exception.Recover, a middleware, at a place in the pipeline that is
// visible in bootstrap/app.go. So this bootstrapper builds the handler and
// returns it; installing it is the application wiring it, not a side effect
// nobody can see.
//
// # What the AppModule is for, and why it is not guessed
//
// The debug page separates the frames of your code from the frames of the
// framework, and it tells them apart by module path prefix. Passing it in is
// what makes that work in a project whose module is example.test/loja.
//
// It used to be derived from a hard-coded constant, and the constant said
// github.com/arandu-io/framework. Once the implementation moved into the
// collection, every hesape frame started reading as application code on the one
// screen where being wrong costs the most.
func HandleExceptions(cfg Configuration, appModule string, diagnose func(context.Context) []string) *exception.Handler {
	return exception.NewHandler(exception.Config{
		// Debug follows the application, and the application refuses to be in
		// debug in production -- hesape/config.App.Validate does that at Load,
		// which is why this can read the field rather than checking again.
		Dev: cfg.App.Debug,

		// The editor a stack frame links to. vscode by default because it is
		// what most people have; the link is only ever built in development.
		Editor: config.String("ARANDU_EDITOR", "vscode"),

		AppModule: appModule,
		Diagnose:  diagnose,
	})
}
