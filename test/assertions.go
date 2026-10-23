package test

import (
	"html"
	"net/http"
	"strings"
	"time"

	htesting "github.com/arandu-io/hesape/testing"
)

// The status a handler answered, named. Each is [Response.AssertStatus] with
// that code, so a failure prints the body the same way.

// AssertCreated asserts a 201.
func (r *Response) AssertCreated() *Response { r.t.Helper(); return r.AssertStatus(http.StatusCreated) }

// AssertAccepted asserts a 202.
func (r *Response) AssertAccepted() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusAccepted)
}

// AssertNoContent asserts a 204 and an empty body. A 204 that carries a body is
// a handler writing something nobody reads.
func (r *Response) AssertNoContent() *Response {
	r.t.Helper()
	r.AssertStatus(http.StatusNoContent)
	if r.rec.Body.Len() != 0 {
		r.t.Errorf("%s answered 204 with a body of %d bytes", r.path, r.rec.Body.Len())
	}
	return r
}

// AssertFound asserts a 302.
func (r *Response) AssertFound() *Response { r.t.Helper(); return r.AssertStatus(http.StatusFound) }

// AssertNotModified asserts a 304.
func (r *Response) AssertNotModified() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusNotModified)
}

// AssertBadRequest asserts a 400.
func (r *Response) AssertBadRequest() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusBadRequest)
}

// AssertUnauthorized asserts a 401.
func (r *Response) AssertUnauthorized() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusUnauthorized)
}

// AssertForbidden asserts a 403, which is what a policy that refused answers.
func (r *Response) AssertForbidden() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusForbidden)
}

// AssertNotFound asserts a 404.
func (r *Response) AssertNotFound() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusNotFound)
}

// AssertMethodNotAllowed asserts a 405.
func (r *Response) AssertMethodNotAllowed() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusMethodNotAllowed)
}

// AssertConflict asserts a 409, which is what a duplicate key answers.
func (r *Response) AssertConflict() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusConflict)
}

// AssertGone asserts a 410.
func (r *Response) AssertGone() *Response { r.t.Helper(); return r.AssertStatus(http.StatusGone) }

// AssertUnprocessable asserts a 422, which is what a rejected form answers to a
// request that asked for JSON.
func (r *Response) AssertUnprocessable() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusUnprocessableEntity)
}

// AssertTooManyRequests asserts a 429.
func (r *Response) AssertTooManyRequests() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusTooManyRequests)
}

// AssertInternalServerError asserts a 500.
func (r *Response) AssertInternalServerError() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusInternalServerError)
}

// AssertServiceUnavailable asserts a 503.
func (r *Response) AssertServiceUnavailable() *Response {
	r.t.Helper()
	return r.AssertStatus(http.StatusServiceUnavailable)
}

// AssertSuccessful asserts a status from 200 to 299.
func (r *Response) AssertSuccessful() *Response {
	r.t.Helper()
	if r.rec.Code < 200 || r.rec.Code > 299 {
		r.t.Fatalf("%s answered %d, want a successful status\n%s", r.path, r.rec.Code, r.body())
	}
	return r
}

// AssertServerError asserts a status from 500 to 599.
func (r *Response) AssertServerError() *Response {
	r.t.Helper()
	if r.rec.Code < 500 || r.rec.Code > 599 {
		r.t.Fatalf("%s answered %d, want a server error\n%s", r.path, r.rec.Code, r.body())
	}
	return r
}

// AssertHeader asserts the header is present and, when a value is given, that
// it holds exactly that value.
func (r *Response) AssertHeader(name string, value ...string) *Response {
	r.t.Helper()
	values, ok := r.rec.Header()[http.CanonicalHeaderKey(name)]
	if !ok {
		r.t.Errorf("%s has no %s header", r.path, name)
		return r
	}
	if len(value) > 0 && r.rec.Header().Get(name) != value[0] {
		r.t.Errorf("%s answered %s: %q, want %q", r.path, name, values, value[0])
	}
	return r
}

// AssertHeaderMissing asserts the header is absent.
func (r *Response) AssertHeaderMissing(name string) *Response {
	r.t.Helper()
	if values, ok := r.rec.Header()[http.CanonicalHeaderKey(name)]; ok {
		r.t.Errorf("%s answered %s: %q, and should not have the header", r.path, name, values)
	}
	return r
}

// AssertRedirectContains asserts the response redirects to an address that
// contains fragment, reading HX-Redirect as [Response.AssertRedirect] does.
func (r *Response) AssertRedirectContains(fragment string) *Response {
	r.t.Helper()
	got := r.redirect()
	if got == "" || !strings.Contains(got, fragment) {
		r.t.Errorf("%s redirected to %q, want an address containing %q", r.path, got, fragment)
	}
	return r
}

// redirect is where the response sends the client: Location, or HX-Redirect
// for an HTMX request answered with a 204.
func (r *Response) redirect() string {
	if got := r.rec.Header().Get("Location"); got != "" {
		return got
	}
	return r.rec.Header().Get("HX-Redirect")
}

// AssertCookie asserts the response sets the cookie and, when a value is
// given, that it carries exactly that value -- as it crossed the wire, signed
// or sealed if the application signs or seals it.
func (r *Response) AssertCookie(name string, value ...string) *Response {
	r.t.Helper()
	c := r.cookie(name)
	if c == nil {
		r.t.Errorf("%s does not set the cookie %q", r.path, name)
		return r
	}
	if len(value) > 0 && c.Value != value[0] {
		r.t.Errorf("%s set the cookie %q to %q, want %q", r.path, name, c.Value, value[0])
	}
	return r
}

// AssertCookieMissing asserts the response does not set the cookie.
func (r *Response) AssertCookieMissing(name string) *Response {
	r.t.Helper()
	if c := r.cookie(name); c != nil {
		r.t.Errorf("%s set the cookie %q and should not have", r.path, name)
	}
	return r
}

// AssertCookieExpired asserts the response sets the cookie in the form that
// deletes it -- a negative MaxAge, or an expiry in the past -- which is what a
// sign-out has to send.
func (r *Response) AssertCookieExpired(name string) *Response {
	r.t.Helper()
	c := r.cookie(name)
	if c == nil {
		r.t.Errorf("%s does not set the cookie %q, so nothing expires it", r.path, name)
		return r
	}
	if c.MaxAge >= 0 && (c.Expires.IsZero() || c.Expires.After(time.Now())) {
		r.t.Errorf("%s set the cookie %q without expiring it", r.path, name)
	}
	return r
}

// cookie is the last cookie of that name the response set, or nil.
func (r *Response) cookie(name string) *http.Cookie {
	var found *http.Cookie
	for _, c := range r.rec.Result().Cookies() {
		if c.Name == name {
			found = c
		}
	}
	return found
}

// AssertDownload asserts the response offers a file download and, when a name
// is given, that the file is offered under it.
func (r *Response) AssertDownload(filename ...string) *Response {
	r.t.Helper()
	disposition := r.rec.Header().Get("Content-Disposition")
	kind, params, _ := strings.Cut(disposition, ";")
	if strings.TrimSpace(kind) != "attachment" {
		r.t.Errorf("%s answered Content-Disposition %q, want an attachment", r.path, disposition)
		return r
	}
	if len(filename) == 0 {
		return r
	}
	for _, param := range strings.Split(params, ";") {
		key, value, _ := strings.Cut(strings.TrimSpace(param), "=")
		if key == "filename" && strings.Trim(value, `"`) == filename[0] {
			return r
		}
	}
	r.t.Errorf("%s offered %q, want the file %q", r.path, disposition, filename[0])
	return r
}

// AssertContent asserts the body is exactly value.
func (r *Response) AssertContent(value string) *Response {
	r.t.Helper()
	if got := r.rec.Body.String(); got != value {
		r.t.Errorf("%s answered\n%s\nwant exactly\n%s", r.path, r.body(), value)
	}
	return r
}

// AssertSeeInOrder asserts every value is in the body, each after the one
// before it -- the order of rows in a listing, which AssertSee cannot tell.
func (r *Response) AssertSeeInOrder(values ...string) *Response {
	r.t.Helper()
	r.inOrder(r.rec.Body.String(), values)
	return r
}

// AssertSeeText asserts the text is on the page once the markup is taken off
// and the entities decoded: what a person reads, rather than what the HTML
// says.
func (r *Response) AssertSeeText(text string) *Response {
	r.t.Helper()
	if !strings.Contains(r.text(), text) {
		r.t.Errorf("%s does not read %q\n%s", r.path, text, r.body())
	}
	return r
}

// AssertSeeTextInOrder is [Response.AssertSeeInOrder] over the text a person
// reads.
func (r *Response) AssertSeeTextInOrder(values ...string) *Response {
	r.t.Helper()
	r.inOrder(r.text(), values)
	return r
}

// AssertDontSeeText asserts the text is not on the page once the markup is
// taken off and the entities decoded.
func (r *Response) AssertDontSeeText(text string) *Response {
	r.t.Helper()
	if strings.Contains(r.text(), text) {
		r.t.Errorf("%s reads %q and should not", r.path, text)
	}
	return r
}

// inOrder reports the first value not found after the one before it.
func (r *Response) inOrder(content string, values []string) {
	r.t.Helper()
	rest := content
	for _, value := range values {
		i := strings.Index(rest, value)
		if i < 0 {
			r.t.Errorf("%s does not show %q after the values before it, in %q\n%s", r.path, value, values, r.body())
			return
		}
		rest = rest[i+len(value):]
	}
}

// text is the body with the markup removed and the entities decoded.
func (r *Response) text() string {
	body := r.rec.Body.String()
	var out strings.Builder
	out.Grow(len(body))
	for i := 0; i < len(body); {
		if body[i] != '<' {
			out.WriteByte(body[i])
			i++
			continue
		}
		// A comment runs to its own terminator, which may hold a bare '>'.
		if strings.HasPrefix(body[i:], "<!--") {
			end := strings.Index(body[i+4:], "-->")
			if end < 0 {
				break
			}
			i += 4 + end + 3
			continue
		}
		end := strings.IndexByte(body[i:], '>')
		if end < 0 {
			break
		}
		i += end + 1
	}
	return html.UnescapeString(out.String())
}

// DecodeJSON returns the body as a decoded JSON payload, for the assertions
// hesape/testing writes over one. A body that is not JSON fails the test.
func (r *Response) DecodeJSON() *htesting.AssertableJSONString {
	r.t.Helper()
	decoded := htesting.NewAssertableJSONString(r.t, r.rec.Body.String())
	if decoded.Decoded() == nil {
		r.t.Fatalf("%s did not answer JSON\n%s", r.path, r.body())
	}
	return decoded
}

// AssertJSON asserts the payload holds data, as a subset: keys the payload
// has and data does not name are ignored.
func (r *Response) AssertJSON(data any) *Response {
	r.t.Helper()
	r.DecodeJSON().AssertSubset(data, false)
	return r
}

// AssertExactJSON asserts the payload is data, no more and no less.
func (r *Response) AssertExactJSON(data any) *Response {
	r.t.Helper()
	r.DecodeJSON().AssertExact(data)
	return r
}

// AssertJSONPath asserts the value at a dotted path, "data.0.id".
func (r *Response) AssertJSONPath(path string, expect any) *Response {
	r.t.Helper()
	r.DecodeJSON().AssertPath(path, expect)
	return r
}

// AssertJSONMissingPath asserts nothing is at the dotted path.
func (r *Response) AssertJSONMissingPath(path string) *Response {
	r.t.Helper()
	r.DecodeJSON().AssertMissingPath(path)
	return r
}

// AssertJSONFragment asserts data appears somewhere in the payload.
func (r *Response) AssertJSONFragment(data any) *Response {
	r.t.Helper()
	r.DecodeJSON().AssertFragment(data)
	return r
}

// AssertJSONMissing asserts data appears nowhere in the payload -- the half
// that catches a field leaking into an API answer.
func (r *Response) AssertJSONMissing(data any) *Response {
	r.t.Helper()
	r.DecodeJSON().AssertMissing(data, false)
	return r
}

// AssertJSONStructure asserts the payload has the keys structure names, at the
// depth it names them.
func (r *Response) AssertJSONStructure(structure any) *Response {
	r.t.Helper()
	r.DecodeJSON().AssertStructure(structure, nil, false)
	return r
}

// AssertJSONCount asserts how many elements the payload, or the value at key,
// holds.
func (r *Response) AssertJSONCount(count int, key ...string) *Response {
	r.t.Helper()
	r.DecodeJSON().AssertCount(count, key...)
	return r
}
