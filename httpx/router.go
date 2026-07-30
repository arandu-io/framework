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
	routes *[]Route
}

// Route is metadata, used by `aru routes` and by the error page.
type Route struct {
	Name    string
	Method  string
	Pattern string
	Module  string
}

// NewRouter returns an empty router.
func NewRouter() *Router {
	return &Router{mux: http.NewServeMux(), routes: &[]Route{}}
}

// Group returns a sub-router with the prefix appended and the middleware
// inherited. The route table is shared with the parent.
func (r *Router) Group(prefix string, mws ...Middleware) *Router {
	return &Router{
		mux:    r.mux,
		prefix: joinPath(r.prefix, prefix),
		module: r.module,
		mws:    append(append([]Middleware{}, r.mws...), mws...),
		routes: r.routes,
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
func (r *Router) Get(pattern string, h http.HandlerFunc, mws ...Middleware) {
	r.handle(http.MethodGet, pattern, h, mws...)
}

// Post registers a POST route.
func (r *Router) Post(pattern string, h http.HandlerFunc, mws ...Middleware) {
	r.handle(http.MethodPost, pattern, h, mws...)
}

// Put registers a PUT route.
func (r *Router) Put(pattern string, h http.HandlerFunc, mws ...Middleware) {
	r.handle(http.MethodPut, pattern, h, mws...)
}

// Patch registers a PATCH route.
func (r *Router) Patch(pattern string, h http.HandlerFunc, mws ...Middleware) {
	r.handle(http.MethodPatch, pattern, h, mws...)
}

// Delete registers a DELETE route.
func (r *Router) Delete(pattern string, h http.HandlerFunc, mws ...Middleware) {
	r.handle(http.MethodDelete, pattern, h, mws...)
}

func (r *Router) handle(method, pattern string, h http.HandlerFunc, mws ...Middleware) {
	full := joinPath(r.prefix, pattern)
	all := append(append([]Middleware{}, r.mws...), mws...)
	r.mux.Handle(method+" "+full, Chain(h, all...))
	*r.routes = append(*r.routes, Route{Method: method, Pattern: full, Module: r.module})
}

// Routes returns the registered routes, in registration order.
func (r *Router) Routes() []Route { return *r.routes }

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
