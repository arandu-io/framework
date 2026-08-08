package httpx

import (
	"fmt"
	"strings"
	"sync"
)

// Name gives the route a name, so a URL can be generated from it instead of
// written by hand.
//
//	Route.Get("/", home).Name("home")
//	Route.Resource("invoices", InvoiceController{})   // names them all
//
// It returns the route so the call chains, and the declaration reads as one
// line: Route.Get("/", home).Name("home").
//
// The name was a field on Route from the first version and was never filled in.
// A field that nothing writes is a promise the code does not keep -- `aru
// routes` printed an empty column, and there was no way to generate a URL.
func (r *Route) Name(name string) *Route {
	if r == nil {
		return nil
	}
	r.name = name
	if r.table != nil {
		r.table.register(name, r)
	}
	return r
}

// URL builds the path of a named route, filling the parameters in order.
//
//	URL("home")                  -> "/"
//	URL("invoices.show", "42")   -> "/invoices/42"
//
// A hardcoded "/invoices/"+id compiles and keeps compiling after the route
// moves. This does not: an unknown name or a wrong number of parameters is an
// error the caller sees, not a 404 the user sees.
//
// It returns an error rather than panicking, because a URL is often built from
// data -- and a panic in a template renderer takes the whole page down to report
// something a broken link would have said better.
func (t *Routes) URL(name string, params ...string) (string, error) {
	t.mu.RLock()
	route, known := t.byName[name]
	t.mu.RUnlock()

	if !known {
		return "", fmt.Errorf("httpx: no route named %q. Name it with .Name(%q), or run `aru routes` to see what exists", name, name)
	}

	// "{$}" is not a parameter. It is the anchor that stops a pattern ending in
	// a slash from matching everything below it, which is what "GET /{$}" means
	// and what registering the root route does by default. Reading it as a
	// parameter made URL("home") return an error for the one route every
	// application has.
	out := strings.TrimSuffix(route.Pattern, "{$}")
	if out == "" {
		out = "/"
	}

	var missing []string
	for _, segment := range strings.Split(out, "/") {
		if !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
			continue
		}
		if len(params) == 0 {
			missing = append(missing, segment)
			continue
		}
		out = strings.Replace(out, segment, params[0], 1)
		params = params[1:]
	}

	if len(missing) > 0 {
		return "", fmt.Errorf("httpx: route %q needs %s and got none", name, strings.Join(missing, ", "))
	}
	if len(params) > 0 {
		return "", fmt.Errorf("httpx: route %q takes fewer parameters than the %d given", name, len(params))
	}
	return out, nil
}

// Must is URL for the places that cannot handle an error -- a template helper,
// mostly. It returns the message as the href, so a broken link says what is
// wrong instead of pointing at "/".
func (t *Routes) Must(name string, params ...string) string {
	out, err := t.URL(name, params...)
	if err != nil {
		return "#" + err.Error()
	}
	return out
}

// Routes is the table of registered routes, and the index by name.
type Routes struct {
	mu     sync.RWMutex
	all    []*Route
	byName map[string]*Route
}

func newRoutes() *Routes { return &Routes{byName: map[string]*Route{}} }

func (t *Routes) add(r *Route) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.all = append(t.all, r)
}

// register indexes a route by name.
//
// Registering the same name twice panics rather than replacing. Two routes with
// one name means every URL built from it goes to one of them, chosen by
// registration order -- and finding that out at boot beats finding it out from a
// link that quietly points at the wrong page.
func (t *Routes) register(name string, r *Route) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if existing, taken := t.byName[name]; taken && existing != r {
		panic(fmt.Sprintf("httpx: two routes named %q: %s %s and %s %s",
			name, existing.Method, existing.Pattern, r.Method, r.Pattern))
	}
	t.byName[name] = r
}

// All returns the routes in registration order, for `aru routes`.
func (t *Routes) All() []*Route {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return append([]*Route(nil), t.all...)
}
