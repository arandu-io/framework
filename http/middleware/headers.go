// The default response headers, answered by
// github.com/arandu-io/hesape/http/middleware.

package middleware

import (
	"net/http"

	hmiddleware "github.com/arandu-io/hesape/http/middleware"
)

// SecurityHeaders applies the default headers.
//
// The CSP is restrictive on purpose and still works with HTMX, because HTMX
// operates through attributes rather than inline script. A module that truly
// needs inline code asks for an explicit nonce -- there is no global
// 'unsafe-inline' in this framework.
//
// A wrapper and not an alias, because a plain function has no alias form. The
// declared return type stays func(http.Handler) http.Handler rather than
// hhttp.Middleware, which is the same type by two aliases: spelling it the way
// this package always has keeps the signature identical for every caller.
func SecurityHeaders(dev bool) func(http.Handler) http.Handler {
	return hmiddleware.SecurityHeaders(dev)
}
