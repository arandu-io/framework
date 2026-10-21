// The resource controller, answered by
// github.com/arandu-io/hesape/routing/controllers.

package http

import "github.com/arandu-io/hesape/routing/controllers"

// The seven actions of a resource controller, one interface each.
//
// Each action is its own tiny interface, and Router.Resource registers exactly
// the ones the controller implements, so a route that exists is a route that
// answers. Router.Singleton registers the three of a resource with no id.
//
// Each is hesape's under its old name. A controller declaring
// Index(*http.Context) error satisfies controllers.Indexer literally, because
// http.Context is hhttp.Context.
type (
	// Indexer lists the resource: GET /invoices.
	Indexer = controllers.Indexer
	// Creator shows the form for a new record: GET /invoices/create.
	Creator = controllers.Creator
	// Storer saves a new record: POST /invoices.
	Storer = controllers.Storer
	// Shower shows one record: GET /invoices/{id}.
	Shower = controllers.Shower
	// Editor shows the form for one record: GET /invoices/{id}/edit.
	Editor = controllers.Editor
	// Updater saves one record: PUT and PATCH /invoices/{id}.
	Updater = controllers.Updater
	// Destroyer deletes one record: DELETE /invoices/{id}.
	Destroyer = controllers.Destroyer
)
