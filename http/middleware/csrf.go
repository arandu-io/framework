// The CSRF check, answered by github.com/arandu-io/hesape/http/middleware.

package middleware

import (
	"net/http"

	"github.com/arandu-io/framework/security"
	hmiddleware "github.com/arandu-io/hesape/http/middleware"
)

// StatusCSRFExpired is the status returned when the token is missing, invalid or
// expired. 419 is the conventional status for it, kept on purpose: HTMX can be told to
// reload the page on 419, which is the only useful reaction to an expired token.
const StatusCSRFExpired = hmiddleware.StatusCSRFExpired

// CSRFProtect refuses a state-changing request the browser reports as
// cross-origin, then validates the token on the ones that are left: the
// X-CSRF-Token header first and the _token form field after it, bound to the
// session sessionIDFrom names or, for a visitor with none, to a guest cookie.
// The token it issued is put on the request context, where view.New reads it.
// A form carries it as the hidden field @csrf writes, and an HTMX page sends
// it as a header on every request, from the body:
//
//	<body hx-headers='{"X-CSRF-Token": "{{ .CSRFToken }}"}'>
//
// It is hesape/http/middleware.CSRFProtect under its old name.
func CSRFProtect(c *security.CSRF, sessionIDFrom func(*http.Request) string, opts ...CSRFOption) func(http.Handler) http.Handler {
	return hmiddleware.CSRFProtect(c, sessionIDFrom, opts...)
}

// CSRFOption adjusts CSRFProtect.
type CSRFOption = hmiddleware.CSRFOption

// CSRFExcept exempts every request whose path is one of prefixes or sits
// under one, for a webhook that authenticates by signature instead. Each
// prefix is a clean path; one that is not panics at boot, because a request
// path is cleaned before it is compared and the prefix would never match.
func CSRFExcept(prefixes ...string) CSRFOption { return hmiddleware.CSRFExcept(prefixes...) }
