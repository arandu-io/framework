// Package errorpage renders the development error page.
//
// The data comes from the Collector, which is part of the core, so the page
// knows the queries, the dumps, the events and the routes without any extra
// package installed.
//
// Absolute rule: nothing in this package may be reachable when Env is not dev.
// The Recover middleware is the only caller, and it checks the flag first.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/exception directly.
//
// The page moved to github.com/arandu-io/hesape/exception, where it is one part
// of the exception handler rather than a package of its own: the same Handler
// that decides a failure is a 404 draws the page for the one that is a defect.
// Two things came out of that, and both are why half of this file is envelope
// rather than alias:
//
//   - Render and RenderDump are no longer exported. They are methods on the
//     Handler, and the Handler is reached through Recover, the Displayer or
//     Render(err). Both functions below therefore build a Handler and drive it.
//   - Options is not Config. The hesape struct carries five more fields -- Dev,
//     Views, DontReport, RenderJSONWhen, Console -- and this one may not grow
//     them without changing what the thirteen repositories construct, so
//     Options stays declared here and is translated.
//
// StackFrame and Capture came across whole. Capture decides "framework or
// application" by asking about hesape's own import path and about the standard
// library, and collapses everything that is neither only when Options.AppModule
// says which module the application is. See Capture for what that leaves.
//
// The death date above is what keeps this from being a second way to import one
// type.
package errorpage
