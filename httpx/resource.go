package httpx

import (
	"net/http"
	"strings"
)

// The seven actions of a resource controller, one interface each.
//
// Laravel's Route::resource takes a class and registers seven routes whether or
// not the methods exist -- a request to a missing one is a runtime error. Here
// each action is its own tiny interface, and Resource registers exactly the ones
// the controller implements. A route that exists is a route that answers.
//
// Seven interfaces rather than one with seven methods, for the same reason the
// kernel splits Bootable, Background and Closable: a controller that only lists
// and shows should not be forced to write five empty methods, and five empty
// methods is how a 500 gets committed.
type (
	// Indexer answers GET /thing -- the list.
	Indexer interface {
		Index(*Context) error
	}
	// Creator answers GET /thing/create -- the empty form.
	Creator interface {
		Create(*Context) error
	}
	// Storer answers POST /thing -- the form submission.
	Storer interface {
		Store(*Context) error
	}
	// Shower answers GET /thing/{id} -- one record.
	Shower interface {
		Show(*Context) error
	}
	// Editor answers GET /thing/{id}/edit -- the filled form.
	Editor interface {
		Edit(*Context) error
	}
	// Updater answers PUT and PATCH /thing/{id}.
	Updater interface {
		Update(*Context) error
	}
	// Destroyer answers DELETE /thing/{id}.
	Destroyer interface {
		Destroy(*Context) error
	}
)

// Resource registers the REST routes a controller implements.
//
//	Route.Resource("invoices", InvoiceController{})
//
// The seven, in Laravel's order and with Laravel's names:
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
// The order matters: /invoices/create is registered before /invoices/{id} so a
// GET of "create" reaches the form rather than being read as an id. Go's
// ServeMux prefers the more specific pattern, and registering in this order
// keeps the intent readable even where the mux would sort it out anyway.
//
// A controller implementing none of the seven registers nothing and returns
// zero routes, which is a wiring mistake worth seeing in `aru routes`.
func (r *Router) Resource(name string, controller any) []*Route {
	name = strings.Trim(name, "/")
	base := "/" + name
	one := base + "/{id}"

	var out []*Route
	add := func(method, pattern, action string, h func(*Context) error) {
		out = append(out, r.handleCtx(method, pattern, h).Name(name+"."+action))
	}

	if c, ok := controller.(Indexer); ok {
		add(http.MethodGet, base, "index", c.Index)
	}
	if c, ok := controller.(Creator); ok {
		add(http.MethodGet, base+"/create", "create", c.Create)
	}
	if c, ok := controller.(Storer); ok {
		add(http.MethodPost, base, "store", c.Store)
	}
	if c, ok := controller.(Shower); ok {
		add(http.MethodGet, one, "show", c.Show)
	}
	if c, ok := controller.(Editor); ok {
		add(http.MethodGet, one+"/edit", "edit", c.Edit)
	}
	if c, ok := controller.(Updater); ok {
		add(http.MethodPut, one, "update", c.Update)
		// PATCH answers the same handler and shares the name, like Laravel.
		// Only the PUT row carries the name in the table, so URL generation has
		// one answer rather than two.
		r.handleCtx(http.MethodPatch, one, c.Update)
	}
	if c, ok := controller.(Destroyer); ok {
		add(http.MethodDelete, one, "destroy", c.Destroy)
	}
	return out
}
