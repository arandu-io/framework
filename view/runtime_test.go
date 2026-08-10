package view_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/arandu-io/framework/view"
)

// TestAMethodInterpolatedInsteadOfItsResultIsRefused.
//
// {{ .Greeting }} and {{ .Greeting() }} both compile: without the parentheses
// the expression is the method value, and fmt's %v prints a function as its
// address. The showcase shipped a verification e-mail whose plain-text part read
// "Hello 0x10273d6d0, and welcome." to everybody who registered, and it stayed
// invisible because the HTML part beside it was written correctly -- only a
// reader whose client prefers text ever saw it, which is exactly the audience a
// text part exists for.
func TestAMethodInterpolatedInsteadOfItsResultIsRefused(t *testing.T) {
	for name, value := range map[string]any{
		"func() string":         func() string { return "Ada" },
		"func() int":            func() int { return 1 },
		"func() (string, bool)": func() (string, bool) { return "Ada", true },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					t.Fatalf("a %s was rendered instead of refused, which is how an address reaches somebody's inbox", name)
				}
				if !strings.Contains(fmt.Sprint(recovered), "{{ .Name() }}") {
					t.Errorf("the panic does not say how to fix it: %v", recovered)
				}
			}()
			_ = view.Text(value)
		})
	}
}

// And the values a view actually interpolates still render.
func TestTheOrdinaryValuesStillRender(t *testing.T) {
	for _, c := range []struct {
		in   any
		want string
	}{
		{"Ada", "Ada"}, {42, "42"}, {int64(42), "42"}, {true, "true"},
		{1.5, "1.5"}, {nil, ""}, {struct{ A int }{1}, "{1}"},
	} {
		if got := view.Text(c.in); got != c.want {
			t.Errorf("Text(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
