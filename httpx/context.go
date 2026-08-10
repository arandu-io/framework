package httpx

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/arandu-io/framework/observability"
)

// Context is what a controller action receives.
//
// It is everything a controller action gets, and no more: the request,
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
	routes *Routes
}

// URL is the path of a named route, with its parameters filled in order.
//
//	ctx.URL("posts.show", post.ID)   -> "/posts/01J.../"
//
// It is what a controller hands a view instead of building a path by hand.
// "/posts/"+id compiles and keeps compiling after the route moves; this stops
// working the moment the name is wrong, and says so.
//
// An unknown name or a wrong number of parameters returns empty and is logged
// at ERROR with the name. Empty is what the views already treat as "there is no
// link here" -- a page with a missing button is recoverable, and a template
// renderer that panics takes the whole page down to report something a missing
// link would have said better.
func (c *Context) URL(name string, params ...string) string {
	if c.routes == nil {
		return ""
	}
	out, err := c.routes.URL(name, params...)
	if err != nil {
		observability.Log(c.Ctx()).Error("building a URL", "route", name, "error", err)
		return ""
	}
	return out
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
// Named Input rather than Form because Input is the word the vocabulary already
// uses for it, and the vocabulary is the point (RULE 10).
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
// fragment, so the browser and the logs agree with each other. Answering 200
// would make both of them believe it worked.
//
// # The layout has to let htmx swap it, and by default htmx does not
//
// This comment used to end with "and HTMX swaps it in", stated as fact. It does
// not. htmx's default response handling is
//
//	[{code:"204", swap:false}, {code:"[23]..", swap:true}, {code:"[45]..", swap:false, error:true}]
//
// in the copy this framework embeds (2.0.4), and a 422 matches the third entry.
// The fragment is fetched, the status is right, the body is correct -- and it is
// thrown away. The person sees the form they submitted, unchanged, with no
// message on it: the same failure that made a guard's 403 invisible, on the
// answer every form in an application gives.
//
// Neither HX-Retarget nor HX-Reswap rescues it. Both are read after shouldSwap
// has already been decided from the table above; they change where and how, not
// whether. What decides whether is the configuration, and htmx reads it from the
// document without any script running:
//
//	<meta name="htmx-config" content='{"responseHandling":[
//	  {"code":"204","swap":false},
//	  {"code":"422","swap":true},
//	  {"code":"[23]..","swap":true},
//	  {"code":"[45]..","swap":false,"error":true}]}'>
//
// 422 before the catch-all, because htmx takes the first entry that matches.
// That line belongs in the application's layout, once -- it is the layout that
// decides what a fragment answer means, and a per-page opt-in would be a second
// way to answer a rejected form (RULE 9). A meta tag is not a script, so it
// costs nothing against a `script-src 'self'` policy and nothing in Node.
//
// A refusal is a different thing and does not go through here: 403, 419 and 429
// are not a form coming back with messages on it, and they answer through
// Refuse.
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
	Redirect(c.Response, c.Request, to)
	return nil
}

// Redirect is Context.Redirect for the code that holds a raw ResponseWriter: a
// middleware, or a handler registered with Get rather than Action.
//
// It is one function and not one per caller. The branch below existed in three
// copies -- here, in the auth module's handlers, and in the route guards -- and
// the middle one had already been wrong once, answering HX-Redirect with 200 to
// every client including the ones that do not read the header, which left a
// browser with scripts off on a blank page after signing in. Three copies of a
// six-line decision is three chances to be the copy that is wrong (RULE 9).
//
// 303 and not 302: after a POST, 303 is what tells the browser to GET the next
// address instead of posting the body to it again.
func Redirect(w http.ResponseWriter, r *http.Request, to string) {
	if r.Header.Get("HX-Request") == "true" {
		// A body alongside HX-Redirect is swapped in before the browser
		// navigates, so there is deliberately none: 204.
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// Refuse answers a refusal that the person in front of the browser can see.
//
// The status and the sentence are the caller's and are unchanged: 403 stays 403
// and 419 stays 419, so logs, monitoring and every non-HTMX client see exactly
// what they saw before.
//
// What it adds is the HTMX half. htmx does not swap a 4xx -- its response
// handling is configured `{code:"[45]..", swap:false, error:true}` in the copy
// this framework embeds -- so a guard's 403 and an expired CSRF token both
// arrived as a fired event and a blank screen: the person clicked, nothing at
// all happened, and the sentence explaining why was in a response body that was
// thrown away. HX-Refresh makes the browser reload the page as an ordinary
// navigation, and that request is not an HTMX one, so the same middleware
// answers the same refusal and the browser renders it. Nothing loops: a reload
// is a GET, which CSRFProtect does not check, and which a role guard answers as
// a plain page.
//
// # What a reload can and cannot show
//
// It reloads the page the person is ON, which is not always the address that was
// refused. When the two are the same -- an expired token on a form, a fragment
// of the page itself -- the refusal comes back as a full page and is read. When
// they differ -- a boosted link into an area this account may not open -- the
// person gets their own page back, correct and unexplained, and learns only that
// the link does nothing.
//
// That half is deliberately not solved by sending them to the refused address
// instead: the address would come off the request, and a request target
// beginning with "//" is parsed into URL.Path host and all, so handing it to
// location.href is an open redirect built out of a refusal. Something visible
// and incomplete beats something invisible, and beats a hole.
//
// It is one function and not a branch in each middleware, for the reason
// Redirect above is one function: the last time this decision existed in three
// copies, one of them was wrong.
func Refuse(w http.ResponseWriter, r *http.Request, status int, message string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Refresh", "true")
	}
	http.Error(w, message, status)
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
// trace that points at the framework rather than at the missing line in
// bootstrap/app.go.
var errNoRenderer = &noRendererError{}

type noRendererError struct{}

func (*noRendererError) Error() string {
	return "arandu: this handler rendered a view and no view layer is wired. " +
		"Register it in bootstrap/app.go: k.Register(view.NewModule())"
}
