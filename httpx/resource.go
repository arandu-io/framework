// The resource controller, answered by github.com/arandu-io/hesape/routing.
//
// The seven interfaces are there under the same names and the same doc, with
// one difference that decides the shape of this file: each of them gained a
// type parameter for the request context, because hesape/routing must not
// import the layer that owns one.

package httpx

import (
	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/routing"
)

// The seven actions of a resource controller, one interface each.
//
// The usual shape of this takes one object and registers seven routes whether
// or not the methods exist -- a request to a missing one is a runtime error.
// Here each action is its own tiny interface, and Resource registers exactly
// the ones the controller implements. A route that exists is a route that
// answers.
//
// Each is an alias to an INSTANTIATED generic: hesape declares
// routing.Indexer[C any], so `type Indexer = routing.Indexer` does not compile
// and `type Indexer = routing.Indexer[hhttp.Context]` does. Instantiating is
// what keeps these aliases rather than renames -- a controller declaring
// Index(*httpx.Context) error satisfies routing.Indexer[hhttp.Context]
// literally, because httpx.Context IS hhttp.Context, so hesape's type assertion
// finds it.
type (
	// Indexer answers GET /thing -- the list.
	Indexer = routing.Indexer[hhttp.Context]
	// Creator answers GET /thing/create -- the empty form.
	Creator = routing.Creator[hhttp.Context]
	// Storer answers POST /thing -- the form submission.
	Storer = routing.Storer[hhttp.Context]
	// Shower answers GET /thing/{id} -- one record.
	Shower = routing.Shower[hhttp.Context]
	// Editor answers GET /thing/{id}/edit -- the filled form.
	Editor = routing.Editor[hhttp.Context]
	// Updater answers PUT and PATCH /thing/{id}.
	Updater = routing.Updater[hhttp.Context]
	// Destroyer answers DELETE /thing/{id}.
	Destroyer = routing.Destroyer[hhttp.Context]
)

// Resource registers the REST routes a controller implements.
//
//	Route.Resource("invoices", InvoiceController{})
//
// The seven, in the conventional order and with the conventional names:
//
//	GET    /invoices             index    invoices.index
//	GET    /invoices/create      create   invoices.create
//	POST   /invoices             store    invoices.store
//	GET    /invoices/{id}        show     invoices.show
//	GET    /invoices/{id}/edit   edit     invoices.edit
//	PUT    /invoices/{id}        update   invoices.update
//	PATCH  /invoices/{id}        update   invoices.update
//	DELETE /invoices/{id}        destroy  invoices.destroy
//
// A controller implementing none of the seven registers nothing and returns
// zero routes, which is a wiring mistake worth seeing in `aru routes`.
//
// It stays a method here and is a function there -- routing.Resource takes the
// router first, because a Go method cannot take a type parameter and C has to
// come from somewhere. The type parameter and the adapter are supplied by this
// line, so no caller of Route.Resource changes.
func (r *Router) Resource(name string, controller any) []*Route {
	return routing.Resource(r.inner, name, controller, r.adapt)
}
