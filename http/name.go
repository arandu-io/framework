// The route table, answered by github.com/arandu-io/hesape/routing/controllers.

package http

import "github.com/arandu-io/hesape/routing/controllers"

// Routes is the table of registered routes, and the index by name: URL builds
// the path of a named route, Must is URL for a place that cannot handle an
// error, and All lists the routes in registration order.
//
// It is hesape's controller route table under its old name, and the two names
// are one type.
type Routes = controllers.Routes
