package middleware

import (
	"mime"
	"net/http"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	hhttp "github.com/arandu-io/hesape/http"
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
// from another site is exactly what this refuses.
//
// # A bearer token is not ambient
//
// A request that carries Authorization: Bearer and no valid session cookie is
// not asked for a token, and binds no guest cookie: it is left to the guard on
// its route, RequireToken, which authenticates it or answers 401. The scheme is
// read by the same reader RequireToken uses, so the two cannot disagree about
// which requests carry one.
//
// The token check exists against ambient authority -- a credential the browser
// attaches by itself to a request another site started. A bearer token is not
// one: a page on another site cannot attach an Authorization header without a
// CORS preflight, and a client that attaches it holds the token. Basic, Digest
// and Negotiate are ambient, because a browser attaches them again by itself
// after a 401 challenge, and those requests are checked like any other.
//
// The origin check still applies, so a browser reporting the request as
// cross-origin is refused whatever header it carries.
//
// With a valid session cookie the full check applies, bearer or not: the route
// behind may honour the cookie and ignore the header, and the cookie is exactly
// what a forged request rides on. A cookie sessionIDFrom does not accept -- a
// signature that does not verify -- carries no session, so it is as though
// there were none: a forged request with a junk cookie and no bearer token is
// still refused for carrying no CSRF token.
//
// # The token a page draws
//
// It also issues the token, so that no controller has to. On a GET or a HEAD it
// issues one and puts it on the request context with hhttp.WithCSRFToken, where
// view.New reads it into Page.Token; on a request that passed the check it puts
// the token that was submitted there, so a form redrawn on the same request --
// a refused sign-in, a validation answered in place -- carries a token that
// still validates. OPTIONS and TRACE draw nothing and get nothing.
//
// The token is bound to what CSRF.Binding returns: the session id when the
// request carries a session cookie, and otherwise a random guest id carried in
// a signed cookie of its own, set on the first page a visitor without one
// loads. A token is therefore accepted only from the browser it was issued to,
// signed in or not, and the sign-in form -- submitted by somebody with no
// session yet -- is protected like every other. A request with neither cookie
// validates nothing.
//
// Every token issued for a binding stays valid until its own expiry, so a page
// left open in one tab keeps working after another tab loaded a newer one.
// Nothing rotates them: the binding changes when the session does, at sign-in
// and sign-out, and those answers are a redirect to a page that issues afresh.
//
// The guest cookie carries the Secure attribute unless the CSRF was built with
// Secure(false), which is for development over plain HTTP only: without it the
// browser never sends the cookie back, and every guest form answers
// StatusCSRFExpired.
//
// A page that carries a token is a page for one visitor. A shared cache in
// front of it that serves one visitor's page to another serves a token bound to
// somebody else, and that form answers StatusCSRFExpired.
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
				if r.Method == http.MethodGet || r.Method == http.MethodHead {
					// Issue fails only when the system has no entropy left, and
					// then the page is drawn without a token: a form on it is
					// refused on submit, which is the safe side of the failure.
					if token, err := c.Issue(c.Binding(w, r, sessionIDFrom(r))); err == nil {
						r = r.WithContext(hhttp.WithCSRFToken(r.Context(), token))
					}
				}
				next.ServeHTTP(w, r)
				return
			}

			if err := origin.Check(r); err != nil {
				fhttp.Refuse(w, r, http.StatusForbidden, "this request was not sent from this site: the browser reported the submission as cross-origin, and a request that changes state is only accepted from a page of this application")
				return
			}

			sessionID := sessionIDFrom(r)
			if sessionID == "" && hhttp.NewContext(w, r, nil, nil).BearerToken() != "" {
				next.ServeHTTP(w, r)
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
			if err := c.Validate(c.Binding(nil, r, sessionID), token); err != nil {
				fhttp.Refuse(w, r, StatusCSRFExpired, "this CSRF token is no longer valid: the session it belongs to expired or was replaced. Reload the page and submit again")
				return
			}
			next.ServeHTTP(w, r.WithContext(hhttp.WithCSRFToken(r.Context(), token)))
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
