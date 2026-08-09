package arandutest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/security"
)

// What a feature test repeats, and why it belongs here.
//
// Every application writes the same six things: send a request, follow a
// redirect or not, keep the cookie, act as somebody, and assert about the HTML
// that came back. Laravel calls that $this->get(), ->actingAs() and
// ->assertSee(), and it is in the framework because a project that writes its
// own writes it slightly differently -- and the one that matters, acting as a
// subject, is the one most easily written wrong.

// Client is a browser for a test: it keeps cookies and it does not follow
// redirects.
//
// Not following them is deliberate. A test that asserts about a page after a
// redirect cannot tell a 200 from a 302 that happened to land somewhere with the
// same words on it, and "it redirected me to the sign-in screen" is the most
// common way a feature test passes while proving the opposite of what it says.
type Client struct {
	t       *testing.T
	handler http.Handler
	cookies []*http.Cookie

	// lastBody is the page this client loaded most recently, and the CSRF token
	// is read out of it. It is a field and not a package variable: two clients
	// in one test would otherwise post each other's tokens, and t.Parallel would
	// make that a race rather than a wrong answer.
	lastBody string
}

// NewClient returns a client over a handler.
func NewClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	return &Client{t: t, handler: h}
}

// Get sends a GET.
func (c *Client) Get(path string) *Response { return c.do(http.MethodGet, path, nil, "") }

// Post sends a form.
//
// The CSRF token is read off the last page this client loaded and sent with the
// body, because that is what a browser does -- and a test that skips it is a
// test that proves the form works with protection disabled.
func (c *Client) Post(path string, form map[string]string) *Response {
	values := make([]string, 0, len(form)+1)
	if token := c.token(); token != "" {
		values = append(values, "_csrf="+token)
	}
	for k, v := range form {
		values = append(values, k+"="+strings.ReplaceAll(v, " ", "+"))
	}
	return c.do(http.MethodPost, path, strings.NewReader(strings.Join(values, "&")),
		"application/x-www-form-urlencoded")
}

func (c *Client) token() string {
	const marker = `name="_csrf" value="`
	at := strings.Index(c.lastBody, marker)
	if at < 0 {
		return ""
	}
	rest := c.lastBody[at+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func (c *Client) do(method, path string, body io.Reader, contentType string) *Response {
	c.t.Helper()

	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}

	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)

	// Cookies the response set are kept, so a session survives the next call.
	// Without this every request after a login is anonymous, and the test that
	// notices is the one that fails three cases later.
	c.cookies = append(c.cookies, rec.Result().Cookies()...)
	c.lastBody = rec.Body.String()

	return &Response{t: c.t, rec: rec, path: method + " " + path}
}

// Response is what came back, with the assertions worth having.
type Response struct {
	t    *testing.T
	rec  *httptest.ResponseRecorder
	path string
}

// Status fails unless the status is the one expected.
//
// The body is printed on failure, because the answer to "why is this a 500" is
// in it and finding out otherwise means running the test again with a print.
func (r *Response) Status(want int) *Response {
	r.t.Helper()
	if r.rec.Code != want {
		r.t.Fatalf("%s answered %d, want %d\n%s", r.path, r.rec.Code, want, r.body())
	}
	return r
}

// OK is Status(200).
func (r *Response) OK() *Response { return r.Status(http.StatusOK) }

// See fails unless the text is in the body.
func (r *Response) See(text string) *Response {
	r.t.Helper()
	if !strings.Contains(r.rec.Body.String(), text) {
		r.t.Errorf("%s does not show %q\n%s", r.path, text, r.body())
	}
	return r
}

// DontSee fails when the text is in the body.
//
// It is the half people skip, and the half that catches a leak: a draft in a
// public listing, an address in a page that should not name one, a button
// somebody without the permission can see.
func (r *Response) DontSee(text string) *Response {
	r.t.Helper()
	if strings.Contains(r.rec.Body.String(), text) {
		r.t.Errorf("%s shows %q and should not", r.path, text)
	}
	return r
}

// RedirectsTo fails unless the response sends the client to that address.
//
// It reads HX-Redirect as well as Location, because a handler answering an HTMX
// request redirects with a header and a 204 -- and a test that only reads
// Location says "redirected to \"\"" for a response that is correct.
func (r *Response) RedirectsTo(want string) *Response {
	r.t.Helper()

	got := r.rec.Header().Get("Location")
	if got == "" {
		got = r.rec.Header().Get("HX-Redirect")
	}
	if got != want {
		r.t.Errorf("%s redirected to %q, want %q", r.path, got, want)
	}
	return r
}

// Body is what came back, for an assertion this package does not have.
func (r *Response) Body() string { return r.rec.Body.String() }

// Header reads one response header.
func (r *Response) Header(name string) string { return r.rec.Header().Get(name) }

func (r *Response) body() string {
	body := r.rec.Body.String()
	if len(body) > 2000 {
		return body[:2000] + "\n… (truncated)"
	}
	return body
}

// ActingAs is a context carrying a subject, for the code paths that take one
// directly rather than through a session.
//
// A service, a job and a seeder are all called with a Subject. A test that wants
// to be somebody for one call does not need a browser, and building a session
// for it is machinery that proves nothing about what is being tested.
//
// It does NOT put anybody in a session: a request made with Client after this is
// still anonymous, and it has to be, or the two ways of being somebody would
// disagree.
func ActingAs(ctx context.Context, s security.Subject) context.Context {
	return context.WithValue(ctx, subjectKey{}, s)
}

// Subject reads back what ActingAs put in, and reports whether there was one.
func Subject(ctx context.Context) (security.Subject, bool) {
	s, ok := ctx.Value(subjectKey{}).(security.Subject)
	return s, ok
}

type subjectKey struct{}
