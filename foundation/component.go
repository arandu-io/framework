// Modules written against hesape/foundation, registered as they are.

package foundation

import (
	fhttp "github.com/arandu-io/framework/http"
	hfoundation "github.com/arandu-io/hesape/foundation"
)

// Component hands Register a module written against hesape/foundation whose
// Routes still takes the handler-only *routing.Router:
//
//	app.Register(foundation.Component(geo.NewModule(cfg, catalog)))
//
// Its routes are registered on the same table as every other module's,
// through Router.Routing, scoped and tagged with the module's name. Every
// optional interface the module implements -- Bootable, Migratable,
// Schedulable, Commandable, Health, Closable and the rest -- is asked of the
// module itself, so wrapping it hides none of them.
//
// A module that implements hesape/foundation.Routable needs no wrapping: its
// Routes takes the *controllers.Router, which is http.Router, so with a Name it
// is a Module already and goes to Register as it stands. This is for the
// modules that have not moved to Routable yet.
func Component(m hfoundation.Module) Module {
	if m == nil {
		panic("foundation: Component was given a nil module")
	}
	return componentModule{inner: m}
}

// componentModule is a hesape module under this package's contract.
type componentModule struct{ inner hfoundation.Module }

// Name is the wrapped module's.
func (c componentModule) Name() string { return c.inner.Name() }

// Routes hands the wrapped module the handler-only router underneath r.
func (c componentModule) Routes(r *fhttp.Router) { c.inner.Routes(r.Routing()) }

// unwrapped is the module the optional interfaces are asked of: the one
// Component wrapped, or m itself.
func unwrapped(m Module) any {
	if c, ok := m.(componentModule); ok {
		return c.inner
	}
	return m
}

// capable answers whether a registered module implements T, asking the module
// Component wrapped rather than the wrapper.
func capable[T any](m Module) (T, bool) {
	t, ok := unwrapped(m).(T)
	return t, ok
}

// A module that implements hesape/foundation.Routable and has a Name is a
// Module: the two routers are one type.
var _ Module = (interface {
	hfoundation.Routable
	Name() string
})(nil)
