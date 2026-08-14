// The browser and the assertions, answered by
// github.com/arandu-io/hesape/arandutest, and -- for the subject a test acts as
// -- by github.com/arandu-io/hesape/auth.
//
// This is where the design diverged, so this is where the envelopes are. Every
// assertion on Response was renamed to the Illuminate spelling on the way to
// hesape, and thirteen modules call the old names, so Response keeps them and
// forwards. Client is an envelope for one reason only: its Get and Post answer
// that Response.

package arandutest

import (
	"context"
	"net/http"
	"testing"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/arandutest"
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
// hesape/arandutest.Client. This type holds nothing but that one.
type Client struct {
	inner *arandutest.Client
}

// NewClient returns a client over a handler.
func NewClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	return &Client{inner: arandutest.NewClient(t, h)}
}

// Get sends a GET.
func (c *Client) Get(path string) *Response { return &Response{inner: c.inner.Get(path)} }

// Post sends a form.
//
// The CSRF token is read off the last page this client loaded and sent with the
// body, because that is what a browser does -- and a test that skips it is a
// test that proves the form works with protection disabled.
func (c *Client) Post(path string, form map[string]string) *Response {
	return &Response{inner: c.inner.Post(path, form)}
}

// Response is what came back, with the assertions worth having.
//
// Renamed on the way to hesape: every method below is spelled Assert* there,
// after Illuminate\Testing\TestResponse. The old names are kept because
// thirteen modules call them, and each one forwards to exactly one hesape
// method -- the comparison, the failure message and whether it stops the test
// are all decided by the code that runs there.
type Response struct {
	inner *arandutest.Response
}

// Status fails unless the status is the one expected.
//
// The body is printed on failure, because the answer to "why is this a 500" is
// in it and finding out otherwise means running the test again with a print.
//
// Renamed on the way to hesape: it is Response.AssertStatus there.
func (r *Response) Status(want int) *Response {
	r.inner.AssertStatus(want)
	return r
}

// OK is Status(200).
//
// Renamed on the way to hesape: it is Response.AssertOk there.
func (r *Response) OK() *Response {
	r.inner.AssertOk()
	return r
}

// See fails unless the text is in the body.
//
// Renamed on the way to hesape: it is Response.AssertSee there.
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
// Renamed on the way to hesape: it is Response.AssertDontSee there.
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
// Renamed on the way to hesape: it is Response.AssertRedirect there.
func (r *Response) RedirectsTo(want string) *Response {
	r.inner.AssertRedirect(want)
	return r
}

// Body is what came back, for an assertion this package does not have.
//
// Renamed on the way to hesape: it is Response.GetContent there.
func (r *Response) Body() string { return r.inner.GetContent() }

// Header reads one response header.
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
// hesape/arandutest.Client.ActingAs, which puts the subject on the outgoing
// request.
//
// Deleted on the way to hesape, and this is the one behaviour the bridge
// changes on purpose. The old implementation wrote a context key of its own
// that no policy, no repository and no middleware ever read: it authenticated
// nothing, so a test written against it passed while proving the opposite of
// what it said. It now writes auth.WithSubject, which is the key the edge
// middleware writes and every policy reads. The signature is untouched --
// security.Subject is an alias for auth.Subject -- so nothing recompiles
// differently.
func ActingAs(ctx context.Context, s security.Subject) context.Context {
	return auth.WithSubject(ctx, s)
}

// Subject reads back what ActingAs put in, and reports whether there was one.
//
// The second result is false when nothing put one there, which is a different
// fact from an anonymous reader: a public page has the Subject that
// security.Guest built.
//
// Renamed on the way to hesape: it is auth.SubjectFrom there, and it is not in
// hesape/arandutest at all, because reading the subject out of a context is not
// a test-only question.
func Subject(ctx context.Context) (security.Subject, bool) { return auth.SubjectFrom(ctx) }
