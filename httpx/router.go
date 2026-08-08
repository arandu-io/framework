// Package httpx is the routing layer.
//
// It is a thin shell over net/http: Middleware is the standard
// func(http.Handler) http.Handler, so every middleware written for the Go
// ecosystem works here unchanged.
package httpx

import (
	"net/http"
	"strings"
)

// Middleware is the standard net/http signature. We do not invent our own type:
// that is what keeps the whole Go ecosystem compatible with the framework.
type Middleware func(http.Handler) http.Handler

// Chain composes middlewares. The first in the list is the outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// Router is a thin shell over http.ServeMux, which since Go 1.22 already
// handles methods and path parameters. It exists for groups, per-group
// middleware and route metadata -- the metadata is what lets the CLI generate
// typed URL helpers and what the error page uses to show the matched route.
type Router struct {
	mux    *http.ServeMux
	prefix string
	module string
	mws    []Middleware
	table  *Routes
	render Renderer
}

// Route is metadata, used by `aru routes` and by the error page.
type Route struct {
	Method  string
	Pattern string
	Module  string

	name  string
	table *Routes
}

// RouteName returns the name given with .Name, or empty.
//
// The exported field used to be called Name and nothing ever wrote to it -- a
// promise the code did not keep. Now .Name(...) writes it and this reads it.
func (r *Route) RouteName() string {
	if r == nil {
		return ""
	}
	return r.name
}

// NewRouter returns an empty router.
func NewRouter() *Router {
	return &Router{mux: http.NewServeMux(), table: newRoutes()}
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

// Group returns a sub-router with the prefix appended and the middleware
// inherited. The route table is shared with the parent.
func (r *Router) Group(prefix string, mws ...Middleware) *Router {
	return &Router{
		mux:    r.mux,
		prefix: joinPath(r.prefix, prefix),
		module: r.module,
		mws:    append(append([]Middleware{}, r.mws...), mws...),
		table:  r.table,
		render: r.render,
	}
}

// ForModule returns a sub-router that tags its routes with the module name, so
// `aru routes` can group them. The Kernel calls it for each module.
func (r *Router) ForModule(name string) *Router {
	g := r.Group("")
	g.module = name
	return g
}

// Get registers a GET route.
func (r *Router) Get(pattern string, h http.HandlerFunc, mws ...Middleware) *Route {
	return r.handle(http.MethodGet, pattern, h, mws...)
}

// Post registers a POST route.
func (r *Router) Post(pattern string, h http.HandlerFunc, mws ...Middleware) *Route {
	return r.handle(http.MethodPost, pattern, h, mws...)
}

// Put registers a PUT route.
func (r *Router) Put(pattern string, h http.HandlerFunc, mws ...Middleware) *Route {
	return r.handle(http.MethodPut, pattern, h, mws...)
}

// Patch registers a PATCH route.
func (r *Router) Patch(pattern string, h http.HandlerFunc, mws ...Middleware) *Route {
	return r.handle(http.MethodPatch, pattern, h, mws...)
}

// Delete registers a DELETE route.
func (r *Router) Delete(pattern string, h http.HandlerFunc, mws ...Middleware) *Route {
	return r.handle(http.MethodDelete, pattern, h, mws...)
}

func (r *Router) handle(method, pattern string, h http.HandlerFunc, mws ...Middleware) *Route {
	full := joinPath(r.prefix, pattern)
	all := append(append([]Middleware{}, r.mws...), mws...)
	r.mux.Handle(method+" "+full, Chain(h, all...))

	route := &Route{Method: method, Pattern: full, Module: r.module, table: r.table}
	r.table.add(route)
	return route
}

// handleCtx registers a controller action, which takes a Context and returns an
// error instead of writing to a ResponseWriter.
//
// An error reaching here is one the handler could not handle, so it goes to the
// panic path: the error page in development, 500 in production. Swallowing it
// would answer 200 with an empty body, which is the failure nobody debugs.
func (r *Router) handleCtx(method, pattern string, h func(*Context) error, mws ...Middleware) *Route {
	renderer, table := r.render, r.table
	return r.handle(method, pattern, func(w http.ResponseWriter, req *http.Request) {
		if err := h(&Context{Response: w, Request: req, render: renderer, routes: table}); err != nil {
			panic(err)
		}
	}, mws...)
}

// Action registers one controller action, for a route outside a resource.
//
//	Route.Action("GET", "/dashboard", dashboard.Index).Name("dashboard")
func (r *Router) Action(method, pattern string, h func(*Context) error, mws ...Middleware) *Route {
	return r.handleCtx(method, pattern, h, mws...)
}

// Table returns the route table, for URL generation and for `aru routes`.
func (r *Router) Table() *Routes { return r.table }

// Routes returns the registered routes, in registration order.
func (r *Router) Routes() []*Route { return r.table.All() }

// ServeHTTP dispatches to the underlying mux.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

func joinPath(a, b string) string {
	a = strings.TrimSuffix(a, "/")
	if b == "" || b == "/" {
		if a == "" {
			return "/"
		}
		return a
	}
	if !strings.HasPrefix(b, "/") {
		b = "/" + b
	}
	return a + b
}
