package test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/hesape/auth"
)

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

	// base and remote are set on a client of a running server, which sends
	// every request over the socket to base instead of calling handler.
	base   string
	remote *http.Client

	// subject is who this client is, when ActingAs said so. It is a pointer
	// because the zero Subject is a meaningful value -- the session somebody
	// forgot to load, which Authorize refuses -- and nil has to mean "nobody
	// said", not "somebody empty".
	subject *auth.Subject

	// lastBody is the page this client loaded most recently, and the CSRF token
	// is read out of it. It is a field and not a package variable: two clients
	// in one test would otherwise post each other's tokens, and t.Parallel would
	// make that a race rather than a wrong answer.
	lastBody string

	// headers are what WithHeader added, sent on every request from here on.
	headers http.Header
}

// NewClient returns a client over a handler.
func NewClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	return &Client{t: t, handler: h}
}

// ActingAs makes every request from here on carry the subject, and returns the
// client so it reads as one line:
//
//	test.NewClient(t, app).ActingAs(alice).Get("/invoices").OK()
//
// The subject travels under auth.WithSubject, which is the one key a policy
// reads. There is no second context key and no test-only guard: this is the
// same value the edge middleware writes when it loads a real session, so a
// handler cannot tell a test apart from a request, which is the only way the
// test proves anything.
//
// It is applied to the outgoing request, before any middleware runs. A
// middleware that loads a real session therefore wins, and that ordering is the
// right one -- a session that exists beats a pretend one -- but it also means
// ActingAs and signing in through the form do not quietly combine. Pick one per
// test: the sign-in flow is tested by posting the form, everything downstream
// of it by acting as somebody.
//
// A subject with no id is refused here rather than three assertions later.
// Authorize refuses it too, so every request would come back forbidden with
// nothing saying why. auth.Guest is how a test says "anonymous reader" on
// purpose.
//
// A client of a running server cannot carry a subject on a context the server
// never sees, and refuses.
//
// Deprecated: the subject reaches the handler without passing the guard, the
// session store or the resolver, so roles written in the test are roles nobody
// re-reads and a demotion never takes effect. Use [As], which starts a real
// session for the account and lets the application's guards load it.
func (c *Client) ActingAs(s auth.Subject) *Client {
	c.t.Helper()
	if c.remote != nil {
		c.t.Fatalf("ActingAs cannot reach a server over the network: the subject would travel on a " +
			"context the server never sees. Sign in with test.As.")
	}
	if s.ID == "" && !s.IsGuest() {
		c.t.Fatalf("ActingAs was handed a subject with no id; " +
			"that is the session nobody loaded, Authorize refuses it, and every " +
			"request would answer 403. Use auth.Guest(tenant) for an anonymous reader.")
	}
	if s.Tenant == "" {
		c.t.Fatalf("ActingAs was handed a subject with no tenant; " +
			"every query is scoped by auth.Tenant(g) and a subject without one reaches no rows (RULE 14).")
	}
	c.subject = &s
	return c
}

// WithHeader makes every request from here on carry the header, and returns the
// client so it reads as one line:
//
//	test.NewClient(t, app).WithHeader("HX-Request", "true").Delete("/invoices/1", nil)
//
// It is sticky rather than an argument for the same reason the cookie jar is a
// field: the headers a test needs to set describe the client, not the call. A
// page that boosts sends HX-Request on everything it fires, and the token a
// generated view puts in hx-headers sits on the body element, so every request
// leaving that page carries it. Passed per call it would have to be repeated on
// every line, and the line that forgot it would be exercising a different
// client than the ones around it.
//
// An empty value removes the header, which is how a test says "and this one is
// not an HTMX request" after saying the others were.
//
// What is set here wins over what the client would send itself -- the
// Content-Type of a form and the X-CSRF-Token read off the last page are both
// replaceable. That is deliberate: refusing a stolen token and refusing a body
// that is not the shape it claims are both behaviour a test has to be able to
// provoke, and neither can be provoked by a client that always knows better.
func (c *Client) WithHeader(name, value string) *Client {
	if value == "" {
		c.headers.Del(name)
		return c
	}
	if c.headers == nil {
		c.headers = http.Header{}
	}
	c.headers.Set(name, value)
	return c
}

// Get sends a GET.
func (c *Client) Get(path string) *Response { return c.do(http.MethodGet, path, nil, "", "") }

// Options sends an OPTIONS, which carries no body for the same reason a GET
// does not: it asks what may be done with the address rather than doing it.
func (c *Client) Options(path string) *Response {
	return c.do(http.MethodOptions, path, nil, "", "")
}

// Post sends a form.
//
// The CSRF token is read off the last page this client loaded and sent with the
// request, because that is what a browser does -- and a test that skips it is a
// test that proves the form works with protection disabled.
func (c *Client) Post(path string, form map[string]string) *Response {
	return c.submit(http.MethodPost, path, form)
}

// Put sends a form as a PUT: the whole replacement of what is at the address.
//
// It carries the token and the cookies exactly as [Client.Post] does. A
// generated resource registers PUT beside POST, so a client without this leaves
// a route of every generated module unreachable -- and a test that reaches for
// POST instead proves something about a route the application does not have.
func (c *Client) Put(path string, form map[string]string) *Response {
	return c.submit(http.MethodPut, path, form)
}

// Patch sends a form as a PATCH: the part of what is at the address that
// changed. It carries the token and the cookies exactly as [Client.Post] does.
func (c *Client) Patch(path string, form map[string]string) *Response {
	return c.submit(http.MethodPatch, path, form)
}

// Delete sends a DELETE, with a form when one is given and a nil form when
// there is nothing to say beyond the address.
//
// A nil form still carries the token, because the guard in front of a delete is
// the guard that is in front of a post: a request that reaches it without one is
// refused, and a client that could not send one would need the guard turned off
// to pass.
func (c *Client) Delete(path string, form map[string]string) *Response {
	return c.submit(http.MethodDelete, path, form)
}

// submit is what the four form-carrying verbs share: the method is the only
// thing that differs between them, so it is the only thing they pass.
//
// The token goes in the header as well as in the body, and the header is the
// half that matters here. net/http parses a request body into the form only for
// POST, PUT and PATCH -- a DELETE body is never parsed, whatever it holds -- so
// a delete that carried the token only in the body would arrive at the guard
// carrying nothing. That is the same reason the generated markup puts the token
// in hx-headers rather than in a field: the request that deletes has no form.
func (c *Client) submit(method, path string, form map[string]string) *Response {
	c.t.Helper()

	// Encoded, not joined: a value holding &, =, + or % would otherwise
	// arrive as a different form -- a password cut at its ampersand, a plus
	// read as a space -- and the test would be asserting about input nobody
	// typed. A _token the test put in the form replaces the one read off the
	// page, for the reason a header it sets does.
	token := c.token()
	values := url.Values{}
	for k, v := range form {
		values.Set(k, v)
	}
	body := values.Encode()
	if _, own := form["_token"]; token != "" && !own {
		field := "_token=" + url.QueryEscape(token)
		if body != "" {
			field += "&"
		}
		body = field + body
	}
	return c.do(method, path, strings.NewReader(body),
		"application/x-www-form-urlencoded", token)
}

// Cookies returns the cookies this client holds, the session among them, as
// the server last set them. They are what a browser is handed to be signed in
// as the same person.
func (c *Client) Cookies() []*http.Cookie {
	out := make([]*http.Cookie, len(c.cookies))
	for i, ck := range c.cookies {
		copied := *ck
		out[i] = &copied
	}
	return out
}

func (c *Client) token() string {
	const marker = `name="_token" value="`
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

func (c *Client) do(method, path string, body io.Reader, contentType, csrfToken string) *Response {
	c.t.Helper()

	var req *http.Request
	if c.remote != nil {
		var err error
		if req, err = http.NewRequestWithContext(c.t.Context(), method, c.base+path, body); err != nil {
			c.t.Fatalf("%s %s: %v", method, path, err)
		}
	} else {
		req = httptest.NewRequest(method, path, body)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if csrfToken != "" {
		req.Header.Set("X-CSRF-Token", csrfToken)
	}
	// Last, so a header the test set replaces the one the client inferred. See
	// WithHeader for why that is the way round it is.
	for name, values := range c.headers {
		req.Header[name] = append([]string(nil), values...)
	}
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}
	if c.subject != nil {
		req = req.WithContext(auth.WithSubject(req.Context(), *c.subject))
	}

	rec := httptest.NewRecorder()
	if c.remote != nil {
		c.send(rec, req)
	} else {
		c.handler.ServeHTTP(rec, req)
	}

	// Cookies the response set are kept, so a session survives the next call.
	// Without this every request after a login is anonymous, and the test that
	// notices is the one that fails three cases later.
	for _, ck := range rec.Result().Cookies() {
		c.keep(ck)
	}
	c.lastBody = rec.Body.String()

	return &Response{t: c.t, rec: rec, path: method + " " + path}
}

// send makes the request over the socket and writes what came back into rec,
// so a response from a running server is asserted about exactly as one from a
// handler called in process.
func (c *Client) send(rec *httptest.ResponseRecorder, req *http.Request) {
	c.t.Helper()
	resp, err := c.remote.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", req.Method, req.URL.RequestURI(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	for name, values := range resp.Header {
		rec.Header()[name] = values
	}
	rec.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(rec, resp.Body); err != nil {
		c.t.Fatalf("%s %s: reading the body: %v", req.Method, req.URL.RequestURI(), err)
	}
}

// keep stores one Set-Cookie the way a browser stores it: by name and path, so
// a second value REPLACES the first, and a cookie the server told the browser to
// drop is dropped.
//
// It used to append, and a jar that only appends cannot express a sign-out.
// http.Request.Cookie returns the first cookie of a name, so the cleared value
// that logout sends sat behind the live one and was never read, and a second
// sign-in in the same test put a second session behind the first -- which by
// then the server had deleted, so the client silently went back to being
// anonymous while the test read on. Both of those are tests that pass while
// proving the opposite of what they say, which is the one failure this whole
// package exists to prevent.
func (c *Client) keep(ck *http.Cookie) {
	kept := c.cookies[:0]
	for _, have := range c.cookies {
		if have.Name != ck.Name || have.Path != ck.Path {
			kept = append(kept, have)
		}
	}
	c.cookies = kept

	// MaxAge below zero is "delete this now", and an Expires in the past is the
	// older spelling of the same instruction. Either way there is nothing to
	// store: the cookie is gone from the jar and the next request carries none.
	if ck.MaxAge < 0 || (!ck.Expires.IsZero() && !ck.Expires.After(time.Now())) {
		return
	}
	c.cookies = append(c.cookies, ck)
}

// Response is what came back, with the assertions worth having.
type Response struct {
	t    *testing.T
	rec  *httptest.ResponseRecorder
	path string
}

// AssertStatus fails unless the status is the one expected.
//
// The body is printed on failure, because the answer to "why is this a 500" is
// in it and finding out otherwise means running the test again with a print.
func (r *Response) AssertStatus(want int) *Response {
	r.t.Helper()
	if r.rec.Code != want {
		r.t.Fatalf("%s answered %d, want %d\n%s", r.path, r.rec.Code, want, r.body())
	}
	return r
}

// AssertOk asserts a 200 status. It is [Response.AssertStatus] with that code.
func (r *Response) AssertOk() *Response { return r.AssertStatus(http.StatusOK) }

// AssertSee fails unless the text is in the body.
func (r *Response) AssertSee(text string) *Response {
	r.t.Helper()
	if !strings.Contains(r.rec.Body.String(), text) {
		r.t.Errorf("%s does not show %q\n%s", r.path, text, r.body())
	}
	return r
}

// AssertDontSee fails when the text is in the body.
//
// It is the half people skip, and the half that catches a leak: a draft in a
// public listing, an address in a page that should not name one, a button
// somebody without the permission can see.
func (r *Response) AssertDontSee(text string) *Response {
	r.t.Helper()
	if strings.Contains(r.rec.Body.String(), text) {
		r.t.Errorf("%s shows %q and should not", r.path, text)
	}
	return r
}

// AssertRedirect fails unless the response sends the client to that address.
//
// It reads HX-Redirect as well as Location, because a handler answering an HTMX
// request redirects with a header and a 204 -- and a test that only reads
// Location says "redirected to \"\"" for a response that is correct.
func (r *Response) AssertRedirect(want string) *Response {
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

// GetContent returns the response body, for an assertion this package does not
// have.
func (r *Response) GetContent() string { return r.rec.Body.String() }

// Header reads one response header.
func (r *Response) Header(name string) string { return r.rec.Header().Get(name) }

func (r *Response) body() string {
	body := r.rec.Body.String()
	if len(body) > 2000 {
		return body[:2000] + "\n… (truncated)"
	}
	return body
}
