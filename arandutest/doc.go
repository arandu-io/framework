// Package arandutest holds the helpers a test needs and an application must not
// use.
//
// It is a browser (Client), the assertions worth having about what came back
// (Response), and the two pieces a test needs to say something about domain
// events (DrainOutbox, Collected).
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/framework/test directly.
//
// Deprecated: import github.com/arandu-io/framework/test, which holds these
// helpers under their new names beside what this package never had: Serve,
// which starts the application on a free port, As, which signs a client in
// through the real session, and the database assertions.
//
// The helpers moved to github.com/arandu-io/framework/test -- spelled test,
// because an import segment called "testing" shadows the standard library
// package every _test.go imports, and the precedent is net/http/httptest. What
// is left here is the old names pointing at them.
//
// The death date above is what keeps this from being a second way to import one
// helper. Nothing here holds an implementation: the cookie jar, the CSRF token
// read off the last page, every comparison a Response makes and the pass the
// outbox drain runs are all the kit's. Where the name and the signature
// survived the move it is a Go alias, and where the design diverged it is an
// envelope that translates and nothing more.
//
// Two packages answer for it, and which one depends on the symbol:
//
//	framework/test  Client, Response, DrainOutbox, Collected
//	hesape/auth     the subject a test acts as
//
// One name is a plain alias. The rest are envelopes, and the divergence each
// one absorbs is worth naming:
//
//	Collected    the alias. It has to satisfy framework/events.Publisher over
//	             framework/events.Stored, and both of those are hesape's own
//	             types under this module's names
//	Response     every assertion was renamed -- Status becomes AssertStatus, OK
//	             becomes AssertOk, See becomes AssertSee, DontSee becomes
//	             AssertDontSee, RedirectsTo becomes AssertRedirect, Body becomes
//	             GetContent. The envelope keeps the old names and forwards
//	Client       Get and Post answer the Response above, so the client that
//	             returns them is an envelope as well
//	ActingAs     the kit has no package-level form, and the subject goes under
//	             auth.WithSubject, which is the key a policy actually reads. The
//	             old signature is kept and writes that key
//	Subject      the reader for the above; it is auth.SubjectFrom in hesape/auth
//	DrainOutbox  a forward and not an alias, so the parameters keep the names
//	             this module spells them with. It carried an implementation for
//	             as long as framework/events.Outbox was a type of its own; that
//	             type is an alias now, so the two signatures name the same types
//	             and there is nothing left to translate
//
// # What the kit has and this bridge does not re-export
//
// framework/test also has Serve, As, AssertDatabaseHas, AssertDatabaseMissing,
// AssertDatabaseCount, Match, the browser, the trace and the conformance kits.
// None of them ever existed under this import path, and adding them here would
// be a second place to learn the new API while the old one is being removed.
// Import framework/test for those.
package arandutest
