package view

import (
	"github.com/arandu-io/framework/http"
	internalroutes "github.com/arandu-io/framework/internal/routes"
	"github.com/arandu-io/framework/kernel"
)

type reservedNamespace = internalroutes.ReservedNamespace

// Module serves the embedded assets and wires the renderer.
//
// It is a kernel.Module and not a plain Mount function an application calls: a
// function has to be remembered, and a screen that emits three tags against a
// server nobody mounted gets three 404s -- no stylesheet, no HTMX, no client
// behaviour.
//
// A module appears in the Register call next to events, jobs and the scheduler,
// which is where somebody reading main.go already looks to learn what an
// application is made of.
type Module struct{ reservedNamespace }

// NewModule returns the module.
//
//	k.Register(view.NewModule(), auth.New(...), events.NewModule())
func NewModule() *Module { return &Module{} }

var _ kernel.Module = (*Module)(nil)

// Name is the module identifier.
func (*Module) Name() string { return "view" }

// Routes registers the content-addressed asset route.
//
// One route, one handler. The hash in the path is what makes the response
// cacheable forever, and what makes a deploy invalidate it without anybody
// clearing anything.
func (*Module) Routes(r *http.Router) {
	r.Get(AssetPath+"{hash}/{name}", Handler).Name("view.asset")
}

// ReloadTag supplies the development live-reload tag to the kernel.
//
// kernel.ReloadTagger, an optional interface, asked for only in development.
// The kernel gives the address of the stream it serves; this package owns the
// script and the asset it is served as.
func (*Module) ReloadTag(stream string) string { return ReloadTag(stream) }

// Renderer supplies the view renderer to the kernel.
//
// It is kernel.RendererProvider, an optional interface: the kernel asks every
// registered module whether it brings one, before any route is registered. That
// is what makes ctx.View work without the application calling a wiring function
// that somebody eventually forgets.
func (*Module) Renderer() http.Renderer { return NewRenderer() }
