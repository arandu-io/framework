// The browser and the assertions, answered by
// github.com/arandu-io/framework/test, and -- for the subject a test acts as
// -- by github.com/arandu-io/hesape/auth.
//
// This is where the design diverged, so this is where the envelopes are. Every
// assertion on Response was renamed on the way to framework/test, so Response
// keeps the old names and forwards. Client is an envelope for one reason only: its Get and
// Post answer that Response.

package arandutest

import (
	"context"
	"net/http"
	"testing"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/test"
	"github.com/arandu-io/hesape/auth"
)

// Client is a browser for a test: it keeps cookies and it does not follow
// redirects.
//
// Not following them is deliberate. A test that asserts about a page after a
// redirect cannot tell a 200 from a 302 that happened to land somewhere with the
// same words on it, and "it redirected me to the sign-in screen" is the most
// common way a feature test passes while proving the opposite of what it says.
//
// The jar, the CSRF token read off the last page, and the rule that a second
// Set-Cookie of a name replaces the first all live in
// framework/test.Client. This type holds nothing but that one.
//
// It is declared here for one reason: Get and Post answer this package's
// Response, which keeps the assertion names the kit renamed. It becomes an
// alias the day Response does, and not before.
//
// Deprecated: use Client from github.com/arandu-io/framework/test.
type Client struct {
	inner *test.Client
}

// NewClient returns a client over a handler.
//
// Deprecated: use NewClient from github.com/arandu-io/framework/test.
func NewClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	return &Client{inner: test.NewClient(t, h)}
}

// Get sends a GET.
//
// Deprecated: use Client.Get from github.com/arandu-io/framework/test.
func (c *Client) Get(path string) *Response { return &Response{inner: c.inner.Get(path)} }

// Post sends a form.
//
// The CSRF token is read off the last page this client loaded and sent with the
// body, because that is what a browser does -- and a test that skips it is a
// test that proves the form works with protection disabled.
//
// Deprecated: use Client.Post from github.com/arandu-io/framework/test.
func (c *Client) Post(path string, form map[string]string) *Response {
	return &Response{inner: c.inner.Post(path, form)}
}

// Response is what came back, with the assertions worth having.
//
// Renamed on the way to framework/test: every method below is spelled Assert* there,
// and Body is GetContent. Each one forwards to exactly one method of the kit -- the
// comparison, the failure message and whether it stops the test are all decided
// by the code that runs there.
//
// It is a declaration rather than an alias because of what an alias would carry:
// the new names, and only those. Every existing call of Status, OK, See,
// DontSee, RedirectsTo and Body would stop compiling on the day this type
// changed shape, which is the one thing a bridge exists to prevent. That is the
// condition, not a count of callers -- one call left anywhere is enough for it
// to hold, and it stops holding only when there are none.
//
// Deprecated: use Response from github.com/arandu-io/framework/test,
// whose assertions are spelled Assert*.
type Response struct {
	inner *test.Response
}

// Status fails unless the status is the one expected.
//
// The body is printed on failure, because the answer to "why is this a 500" is
// in it and finding out otherwise means running the test again with a print.
//
// Renamed on the way to framework/test: it is Response.AssertStatus there.
//
// Deprecated: use Response.AssertStatus from github.com/arandu-io/framework/test.
func (r *Response) Status(want int) *Response {
	r.inner.AssertStatus(want)
	return r
}

// OK is Status(200).
//
// Renamed on the way to framework/test: it is Response.AssertOk there.
//
// Deprecated: use Response.AssertOk from github.com/arandu-io/framework/test.
func (r *Response) OK() *Response {
	r.inner.AssertOk()
	return r
}

// See fails unless the text is in the body.
//
// Renamed on the way to framework/test: it is Response.AssertSee there.
//
// Deprecated: use Response.AssertSee from github.com/arandu-io/framework/test.
func (r *Response) See(text string) *Response {
	r.inner.AssertSee(text)
	return r
}

// DontSee fails when the text is in the body.
//
// It is the half people skip, and the half that catches a leak: a draft in a
// public listing, an address in a page that should not name one, a button
// somebody without the permission can see.
//
// Renamed on the way to framework/test: it is Response.AssertDontSee there.
//
// Deprecated: use Response.AssertDontSee from github.com/arandu-io/framework/test.
func (r *Response) DontSee(text string) *Response {
	r.inner.AssertDontSee(text)
	return r
}

// RedirectsTo fails unless the response sends the client to that address.
//
// It reads HX-Redirect as well as Location, because a handler answering an HTMX
// request redirects with a header and a 204 -- and a test that only reads
// Location says "redirected to \"\"" for a response that is correct.
//
// Renamed on the way to framework/test: it is Response.AssertRedirect there.
//
// Deprecated: use Response.AssertRedirect from github.com/arandu-io/framework/test.
func (r *Response) RedirectsTo(want string) *Response {
	r.inner.AssertRedirect(want)
	return r
}

// Body is what came back, for an assertion this package does not have.
//
// Renamed on the way to framework/test: it is Response.GetContent there.
//
// Deprecated: use Response.GetContent from github.com/arandu-io/framework/test.
func (r *Response) Body() string { return r.inner.GetContent() }

// Header reads one response header.
//
// Deprecated: use Response.Header from github.com/arandu-io/framework/test.
func (r *Response) Header(name string) string { return r.inner.Header(name) }

// ActingAs is a context carrying a subject, for the code paths that take one
// directly rather than through a session.
//
// A service, a job and a seeder are all called with a Subject. A test that wants
// to be somebody for one call does not need a browser, and building a session
// for it is machinery that proves nothing about what is being tested.
//
// It does NOT put anybody in a session: a request made with Client after this is
// still anonymous, and it has to be, or the two ways of being somebody would
// disagree. A test that wants a client to be somebody uses
// framework/test.Client.ActingAs, which puts the subject on the outgoing
// request.
//
// Deleted on the way to framework/test: there is no package-level form there,
// and a subject goes under auth.WithSubject -- the key the edge middleware writes and
// every policy reads. This is one call to it and nothing else, so the name is
// the only thing left here that the kit does not spell the same way. The
// signature is untouched, because security.Subject is an alias for auth.Subject.
//
// It used to hold a context key of its own that no policy, no repository and no
// middleware ever read, which authenticated nothing: a test written against it
// passed while proving the opposite of what it said. Naming that is worth a line
// because it is the reason to reach for auth.WithSubject and not for a key that
// looks equivalent.
//
// Deprecated: sign a test in with As from github.com/arandu-io/framework/test,
// which goes through the session; a call that takes a context directly is
// handed the subject with auth.WithSubject from github.com/arandu-io/hesape/auth.
func ActingAs(ctx context.Context, s security.Subject) context.Context {
	return auth.WithSubject(ctx, s)
}

// Subject reads back what ActingAs put in, and reports whether there was one.
//
// The second result is false when nothing put one there, which is a different
// fact from an anonymous reader: a public page has the Subject that
// security.Guest built.
//
// Renamed: it is auth.SubjectFrom in hesape/auth, and it is not in
// framework/test at all, because reading the subject out of a context is not a
// test-only question.
//
// Deprecated: use auth.SubjectFrom from github.com/arandu-io/hesape/auth; the
// test helpers are in github.com/arandu-io/framework/test.
func Subject(ctx context.Context) (security.Subject, bool) { return auth.SubjectFrom(ctx) }
