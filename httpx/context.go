package httpx

import (
	"context"
	"encoding/json"
	"net/http"
)

// Context is what a controller action receives.
//
// It is the shape a Laravel developer expects from a controller: the request,
// the response, and helpers that answer. Nothing more -- and the "nothing more"
// is the point. There is no database handle here, no repository, no Grant. A
// controller that could reach the data layer would be a controller that skipped
// the service, and therefore the policy, and `aru doctor` exists to catch
// exactly that.
//
// It is a struct rather than an interface because it has no second
// implementation and never will. One way to do one thing.
type Context struct {
	// Response and Request are exported: a handler that needs the standard
	// library reaches for it directly instead of waiting for a wrapper.
	Response http.ResponseWriter
	Request  *http.Request

	render Renderer
}

// Renderer draws a named view with typed data.
//
// It is an interface here, and implemented in the view package, for one reason:
// the view package imports httpx to register its route, so httpx importing the
// view package back would be a cycle. The kernel wires the concrete one at boot.
type Renderer interface {
	Render(ctx context.Context, w http.ResponseWriter, status int, name string, data any) error
}

// Ctx returns the request context, which carries the Collector, the logger and
// the request id.
func (c *Context) Ctx() context.Context { return c.Request.Context() }

// Param reads a path parameter: /invoices/{id} gives Param("id").
func (c *Context) Param(name string) string { return c.Request.PathValue(name) }

// Query reads a query string parameter.
func (c *Context) Query(name string) string { return c.Request.URL.Query().Get(name) }

// Input reads a form field, from the body or the query string.
//
// Named Input rather than Form because that is what Laravel calls it, and the
// vocabulary is the point (RULE 10).
func (c *Context) Input(name string) string { return c.Request.FormValue(name) }

// View renders a page. The data is a typed struct, never a map.
//
//	return ctx.View("invoices/index", IndexData{Invoices: list})
//
// A map would compile and render blank on a typo, which is the failure this
// framework exists to make impossible. `aru doctor` refuses a map here.
func (c *Context) View(name string, data any) error {
	return c.renderWith(http.StatusOK, name, data)
}

// Fragment renders a partial with a status, for HTMX.
//
// The status matters: a form that failed validation answers 422 with the form
// fragment, and HTMX swaps it in. Answering 200 would make the browser and the
// logs both believe it worked.
func (c *Context) Fragment(status int, name string, data any) error {
	return c.renderWith(status, name, data)
}

func (c *Context) renderWith(status int, name string, data any) error {
	if c.render == nil {
		return errNoRenderer
	}
	return c.render.Render(c.Ctx(), c.Response, status, name, data)
}

// Redirect answers a redirect, and does the right thing under HTMX.
//
// An HTMX request that gets a 302 follows it inside the fragment, so the whole
// page ends up nested in a div. HX-Redirect is the header that makes the browser
// navigate instead. Handling it here means no application has to remember.
func (c *Context) Redirect(to string) error {
	if c.Request.Header.Get("HX-Request") == "true" {
		c.Response.Header().Set("HX-Redirect", to)
		c.Response.WriteHeader(http.StatusNoContent)
		return nil
	}
	http.Redirect(c.Response, c.Request, to, http.StatusSeeOther)
	return nil
}

// JSON answers with JSON. It exists for the endpoints that are genuinely an
// API; a page answers with View.
func (c *Context) JSON(status int, v any) error {
	c.Response.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.Response.WriteHeader(status)
	return json.NewEncoder(c.Response).Encode(v)
}

// Status answers with a status and no body.
func (c *Context) Status(code int) error {
	c.Response.WriteHeader(code)
	return nil
}

// errNoRenderer is what a View call gets when no view layer was wired.
//
// It names the fix, because the alternative is a nil dereference in a stack
// trace that points at the framework rather than at the missing line in main.go.
var errNoRenderer = &noRendererError{}

type noRendererError struct{}

func (*noRendererError) Error() string {
	return "arandu: this handler rendered a view and no view layer is wired. " +
		"Register it in bootstrap/app.go: k.Register(view.NewModule())"
}
