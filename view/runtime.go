package view

import (
	"fmt"
	"io"
	"strconv"
)

// The functions here are what generated views call. They are the runtime of
// kyse, and it is deliberately small: everything that can be decided at build
// time was, and what is left is the handful of things that cannot.

// Text renders a value as a string, for interpolation.
//
// It handles the types a view actually interpolates, and formats anything else
// with %v. It is not reflection over a struct: the field access already happened
// in generated Go, and this only turns the result into characters.
func Text(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case nil:
		return ""
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprint(v)
	}
}

// Yield renders the section a child view declared, or nothing.
//
// A layout yields sections that a given child may not have. Blade answers with
// the default -- an empty string -- and so does this. A missing section is a
// page without a sidebar, not an error.
func Yield(w io.Writer, sections map[string]func(io.Writer) error, name string) error {
	section, declared := sections[name]
	if !declared {
		return nil
	}
	return section(w)
}

// RenderInto renders a layout, handing it the sections of the child view.
//
// This is what `@extends` compiles to: the child does not write markup, it
// renders the layout and passes what goes in the holes. Same shape as Blade,
// where the child template is evaluated to fill sections before the layout runs.
func RenderInto(w io.Writer, layout string, data any, sections map[string]func(io.Writer) error) error {
	mu.RLock()
	f, known := layouts[layout]
	mu.RUnlock()

	if !known {
		return fmt.Errorf("view: %q extends a layout that does not exist. "+
			"Layouts are views too -- resources/views/%s.kyse.go", layout, layout)
	}
	return f(w, data, sections)
}

// LayoutFunc is a view that receives sections, which is what a layout is.
type LayoutFunc func(w io.Writer, data any, sections map[string]func(io.Writer) error) error

var layouts = map[string]LayoutFunc{}

// RegisterLayout records a compiled layout. Generated code calls it from init()
// when the view contains a @yield.
func RegisterLayout(name string, f LayoutFunc) {
	mu.Lock()
	defer mu.Unlock()
	if _, taken := layouts[name]; taken {
		panic("view: layout " + name + " is already registered -- a stale generated file is probably still on disk")
	}
	layouts[name] = f
}

// Include renders a partial with the same data as the page.
//
// Blade's @include shares the parent's variables. Here the data is one typed
// struct, so the partial receives exactly it -- and a partial that wants
// something else is a partial that takes different data, which is what a
// component is for.
func Include(w io.Writer, name string, data any) error {
	mu.RLock()
	f, known := views[name]
	mu.RUnlock()

	if !known {
		return unknownView(name)
	}
	return f(w, data)
}

// CSRF writes the hidden input a form needs.
//
// The token comes from the data, through an interface the page data satisfies.
// It is not read from a global: a template that reaches for request state
// outside the data it was given is how a form ends up with another session's
// token under load.
func CSRF(w io.Writer, data any) error {
	holder, ok := data.(interface{ CSRFToken() string })
	if !ok {
		return fmt.Errorf("view: @csrf needs the page data to provide the token. " +
			"Add a CSRFToken() string method to the struct the view declares")
	}
	_, err := fmt.Fprintf(w, `<input type="hidden" name="_csrf" value="%s">`, holder.CSRFToken())
	return err
}
