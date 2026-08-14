// The middleware type, answered by github.com/arandu-io/hesape/pipeline.
//
// It went to pipeline rather than staying here because the shape is not the
// request layer's: hesape/session, hesape/cookie and hesape/exception all
// produce middleware, and none of them may import the layer that calls it.

package http

import (
	"net/http"

	"github.com/arandu-io/hesape/pipeline"
)

// Middleware is the standard net/http signature. We do not invent our own type:
// that is what keeps the whole Go ecosystem compatible with the framework.
//
// The "=" is load-bearing and not a shorthand. It is what lets hesape/session,
// hesape/cookie and hesape/exception return a pipeline.Middleware[http.Handler]
// that IS an http.Middleware without importing this package -- with a defined
// type each of them would have to import the layer above, which is the cycle
// this reorganisation exists to remove.
//
// hesape/http.Middleware is the same type by the same alias, so a middleware
// written against either name satisfies both.
type Middleware = pipeline.Middleware[http.Handler]

// Chain composes middlewares. The first in the list is the outermost.
//
// A wrapper and not an alias: Go has no alias form for a generic function, and
// pipeline.Chain is generic over the handler type.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	return pipeline.Chain(h, mws...)
}
