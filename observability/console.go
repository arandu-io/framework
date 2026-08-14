// The debug console, answered by github.com/arandu-io/hesape/log.
//
// The page itself moved with the type: console_page.go was the html/template
// that Console.Handler executes, it holds nothing a caller can name, and it is
// gone from here rather than kept as a second copy of the same markup.

package observability

import hlog "github.com/arandu-io/hesape/log"

// ConsolePath is where the console is mounted.
const ConsolePath = hlog.ConsolePath

// TracingHeader carries the secret that turns tracing on outside development,
// and that the console requires to answer there.
//
// A constant rather than a string in three places: the middleware reads it, the
// kernel gates on it, and `aru trace` sends it. Three literals is three chances
// to change one and not the others.
const TracingHeader = hlog.TracingHeader

// Console serves the request inspector at /_arandu/debug.
//
// This is the Telescope equivalent, and it is core rather than a package you
// install. The reason is the thesis of the product: a framework whose selling
// point is "the debugger names the probable cause" cannot ship the debugger as
// an optional dependency that half the projects never add.
//
// It renders with html/template and no assets, like the error page and for the
// same reason: it has to work when the rest is broken, including when the view
// build failed.
type Console = hlog.Console

// NewConsole returns the console over a recorder.
func NewConsole(r *Recorder, editor string) *Console {
	return hlog.NewConsole(r, editor)
}
