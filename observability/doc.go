// Package observability is the reason this framework exists.
//
// slog covers structured logging and OpenTelemetry covers production tracing.
// What is missing between the two is the development layer: the moment a request
// broke and you want the stack, the queries with their timing, what you dumped
// and what the framework thinks is wrong, on one screen.
//
// The Collector is that layer, and it is core rather than a plugin, so the error
// page knows the queries, the dumps and the routes without any extra install.
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/log directly.
//
// The components moved to github.com/arandu-io/hesape, under new names, and
// this package is now the old names pointing at them. Everything here is
// answered by one package:
//
//	hesape/log  the logger, the Collector, the Recorder, the Console, the
//	            outbound transport, Dump and the editor links
//
// The error page is the one thing that did not go with it: it renders a failure
// rather than recording one, so it went to hesape/exception, and the bridge for
// it is the errorpage subpackage.
//
// The death date above is what keeps this from being a second way to import one
// type. With one exception, named below, nothing here holds an implementation:
// where the name and the signature survived the move it is a Go alias, and where
// the design diverged it is a call through that translates and nothing more.
//
// The exception is Gauges, which has no counterpart in hesape/log. It is the
// current value of a number the process owns, and the Collector and the Recorder
// are both scoped to one request, so it belongs to neither. It is written here
// because this is the observability layer a project imports; it needs a home
// that outlives this package before the date above arrives.
//
// The four renames, which are the whole of the divergence:
//
//	NewLogger   hesape/log.New
//	WithLogger  hesape/log.Into
//	Log         hesape/log.For
//	RootLogger  hesape/log.Middleware
//
// Two behaviours changed with the move, and a caller can tell:
//
//   - DumpDie now aborts the request everywhere, not only where a Collector
//     exists. The recording half still needs one; the die half no longer waits
//     for it, because a forgotten call that answered 200 with the dump written
//     into the body was a page broken in a way nothing reported.
//   - EditorLink returns "" for an unset or unknown editor instead of a
//     vscode:// link nothing is registered to open, and it takes an optional
//     path rewrite this package's signature has no room for.
package observability
