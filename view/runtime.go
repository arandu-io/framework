// The runtime generated views call, answered by
// github.com/arandu-io/hesape/view.
//
// These are the functions a compiled `.kyse.go` emits calls to. They read the
// same two package-level tables the renderer does -- views and layouts -- so
// they bridge with it or not at all.

package view

import (
	"io"

	hview "github.com/arandu-io/hesape/view"
)

// Text renders a value as a string, for interpolation.
//
// It handles the types a view actually interpolates, and formats anything else
// with %v. It is not reflection over a struct: the field access already happened
// in generated Go, and this only turns the result into characters.
//
// It panics on a method value, which is {{ .Name }} written where
// {{ .Name() }} was meant. That behaviour is hesape's now, and unchanged.
func Text(v any) string { return hview.Text(v) }

// UnsafeText renders a value as a string for interpolation that is written
// without escaping.
//
// It converts exactly as [Text] does, and the two exist as separate names
// because the escaping is the caller's: the escaped form wraps this conversion
// in an HTML escape and the raw form writes the result as it is. Under one name
// the two are indistinguishable in the generated file and in a search over a
// project, so the raw form carries its own.
//
// Every call to this function is a place where the value has to be markup that
// is already trusted.
func UnsafeText(v any) string { return hview.Text(v) }

// TextAttr renders a value for a quoted attribute value.
//
// It escapes what would end the attribute or start markup. It does not escape
// what only matters outside quotes, because the quotes are what bound the value
// there, and a value written without them has no escaping that makes it safe.
func TextAttr(v any) string { return hview.TextAttr(v) }

// TextURL renders a value that is written where a URL is expected, and refuses
// one that is not a URL a page may navigate to or fetch.
//
// The scheme is checked against a list of what is allowed rather than a list of
// what is not, and the comparison is exact: a value that hides a scheme behind
// whitespace or a control character fails to match instead of being cleaned up.
// Cleaning would change what the page says without saying so.
//
// A refused value comes back as the empty string alongside the error, so a
// caller that ignores the error writes nothing rather than the value.
func TextURL(v any) (string, error) { return hview.TextURL(v) }

// TextJS renders a value inside a script, as a literal rather than as code.
//
// What comes back is a quoted literal, so a value carrying statements arrives at
// the interpreter as characters. The sequence that would end the enclosing
// element is escaped as well, because the HTML parser finds it before the script
// is ever read.
func TextJS(v any) string { return hview.TextJS(v) }

// TextCSS renders a value inside a style, and refuses one that is not a plain
// style value.
//
// Style has no escaping that survives every position it can appear in, so this
// admits what a style value is made of and refuses the rest, naming the
// character it refused. As with [TextURL], a refusal returns the empty string
// alongside the error.
func TextCSS(v any) (string, error) { return hview.TextCSS(v) }

// Yield renders the section a child view declared, or nothing.
//
// A layout yields sections that a given child may not have, and the answer is
// the empty string. A missing section is a page without a sidebar, not an error.
func Yield(w io.Writer, sections map[string]func(io.Writer) error, name string) error {
	return hview.Yield(w, sections, name)
}

// RenderInto renders a layout, handing it the sections of the child view.
//
// This is what `@extends` compiles to: the child does not write markup, it
// renders the layout and passes what goes in the holes.
func RenderInto(w io.Writer, layout string, data any, sections map[string]func(io.Writer) error) error {
	return hview.RenderInto(w, layout, data, sections)
}

// LayoutFunc is a view that receives sections, which is what a layout is.
type LayoutFunc = hview.LayoutFunc

// RegisterLayout records a compiled layout. Generated code calls it from init()
// when the view contains a @yield.
func RegisterLayout(name string, f LayoutFunc) { hview.RegisterLayout(name, f) }

// Include renders a partial with the same data as the page.
//
// A partial shares the page's data. That data is one typed struct, so the
// partial receives exactly it -- and a partial that wants something else is a
// partial that takes different data, which is what a component is for.
func Include(w io.Writer, name string, data any) error { return hview.Include(w, name, data) }

// CSRF writes the hidden input a form needs.
//
// The token comes from the data, through an interface the page data satisfies.
// It is not read from a global: a template that reaches for request state
// outside the data it was given is how a form ends up with another session's
// token under load.
func CSRF(w io.Writer, data any) error { return hview.CSRF(w, data) }
