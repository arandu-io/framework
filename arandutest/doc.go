// Package arandutest holds the helpers a test needs and an application must not
// use.
//
// It is a browser (Client), the assertions worth having about what came back
// (Response), and the two pieces a test needs to say something about domain
// events (DrainOutbox, Collected).
//
// This package is a bridge. It is removed in v1.0.0; import github.com/arandu-io/hesape/arandutest directly.
//
// The helpers moved to github.com/arandu-io/hesape/arandutest -- the spelling is
// arandutest there too, because an import segment called "testing" shadows the
// standard library package every _test.go imports, and the precedent is
// net/http/httptest. What is left here is the old names pointing at them.
//
// The death date above is what keeps this from being a second way to import one
// helper, which RULE 9 forbids. Nothing here holds an implementation of the
// browser or of the assertions: the cookie jar, the CSRF token read off the last
// page, and every comparison a Response makes run in hesape.
//
// Two hesape packages answer for it, and which one depends on the symbol:
//
//	hesape/arandutest  Client, Response, DrainOutbox, Collected
//	hesape/auth        the subject a test acts as
//
// # What diverged
//
// Nothing here is a plain alias, and the reasons are worth naming:
//
//	Response   every assertion was renamed to the Illuminate spelling --
//	           Status becomes AssertStatus, OK becomes AssertOk, See becomes
//	           AssertSee, DontSee becomes AssertDontSee, RedirectsTo becomes
//	           AssertRedirect, Body becomes GetContent. The envelope keeps the
//	           old names and forwards
//	Client     Get and Post answer the Response above, so the client that
//	           returns them is an envelope as well
//	ActingAs   hesape deleted the package-level form and put the subject under
//	           auth.WithSubject, which is the key a policy actually reads. The
//	           old signature is kept and now writes that key
//	Subject    the reader for the above; it is auth.SubjectFrom in hesape
//
// # What is still framework-side, and why
//
// DrainOutbox and Collected are declared here rather than forwarded, because
// their parameters are framework/events types and hesape/events.Outbox is not
// the same type as framework/events.Outbox: the hesape outbox takes a DB
// interface that reports its own transaction, the framework one takes
// *data.DB. Until framework/events is deleted outright, a call through would
// have nothing to pass. Collected becomes a one-line alias the day
// framework/events.Stored is one.
//
// # What hesape has and this bridge does not re-export
//
// hesape/arandutest also has AssertDatabaseHas, AssertDatabaseMissing,
// AssertDatabaseCount, Match and Client.ActingAs. None of them ever existed
// under this import path, and adding them here would be a second place to learn
// the new API while the old one is being removed. Import hesape/arandutest for
// those.
package arandutest
