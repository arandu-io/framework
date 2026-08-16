// The route table, answered by github.com/arandu-io/hesape/routing.
//
// The table itself is hesape's and holds every route; what is declared here is
// the one method whose name did not survive the move.

package http

import "github.com/arandu-io/hesape/routing"

// Routes is the table of registered routes, and the index by name.
//
// It is an envelope over hesape/routing.Routes and not an alias, for one
// reason: the method that builds a path from a name was renamed URL -> Route,
// and Go forbids declaring a method on another package's type. An alias here
// would drop Routes.URL from the framework's surface, and a bridge that drops a
// method is not a bridge.
//
// It forwards and holds nothing. The routes are hesape's table, shared by every
// sub-router the way it always was, so a route registered through a group is in
// the same table the root reads.
type Routes struct{ inner *routing.Routes }

// URL builds the path of a named route, filling the parameters in order.
//
//	URL("home")                  -> "/"
//	URL("invoices.show", "42")   -> "/invoices/42"
//
// A hardcoded "/invoices/"+id compiles and keeps compiling after the route
// moves. This does not: an unknown name or a wrong number of parameters is an
// error the caller sees, not a 404 the user sees.
//
// It is hesape/routing.Routes.Route under the name this package has always used
// for it. Only this spelling is offered here -- exposing both would be two ways
// to ask one question, and the new one is reached by importing hesape/routing,
// which the death date says to do anyway.
func (t *Routes) URL(name string, params ...string) (string, error) {
	return t.inner.Route(name, params...)
}

// Must is URL for the places that cannot handle an error -- a template helper,
// mostly. It returns the message as the href, so a broken link says what is
// wrong instead of pointing at "/".
func (t *Routes) Must(name string, params ...string) string {
	return t.inner.Must(name, params...)
}

// All returns the routes in registration order, for `aru routes`.
func (t *Routes) All() []*Route { return t.inner.All() }
