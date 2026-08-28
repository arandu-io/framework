package middleware

import (
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
// first and the _csrf form field after it.
//
// A form carries the hidden _csrf field, so a submission is covered whether it
// posts natively or through hx-post. What that does not cover is a request no
// form backs -- hx-delete on a button, hx-patch on a toggle. Those carry the
// token only where the markup puts it, either once on the layout
//
//	<body hx-headers='{"X-CSRF-Token": "{{ .CSRFToken() }}"}'>
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
				token = r.PostFormValue("_csrf")
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
				fhttp.Refuse(w, r, StatusCSRFExpired, "this request carried no CSRF token: add the hidden _csrf field to the form, or send it as the X-CSRF-Token header")
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
