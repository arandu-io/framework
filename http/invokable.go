// The single-action controller, answered by github.com/arandu-io/hesape/routing.

package http

import (
	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/routing"
)

// Invoker is a controller with one action. It answers one route, so the action
// needs no name beyond Invoke.
//
// It is an alias to an instantiated generic, for the reason the seven resource
// interfaces are: a controller declaring Invoke(*http.Context) error satisfies
// routing.Invoker[hhttp.Context] literally, because http.Context is
// hhttp.Context.
type Invoker = routing.Invoker[hhttp.Context]

// Invokable registers a single-action controller under one method and one
// pattern.
//
//	r.Invokable(http.MethodPost, "/exports", ExportController{}).Name("exports.store")
//
// The method comes first, as in Action. The controller is an Invoker rather
// than an any, so a controller without Invoke fails to compile at this line
// instead of registering nothing. The route is not named; name it with Name, as
// any single route. It is registered on this router, so it carries the group's
// prefix and middleware, and an empty method panics at registration.
//
// It forwards to routing.Invokable with this router's adapter, so the action is
// answered the way Action answers one.
func (r *Router) Invokable(method, pattern string, controller Invoker) *Route {
	return routing.Invokable(r.inner, method, pattern, controller, r.adapt)
}
