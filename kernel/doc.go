// Package kernel boots the application.
//
// It is the single place where an application is composed. One difference
// matters: the Kernel boots ONCE, at process start, not per request, so
// nothing here may assume request scope.
//
// This package is a bridge. It is removed in v1.0.0; import
// github.com/arandu-io/framework/foundation directly.
//
// The boot sequence moved to github.com/arandu-io/framework/foundation, and
// this package is now the old names pointing at it. Unlike every other bridge in
// the collection, this one does not point at github.com/arandu-io/hesape: it
// points one directory across.
//
// The death date above is what keeps this from being a second way to import one
// type. Nothing here holds an implementation: where the name and the signature
// survived the move it is a Go alias, and where the design diverged it is an
// envelope that translates and nothing more.
//
// # The one rename, and the two wrappers
//
//	Kernel        is foundation.Application; the old name stays here as an alias
//	New           a wrapper and not an alias, because Go has no alias form for a
//	              function. The signature is unchanged: it still takes
//	              config.Config and still answers *Kernel
//	FormatRoutes  the same, and what it now calls through to is
//	              hesape/routing.FormatRoutes
//
// Everything else is an alias, and most of them are aliases of aliases: the
// module vocabulary that foundation forwards to
// github.com/arandu-io/hesape/foundation arrives here under the old names
// unchanged, so a module compiled against kernel.Migratable and one compiled
// against hesape/foundation.Migratable satisfy the same interface.
//
// Two names foundation declares rather than forwards, and they keep their old
// meaning here exactly: Module, whose Routes still takes a *http.Router, and
// Locker, which is retired and which the events bridge kept because
// github.com/arandu-io/kv asserts against it.
package kernel
