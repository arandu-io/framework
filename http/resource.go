// The resource controller, answered by github.com/arandu-io/hesape/routing.
//
// The seven interfaces are there under the same names and the same doc, with
// one difference that decides the shape of this file: each of them gained a
// type parameter for the request context, because hesape/routing must not
// import the layer that owns one.

package http

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
// Index(*http.Context) error satisfies routing.Indexer[hhttp.Context]
// literally, because http.Context IS hhttp.Context, so hesape's type assertion
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
//	r.Resource("invoices", InvoiceController{})
//
// The receiver is the router. The line above read Route.Resource until it was
// found not to compile: Route is an alias for the route metadata type, which
// has no Resource method. ExampleRouter_Resource compiles the corrected
// spelling.
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
// A dotted name nests the resource under its parents, and the nesting is
// shallow: the list, the form and the store sit under the parent, and the four
// that act on one record at that record's own path. Every route keeps the
// whole dotted name.
//
//	r.Resource("projects.tasks", TaskController{})
//
//	GET    /projects/{project}/tasks          index    projects.tasks.index
//	GET    /projects/{project}/tasks/create   create   projects.tasks.create
//	POST   /projects/{project}/tasks          store    projects.tasks.store
//	GET    /tasks/{task}                      show     projects.tasks.show
//	GET    /tasks/{task}/edit                 edit     projects.tasks.edit
//	PUT    /tasks/{task}                      update   projects.tasks.update
//	PATCH  /tasks/{task}                      update   projects.tasks.update
//	DELETE /tasks/{task}                      destroy  projects.tasks.destroy
//
// The parent's parameter is where the person navigated, never whose data it
// is: the action loads the parent under its Grant and filters the children by
// it. A name without a dot keeps {id}. routing.Resource holds the whole rule.
//
// A controller implementing none of the seven registers nothing and returns
// zero routes, which is a wiring mistake worth seeing in `aru routes`.
//
// It stays a method here and is a function there -- routing.Resource takes the
// router first, because a Go method cannot take a type parameter and C has to
// come from somewhere. The type parameter and the adapter are supplied by this
// line, so no caller of Router.Resource changes.
//
// The counterpart to this method is the free function routing.Resource, which
// takes the controller value and an adapter. As with Action, the adapter is
// what there is no exported way to supply, so there is no line to migrate to
// yet. hesape/routing.Router has no Resource method, so a migration that only
// changes the receiver's type does not compile rather than registering
// something else.
func (r *Router) Resource(name string, controller any) []*Route {
	return routing.Resource(r.inner, name, controller, r.adapt)
}

// Singleton registers the routes of a resource there is exactly one of where it
// is reached -- the account's settings, a project's billing -- so no route
// carries an id.
//
//	r.Singleton("settings", SettingsController{})
//
//	GET    /settings        show     settings.show
//	GET    /settings/edit   edit     settings.edit
//	PUT    /settings        update   settings.update
//	PATCH  /settings        update   settings.update
//
// It registers whichever of Shower, Editor and Updater the controller
// implements and nothing else, and a controller implementing none of them
// returns zero routes. A dotted name nests it as Resource nests one:
// "projects.billing" answers at /projects/{project}/billing.
//
// It forwards to routing.Singleton with this router's adapter, so each action
// is answered the way Action answers one.
func (r *Router) Singleton(name string, controller any) []*Route {
	return routing.Singleton(r.inner, name, controller, r.adapt)
}

// ResourceAction registers one named action on a record of a resource: a verb
// beyond the seven, such as publish, cancel or approve.
//
//	r.ResourceAction(http.MethodPost, "notes", "publish", notes.Publish)
//
//	POST   /notes/{id}/publish     notes.publish
//
// It sits under the path Resource gives show for the same name and reads the
// same parameter, so every action of one controller finds its record the same
// way. On a nested resource that is the record's own path:
// ResourceAction(http.MethodPost, "projects.tasks", "close", ...) answers
// POST /tasks/{task}/close as projects.tasks.close.
//
// The method comes first, as in Action, and it is POST, PUT, PATCH or DELETE:
// an action changes state, and a GET that changes state is one a prefetching
// browser or a crawler fires without anybody choosing to. Any other method, and
// an empty action, panic at registration. The middleware is the route's own and
// runs after the group's, as Action takes it.
//
// It forwards to routing.ResourceAction with this router's adapter, which takes
// the method after the names; the order here is the one Action already has.
func (r *Router) ResourceAction(method, resource, action string, h func(*Context) error, mws ...Middleware) *Route {
	return routing.ResourceAction(r.inner, resource, action, method, h, r.adapt).Middleware(mws...)
}
