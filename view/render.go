// The compiled-view registry and the renderer, answered by
// github.com/arandu-io/hesape/view.
//
// The registry is package-level state, and that is the reason this file is a
// bridge rather than a copy: generated code calls Register from init(), and the
// kernel installs the Renderer. Two tables would mean a view registered under
// one name and looked up in the other, which renders as "no view named x" on a
// page whose file is right there on disk.

package view

import (
	hview "github.com/arandu-io/hesape/view"
)

// Func is what a compiled view is: a function that writes HTML.
//
// `aru view:build` emits one per `.kyse.go` and registers it by name. The data
// arrives as `any` and the generated function asserts it back to the struct the
// view declared -- which is why a wrong type is an error naming both sides
// rather than a blank page.
type Func = hview.Func

// Register records a compiled view under the name a controller renders it by.
//
//	view.Register("invoices/index", renderInvoicesIndex)
//
// Generated code calls it from init(), so importing the views package is what
// makes them reachable -- the same shape as a database/sql driver.
//
// Registering twice panics rather than replacing. Two views for one name is a
// build artifact that outlived its source, and finding out at boot beats
// finding out from a page that renders the wrong thing.
func Register(name string, f Func) { hview.Register(name, f) }

// Registered returns the known view names, sorted. `aru doctor` reads it to
// check that every ctx.View("x") has a view named x.
func Registered() []string { return hview.Registered() }

// Renderer draws a view. It is the concrete side of http.Renderer.
//
// It carries no state -- the registry above is package-level -- which is why
// the type is an empty struct on both sides of this bridge and the alias is
// exact.
type Renderer = hview.Renderer

// NewRenderer returns the renderer. The kernel hands it to the router at boot.
func NewRenderer() *Renderer { return hview.NewRenderer() }

// WrongData is what a generated view returns when the data is not the struct it
// declared.
//
// Generated code calls it, so the message is the same everywhere:
//
//	d, ok := data.(HomeData)
//	if !ok { return view.WrongData("home", "HomeData", data) }
//
// The alternative -- rendering the zero value -- is a blank page with a 200,
// which is the failure this framework exists to make impossible.
func WrongData(view, want string, got any) error { return hview.WrongData(view, want, got) }
