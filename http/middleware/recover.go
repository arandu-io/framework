// Package middleware holds the mandatory request pipeline.
//
// Order matters and is not a matter of taste: Recover must be the outermost
// middleware, or a panic raised in any other middleware escapes without a page;
// Observe must come right after it, because everything below depends on the
// context it builds.
package middleware

import (
	"net/http"

	"github.com/arandu-io/framework/observability/errorpage"
	"github.com/arandu-io/hesape/exception"
)

// Recover captures panics and decides what to render.
//
// In development: the full debug page -- stack, request, queries, dumps.
// Anywhere else: the status page, which leaks nothing and carries the request
// id so the operator can correlate it with the structured log.
//
// This function is a bridge. It is removed in v1.0.0; build the handler with
// foundation/bootstrap.HandleExceptions and install
// github.com/arandu-io/hesape/exception.Recover:
//
//	h := bootstrap.HandleExceptions(cfg, AppModule, app.Diagnose)
//	app.Use(exception.Recover(h), ...)
//
// Both spellings end in the same exception.Recover, so they catch the same
// things and draw the same pages. What differs is what is left afterwards: this
// one builds the Handler from three fields and drops it, so there is no object
// to register an Error, Missing or Fatal callback on, none to hand the
// application's own error views to, and none to name the errors that must never
// reach the log. HandleExceptions returns that Handler, and the application
// keeps it.
//
// The death date above is what keeps this from being a second way to install
// one middleware.
func Recover(dev bool, opts errorpage.Options) func(http.Handler) http.Handler {
	// Built here and never elsewhere. The panic path used to be written out a
	// second time in this file -- its own deferred recover, its own dump-die
	// branch, its own bare-500 -- and a second implementation of one job drifts
	// in the direction of whichever copy is read less. This one had lost the
	// escape hatch that lets a panic reach the test that provoked it, answered
	// every panic as 500 including the ones carrying a status of their own, and
	// dropped the headers an error asked to carry, so a 429 went out with no
	// Retry-After.
	return exception.Recover(exception.NewHandler(exception.Config{
		Dev:       dev,
		Editor:    opts.Editor,
		AppModule: opts.AppModule,
		Diagnose:  opts.Diagnose,
	}))
}
