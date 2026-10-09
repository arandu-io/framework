// The router, answered by github.com/arandu-io/hesape/routing.
//
// This is the widest divergence in the package. hesape/routing.Router holds no
// request state at all -- an earlier version of it carried the view renderer
// and the flash, and that is what made registering a controller a second
// registration path -- so the two fields this framework's router has always
// carried live in the envelope below, and the adapter that turns a controller
// action into an http.Handler is written here because hesape/routing takes it
// as a parameter (routing.Adapter) rather than knowing what a Context is.

package http

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/hesape/exception"
	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/routing"
)

// Route is metadata, used by `aru routes` and by the error page.
//
// The alias holds because the three exported fields, RouteName and Name are the
// same on both sides. What the move added -- Where, Middleware, Defaults,
// Domain, the parameter accessors -- is a widening, not a change.
type Route = routing.Route

// Router is a thin shell over http.ServeMux, which since Go 1.22 already
// handles methods and path parameters. It exists for groups, per-group
// middleware and route metadata -- the metadata is what lets the CLI generate
// typed URL helpers and what the error page uses to show the matched route.
//
// It is an envelope over hesape/routing.Router and not an alias, for three
// reasons, any one of which would be enough:
//
//   - Group takes a prefix and a variadic here, and a routing.Group struct
//     there;
//   - the renderer and the flash are fields here, and hesape/routing
//     deliberately holds neither;
//   - Action and Resource do not exist there in this shape, because turning a
//     func(*Context) error into an http.Handler is the request layer's job and
//     hesape/routing takes that as a parameter.
//
// It stores no routes of its own. Every registration goes straight through to
// the hesape router, whose table is shared by every sub-router exactly as it
// was before.
type Router struct {
	inner  *routing.Router
	table  *Routes
	render Renderer
	flash  *security.Flash
}

// NewRouter returns an empty router.
func NewRouter() *Router {
	inner := routing.NewRouter()
	return &Router{inner: inner, table: &Routes{inner: inner.Table()}}
}

// WithRenderer returns a router whose handlers can render views.
//
// The kernel calls it at boot with the view module. Without it, Context.View
// returns an error naming the missing line in bootstrap/app.go rather than
// panicking.
func (r *Router) WithRenderer(rd Renderer) *Router {
	g := *r
	g.render = rd
	return &g
}

// WithFlash returns a router whose handlers can answer a rejected request.
//
// The kernel calls it at boot, the way it calls WithRenderer, so no application
// wires it and none can forget to. Without it a handler that returns
// validation.Errors to a page reaches the panic path, which is the honest
// answer: a rejection that cannot be flashed is a rejection nobody will see,
// and a page that fails loudly beats a form that silently comes back blank. A
// request that wants JSON does not need it, because its messages travel in the
// body of the answer rather than to the next page.
func (r *Router) WithFlash(f *security.Flash) *Router {
	g := *r
	g.flash = f
	return &g
}

// Group returns a sub-router with the prefix appended and the middleware
// inherited. The route table is shared with the parent.
//
// The prefix and the middleware are what hesape/routing takes as a Group
// struct. The struct's third field, Name, has no counterpart in this signature
// and is deliberately left unset: adding it would be a new way to name a route
// alongside Route.Name, and this package is being removed rather than grown.
//
// This one has an exact replacement, and taking it is what makes Name
// reachable again:
//
//	r.Group("/admin", mws...)
//	// becomes
//	r.Group(routing.Group{Prefix: "/admin", Middleware: mws})
func (r *Router) Group(prefix string, mws ...Middleware) *Router {
	g := *r
	g.inner = r.inner.Group(routing.Group{Prefix: prefix, Middleware: mws})
	return &g
}

// ForModule returns a sub-router that tags its routes with the module name, so
// `aru routes` can group them. The Kernel calls it for each module.
func (r *Router) ForModule(name string) *Router {
	g := *r
	g.inner = r.inner.ForModule(name)
	return &g
}

// Get registers a GET route.
//
//	r.Get("/health", health).Name("health")
//
// This is the registration path that survives, and the five verb methods below
// it are the same call under another method name. Migrating any of them is the
// identical line: github.com/arandu-io/hesape/routing spells them the same way
// and takes an http.Handler where this takes an http.HandlerFunc, which is a
// widening -- every handler that satisfies this signature satisfies that one,
// so the call moves unchanged. It does not move back: a handler that is an
// http.Handler and not a func has no spelling here.
func (r *Router) Get(pattern string, h http.HandlerFunc, mws ...Middleware) *Route {
	return r.inner.Get(pattern, h, mws...)
}

// Post registers a POST route.
func (r *Router) Post(pattern string, h http.HandlerFunc, mws ...Middleware) *Route {
	return r.inner.Post(pattern, h, mws...)
}

// Put registers a PUT route.
func (r *Router) Put(pattern string, h http.HandlerFunc, mws ...Middleware) *Route {
	return r.inner.Put(pattern, h, mws...)
}

// Patch registers a PATCH route.
func (r *Router) Patch(pattern string, h http.HandlerFunc, mws ...Middleware) *Route {
	return r.inner.Patch(pattern, h, mws...)
}

// Delete registers a DELETE route.
func (r *Router) Delete(pattern string, h http.HandlerFunc, mws ...Middleware) *Route {
	return r.inner.Delete(pattern, h, mws...)
}

// Action registers one controller action, for a route outside a resource.
//
//	r.Action("GET", "/dashboard", dashboard.Index).Name("dashboard")
//
// The receiver is the router. The line above read Route.Action until it was
// found not to compile: Route is an alias for the route metadata type, which
// has no Action method, so the example named a method expression on the wrong
// type. ExampleRouter_Action compiles the corrected spelling.
//
// It is not a second way to route. Registration goes through the one path
// every other verb method goes through, and what this adds is the handler:
// Get takes an http.HandlerFunc, a controller action is a
// func(*Context) error, and something has to turn one into the other. That
// something is adapt, and it is a method rather than a free function because
// it needs the renderer, the route table and the flash that this router holds
// and github.com/arandu-io/hesape/routing deliberately does not.
//
// Which is why there is no line to migrate this to yet. Get and Group have
// one; this does not, because the adapter it needs is unexported here and has
// no exported counterpart to reach for.
func (r *Router) Action(method, pattern string, h func(*Context) error, mws ...Middleware) *Route {
	return r.inner.Match([]string{method}, pattern, r.adapt(h), mws...)
}

// Table returns the route table, for URL generation and for `aru routes`.
func (r *Router) Table() *Routes { return r.table }

// Routes returns the registered routes, in registration order.
func (r *Router) Routes() []*Route { return r.inner.Routes() }

// ServeHTTP dispatches to the underlying mux.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) { r.inner.ServeHTTP(w, req) }

// adapt turns a controller action into the http.Handler the mux dispatches to.
//
// It is a routing.Adapter[hhttp.Context], which is the parameter hesape/routing
// takes so that it can register an action without knowing what a request
// context is -- the same reason hesape/http.Renderer is an interface.
// hesape/routing neither declares that adapter nor names one: it leaves it to
// the layer that owns the request context, and this is that layer. It is a
// method rather than a free function because the three values the body below
// closes over -- the renderer, the route table and the flash -- reach it from
// the router, and a function taking only the action reaches none of them; the
// method value r.adapt is the one-argument function routing.Adapter asks for.
// So this is where it is written, once, for every registration that takes a
// controller action.
//
// An error reaching here that nothing below claims is one the handler could
// not handle, so it goes to the panic path: the error page in development, 500
// in production. Swallowing it would answer 200 with an empty body, which is
// the failure nobody debugs.
//
// Two kinds of error are not that, and each is answered in the representation
// the request asked for:
//
//	                    a page                 a request that wants JSON
//	validation.Errors   Reject: flash, back    422 problem, errors member
//	a named status      Refuse, that status    problem, that status
//
// validation.Errors is not a failure the handler could not handle, it is the
// answer, and a failed Validate reads as one. A page gets the flash and the
// redirect back, because a 422 with the messages in its body is thrown away by
// htmx and posted again by a reload. A client that wants JSON has no form to go
// back to, so it gets the messages keyed by field, through
// exception.WriteValidationProblem.
//
// An error that says which status it is -- a record that does not exist, a
// refusal, an expired form, a duplicate key, or an error declaring HTTPStatus,
// which is how a body over the size limit arrives as 413 -- is answered with
// that status. The status is exception.StatusOf's, the one table every adapter
// reads, so the same error is answered with the same status here and in the
// exception handler; its doc holds the closed list and its order. The sentence
// is statusSentence's in both representations, so the error's own text stays
// out of either.
//
// Which representation is Context.WantsJSON's answer: the request asked for
// JSON in Accept, or it is an XHR that is not htmx. It is the rule the exception
// handler applies to a failure that never reached an action, so one request is
// answered in one representation wherever it failed.
//
// Both branches are here, once, for the reason Redirect and Refuse are one
// function each: the last time a decision of this shape lived at every call
// site there were forty-one copies of it, and the failure they were written to
// answer is invisible when one of them is wrong.
func (r *Router) adapt(h func(*Context) error) http.Handler {
	renderer, urls, flash := r.render, r.table.inner, r.flash
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := hhttp.NewContext(w, req, renderer, urls)
		err := h(ctx)
		if err == nil {
			return
		}
		wantsJSON := ctx.WantsJSON()

		var rejected validation.Errors
		if errors.As(err, &rejected) {
			if !rejected.Any() {
				// An empty set of errors returned as an error is a handler that
				// wrote `return errs` without asking whether anything failed.
				// Answering it sends the person back to the form they just
				// filled in with nothing on it and no reason given -- the exact
				// failure this path exists to remove, produced by the path
				// itself. It is a defect in the handler, so it is answered like
				// one.
				panic("http: a handler returned an empty validation.Errors. " +
					"Return nil when nothing failed: `if errs.Any() { return errs }`")
			}
			if wantsJSON {
				exception.WriteValidationProblem(w, req, rejected)
				return
			}
			if flash == nil {
				// A page has nowhere to show the messages but the flash, and a
				// rejection nobody will see is answered as the defect it is
				// rather than as a bare 422. See WithFlash.
				panic(err)
			}
			Reject(w, req, flash, rejected)
			return
		}

		status, ok := exception.StatusOf(err)
		if !ok {
			panic(err)
		}
		if status < 400 || status > 599 {
			// A status outside the error range is not an answer to a
			// failure: 200 would tell the client the request worked, and a
			// 3xx with no Location sends it nowhere. It is a defect in the
			// error type, and it is answered like one.
			panic(fmt.Errorf("http: %T asked to be answered with status %d, and an error is answered with 400-599: %w", err, status, err))
		}
		if wantsJSON {
			exception.WriteProblem(w, req, status, statusSentence(status))
			return
		}
		Refuse(w, req, status, statusSentence(status))
	})
}

// statusSentence is what a person reads when an error is answered with a
// status.
//
// It is the standard text for the status and never the error's own string:
// an error is written for the log, and a wrapped chain carries exactly the
// context -- an id, a table, a query -- that is not for whoever made the
// request.
func statusSentence(status int) string {
	if status == exception.StatusPageExpired {
		return "this page has expired: reload it and submit again"
	}
	if text := http.StatusText(status); text != "" {
		return text
	}
	return "the request could not be completed"
}
