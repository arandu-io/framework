package middleware

import (
	"mime"
	"net/http"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
)

// StatusCSRFExpired is the status returned when the token is missing, invalid or
// expired. 419 is the conventional status for it, kept on purpose: HTMX can be told to
// reload the page on 419, which is the only useful reaction to an expired token.
const StatusCSRFExpired = 419

// CSRFProtect refuses a state-changing request the browser reports as
// cross-origin, then validates the token on the ones that are left.
//
// THE TRAP THIS SOLVES: with HTMX the token does not always arrive in a form
// field, it arrives in a header. Both sources are read, the X-CSRF-Token header
// first and the _token form field after it.
//
// The body is read only when the header is absent, and only as a form: an
// application/x-www-form-urlencoded body is parsed with ParseForm, a
// multipart/form-data body with ParseMultipartForm, and any other body is left
// unread and the request refused as carrying no token. A request whose token is
// in the header reaches the handler with its body untouched, so a handler can
// still stream a multipart upload part by part.
//
// A multipart form parsed here is removed when the handler returns, whatever
// the answer was. ParseMultipartForm writes the parts that do not fit in memory
// to temporary files, and net/http removes them only for the request the server
// created; the one reaching this middleware is a copy made further up the
// pipeline, so without the removal every upload that reached it -- a refused
// one included -- left its files on disk. The handler reads the same parsed form
// through the request it receives, and the files are gone once it has returned:
// one that keeps an upload has to copy it somewhere before then.
//
// The field is named for the session key the token is stored under, which is
// the one name a form builder, a view and this middleware can all arrive at
// without agreeing on a second one first. A field spelled any other way is read
// by nothing here and the request is refused as though it carried no token.
//
// A form carries the hidden _token field, so a submission is covered whether it
// posts natively or through hx-post. What that does not cover is a request no
// form backs -- hx-delete on a button, hx-patch on a toggle. Those carry the
// token only where the markup puts it, either once on the layout
//
//	<body hx-headers='{"X-CSRF-Token": "{{ .CSRFToken }}"}'>
//
// or on the element that sends the request.
//
// Nothing verifies that the line is there. A layout that loses it keeps
// rendering and every form goes on working; the first button that deletes
// something answers StatusCSRFExpired instead, which reads as an expired
// session rather than as missing markup.
//
// The two checks answer different failures and neither replaces the other. The
// origin check reads Sec-Fetch-Site, which browsers have sent since 2023, and
// falls back to comparing Origin against Host; it costs no allocation and turns
// a form posted from another site away before any HMAC is computed. It cannot
// stand alone, because a request carrying neither header is allowed -- that is
// how a non-browser client reaches an application at all. The token is what
// covers those, and it is the layer that survives a browser too old to report
// where the request came from.
//
// A cross-origin refusal is 403 rather than StatusCSRFExpired, because the two
// ask different things of whoever hit them. An expired token is fixed by
// reloading the page, which brings a fresh one; an origin is not, and telling
// the browser to reload would send it round the same refusal again.
//
// There is no list of trusted origins to configure. A state-changing request
// from another site is exactly what this refuses, so an exception to it is a
// decision about an application rather than a setting on a middleware.
//
// sessionIDFrom must return the id only for a valid session cookie -- pass
// SessionStore.IDFromRequest, which verifies the signature first.
func CSRFProtect(c *security.CSRF, sessionIDFrom func(*http.Request) string) func(http.Handler) http.Handler {
	safe := map[string]bool{
		http.MethodGet: true, http.MethodHead: true, http.MethodOptions: true, http.MethodTrace: true,
	}
	origin := http.NewCrossOriginProtection()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if safe[r.Method] {
				next.ServeHTTP(w, r)
				return
			}

			if err := origin.Check(r); err != nil {
				fhttp.Refuse(w, r, http.StatusForbidden, "this request was not sent from this site: the browser reported the submission as cross-origin, and a request that changes state is only accepted from a page of this application")
				return
			}

			token := r.Header.Get("X-CSRF-Token")
			if token == "" {
				var release func()
				token, release = formToken(r)
				defer release()
			}

			// A missing token and an expired one are different mistakes and get
			// different sentences. "Session expired" sends the developer to
			// look at session lifetimes, when the form simply never carried the
			// field -- which is the first thing that happens to anybody wiring a
			// form or an HTMX request by hand.
			//
			// Both answers go through fhttp.Refuse rather than http.Error, and
			// the role guard's 403 does too: htmx swaps neither status, so on
			// http.Error the message reaches a person as nothing at all -- they
			// press the button, and the screen does not change. Doing it in only
			// one of the two would leave the framework refusing an HTMX request
			// in two different ways.
			if token == "" {
				fhttp.Refuse(w, r, StatusCSRFExpired, "this request carried no CSRF token: add the hidden _token field to the form, or send it as the X-CSRF-Token header")
				return
			}
			if err := c.Validate(sessionIDFrom(r), token); err != nil {
				fhttp.Refuse(w, r, StatusCSRFExpired, "this CSRF token is no longer valid: the session it belongs to expired or was replaced. Reload the page and submit again")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// multipartMemory is how much of a multipart body is held in memory before the
// rest of its files go to temporary files. It is the figure net/http uses when a
// handler calls FormFile without parsing first, so a handler behind this
// middleware sees the form it would have parsed itself.
const multipartMemory = 32 << 20

// formToken reads the _token field from a form body, and returns the function
// that releases what reading it left behind.
//
// Only the two form encodings are parsed. Anything else -- JSON, a file posted
// as the whole body, a body with no Content-Type -- carries its token in the
// header or carries none.
//
// The release removes the temporary files of a multipart form parsed here, and
// only of one parsed here: a form some earlier layer parsed belongs to that
// layer.
func formToken(r *http.Request) (string, func()) {
	nothing := func() {}

	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return "", nothing
	}

	switch mediaType {
	case "application/x-www-form-urlencoded":
		if err := r.ParseForm(); err != nil {
			return "", nothing
		}
		return r.PostForm.Get("_token"), nothing

	case "multipart/form-data":
		if r.MultipartForm != nil {
			return firstValue(r.MultipartForm.Value["_token"]), nothing
		}
		err := r.ParseMultipartForm(multipartMemory)
		form := r.MultipartForm
		release := func() {
			if form != nil {
				_ = form.RemoveAll()
			}
		}
		if err != nil || form == nil {
			return "", release
		}
		return firstValue(form.Value["_token"]), release
	}
	return "", nothing
}

// firstValue is the first of a form field's values, or "" when it has none.
func firstValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
