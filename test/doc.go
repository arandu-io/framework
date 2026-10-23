// Package test holds the helpers a test needs and an application must
// not use.
//
// It is a browser (Client), the assertions worth having about what came back
// (Response: the status, the headers, the redirect, the cookies, a download,
// the body as HTML and as the text a person reads, and a JSON answer), the
// assertions worth having about what landed in the database
// (AssertDatabaseHas, AssertDatabaseMissing, AssertDatabaseCount,
// AssertSoftDeleted, AssertNotSoftDeleted), the assertion about how many
// statements a block ran (AssertQueryCount), and the two pieces a test needs to
// say something about domain events (DrainOutbox, Collected).
//
// Serve boots the application the way its binary boots, on a free port of
// 127.0.0.1 and over a database of the test's own -- a copy of a migrated
// Postgres template, or a temporary SQLite file -- and stops it when the test
// ends. Server.Client sends requests over that socket; a Client from NewClient
// calls a handler in process.
//
// Every application writes the same six things: send a request, do not follow
// the redirect, keep the cookie, act as somebody, assert about the HTML that
// came back, assert about the row that was written.
//
// # Nothing here is a second implementation
//
// Everything drives the same code path production drives. There is no
// synchronous mode, no in-memory substitute and no test double for the outbox:
// "sync only in tests" is a second way to do one thing, and the second way
// always leaks into production -- usually on the day somebody copies a test
// into a handler.
//
// The same rule is why acting as somebody is As. It starts a session on the
// application's own session store, the call its sign-in makes, and every
// request afterwards is loaded by the application's guards and answered by
// its SubjectResolver, so a role taken away in the test is gone on the next
// request. Client.ActingAs, which puts a subject straight onto the request
// under auth.WithSubject, skips the guard and the resolver, and is deprecated
// for it. A package-level ActingAs used to live here writing a context key of
// its own, which no policy, no repository and no middleware ever read: it
// authenticated nothing, and a test written against it passed while proving
// the opposite of what it said. Code that calls
// a service, a job or a seeder directly does not need this package for it --
// auth.WithSubject is the whole answer, and it is the real one.
//
// # The database assertions read without a Grant, on purpose
//
// These five do, and that is what they are for: they check what the
// Grant-protected path actually wrote, so they cannot be the same path or they
// would prove nothing. They take a table name and a match, never an entity,
// and they never appear outside a _test.go.
//
// Because they read the table and not the tenant's slice of it, a match that
// omits the tenant column proves the row exists somewhere. In a multi-tenant
// application that is rarely the assertion anybody means: put the tenant in the
// match.
//
// # The name
//
// It is spelled test rather than testing because an import segment called
// "testing" shadows the standard library package that every _test.go imports;
// the precedent is net/http/httptest. The databases it creates keep the
// arandutest_ prefix, which is what the cleanup drops and nothing else is
// named.
//
// # What it deliberately does not carry
//
// There is no assertion language over JSON here: what these handlers answer
// with is HTML fragments, and [Response.AssertSee] over the fragment is the
// assertion that matches. There is no console command runner and no
// view-level assertion.
package test
