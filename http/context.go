// The request context, answered by github.com/arandu-io/hesape/http.
//
// Every name here is an alias or a one-line call through. The Context a
// controller action receives is literally the hesape type, so a controller
// written against this package and one written against hesape/http take the
// same value -- which is what lets hesape/routing register either of them.

package http

import (
	"net/http"

	hhttp "github.com/arandu-io/hesape/http"
)

// Context is what a controller action receives.
//
// It is everything a controller action gets, and no more: the request, the
// response, and helpers that answer. There is no database handle here, no
// repository, no Grant -- a controller that could reach the data layer would be
// a controller that skipped the service, and therefore the policy.
//
// The alias is what keeps it one type rather than two. It also carries the
// accessors the move added -- Header, Path, Method, IP, BearerToken, Cookie,
// IsHTMX, WantsJSON, User, RedirectRoute -- which is a widening of the surface
// and not a change to any signature that already existed.
//
// Context.Validate is the one name that did not survive: it had no caller
// outside its own test and it was the second way to validate. See the package
// comment.
type Context = hhttp.Context

// Renderer draws a named view with typed data.
//
// It is an interface, and implemented in the view package, for one reason: the
// view package imports this layer to register its route, so this layer
// importing the view package back would be a cycle. The kernel wires the
// concrete one at boot.
type Renderer = hhttp.Renderer

// Redirect is Context.Redirect for the code that holds a raw ResponseWriter: a
// middleware, or a handler registered with Get rather than Action.
//
// An HTMX request gets HX-Redirect and 204, because a 302 would be followed
// inside the fragment and nest the whole page in a div. Everything else gets
// 303, which after a POST is what tells the browser to GET the next address
// instead of posting the body to it again.
//
// A wrapper and not a var: a package-level function variable is reassignable at
// init by any package in the build, and this is the one decision every redirect
// in the framework goes through.
func Redirect(w http.ResponseWriter, r *http.Request, to string) { hhttp.Redirect(w, r, to) }

// Refuse answers a refusal that the person in front of the browser can see.
//
// The status and the sentence are the caller's and are unchanged: 403 stays 403
// and 419 stays 419. What it adds is HX-Refresh, because htmx swaps no 4xx --
// without it a guard's 403 reached somebody as a button that did nothing.
func Refuse(w http.ResponseWriter, r *http.Request, status int, message string) {
	hhttp.Refuse(w, r, status, message)
}
