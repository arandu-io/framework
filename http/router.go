// The router, answered by github.com/arandu-io/hesape/routing/controllers.

package http

import (
	"github.com/arandu-io/hesape/routing"
	"github.com/arandu-io/hesape/routing/controllers"
)

// Route is metadata, used by `aru routes` and by the error page.
//
// The alias holds because the three exported fields, RouteName and Name are the
// same on both sides. What the move added -- Where, Middleware, Defaults,
// Domain, the parameter accessors -- is a widening, not a change.
type Route = routing.Route

// Router registers routes and the controller actions that answer them: Get,
// Post, Put, Patch and Delete for a handler, Action, Resource, Singleton,
// ResourceAction and Invokable for a controller, Group and ForModule to scope
// what is registered after them.
//
// It is hesape's controller router under its old name, and the two names are
// one type. The renderer, the flash and the adapter that turns a
// func(*Context) error into an http.Handler -- the flash and the redirect for
// a rejection, the 422 problem for JSON, the status an error names -- are
// hesape's, so a controller is answered the same way whichever name
// registered it.
type Router = controllers.Router

// NewRouter returns an empty router, with no renderer and no flash.
func NewRouter() *Router { return controllers.NewRouter() }
