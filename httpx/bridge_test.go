// What this file tests, and what it deliberately does not.
//
// It tests that the OLD name reaches the NEW behaviour: one compile-time
// assertion per alias, and one round trip per envelope. It does not re-test
// routing, redirects, the open-redirect defence or the resource controller --
// those have tests in hesape, against the code that now runs, and a second copy
// here would be a second thing to keep in step.

package httpx_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/arandu-io/framework/httpx"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	hhttp "github.com/arandu-io/hesape/http"
	"github.com/arandu-io/hesape/pipeline"
	"github.com/arandu-io/hesape/routing"
	hvalidation "github.com/arandu-io/hesape/validation"
)

// The aliases, asserted at compile time. A rename on either side stops the
// package building, which is the whole point of writing them down.
var (
	_ *httpx.Context = (*hhttp.Context)(nil)
	_ *hhttp.Context = (*httpx.Context)(nil)
	_ *httpx.Route   = (*routing.Route)(nil)
	_ *routing.Route = (*httpx.Route)(nil)

	_ httpx.State = hhttp.State{}
	_ hhttp.State = httpx.State{}

	_ httpx.Middleware                  = pipeline.Middleware[http.Handler](nil)
	_ pipeline.Middleware[http.Handler] = httpx.Middleware(nil)
	_ httpx.Middleware                  = hhttp.Middleware(nil)
	_ func(http.Handler) http.Handler   = httpx.Middleware(nil)
	_ httpx.Middleware                  = func(h http.Handler) http.Handler { return h }
	_ httpx.Renderer                    = hhttp.Renderer(nil)

	// The adapter hesape/routing takes, and the seven interfaces it asserts
	// against. These are what prove the aliases instantiate on the right type.
	_ routing.Adapter[hhttp.Context]   = func(func(*httpx.Context) error) http.Handler { return nil }
	_ httpx.Indexer                    = routing.Indexer[hhttp.Context](nil)
	_ routing.Destroyer[hhttp.Context] = httpx.Destroyer(nil)
	_ httpx.Indexer                    = (*controller)(nil)
	_ httpx.Destroyer                  = (*controller)(nil)
)

// renderer records what a handler asked to draw.
type renderer struct {
	name   string
	status int
}

func (r *renderer) Render(_ context.Context, w http.ResponseWriter, status int, name string, _ any) error {
	r.name, r.status = name, status
	w.WriteHeader(status)
	return nil
}

// controller implements every one of the seven action interfaces, so the test
// can prove all seven aliases are the interfaces hesape asserts against.
type controller struct{ seen []string }

func (c *controller) Index(*httpx.Context) error   { c.seen = append(c.seen, "index"); return nil }
func (c *controller) Create(*httpx.Context) error  { c.seen = append(c.seen, "create"); return nil }
func (c *controller) Store(*httpx.Context) error   { c.seen = append(c.seen, "store"); return nil }
func (c *controller) Show(*httpx.Context) error    { c.seen = append(c.seen, "show"); return nil }
func (c *controller) Edit(*httpx.Context) error    { c.seen = append(c.seen, "edit"); return nil }
func (c *controller) Update(*httpx.Context) error  { c.seen = append(c.seen, "update"); return nil }
func (c *controller) Destroy(*httpx.Context) error { c.seen = append(c.seen, "destroy"); return nil }

// A controller written against the framework's Context is a controller
// hesape/routing accepts, without a line changing. The seven interfaces are
// aliases to instantiated generics, and this is what proves the instantiation
// is the right one.
func TestAControllerWrittenAgainstTheOldNamesStillRegistersSevenRoutes(t *testing.T) {
	r := httpx.NewRouter()
	routes := r.Resource("invoices", &controller{})

	if len(routes) != 7 {
		t.Fatalf("Resource registered %d routes, want 7", len(routes))
	}
	want := []string{
		"invoices.index", "invoices.create", "invoices.store",
		"invoices.show", "invoices.edit", "invoices.update", "invoices.destroy",
	}
	for i, route := range routes {
		if route.RouteName() != want[i] {
			t.Errorf("route %d is named %q, want %q", i, route.RouteName(), want[i])
		}
	}
}

// The action adapter is the framework's, because hesape/routing takes it as a
// parameter. This drives one action end to end through Action.
func TestAnActionRegisteredByTheOldNameIsCalledWithTheHesapeContext(t *testing.T) {
	rd := &renderer{}
	r := httpx.NewRouter().WithRenderer(rd)
	r.Action(http.MethodGet, "/dashboard", func(ctx *httpx.Context) error {
		return ctx.View("dashboard/index", struct{}{})
	}).Name("dashboard")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard", nil))

	if rd.name != "dashboard/index" {
		t.Errorf("the renderer was asked for %q, want %q", rd.name, "dashboard/index")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// Routes.URL is the one method whose name did not survive, and this is the
// envelope that keeps it. It has to answer what routing.Routes.Route answers.
func TestTheOldNameForBuildingAPathAnswersWhatTheNewOneAnswers(t *testing.T) {
	r := httpx.NewRouter()
	r.Get("/invoices/{id}", func(http.ResponseWriter, *http.Request) {}).Name("invoices.show")

	got, err := r.Table().URL("invoices.show", "42")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if got != "/invoices/42" {
		t.Errorf("URL = %q, want %q", got, "/invoices/42")
	}
	if must := r.Table().Must("invoices.show", "42"); must != got {
		t.Errorf("Must = %q, want %q", must, got)
	}
	if all := r.Table().All(); len(all) != 1 {
		t.Errorf("All returned %d routes, want 1", len(all))
	}
}

// A name that does not exist is an error the caller sees, not a 404 the person
// sees. The sentence is hesape's now; what this asserts is that an error still
// comes back rather than an empty path.
func TestAnUnknownRouteNameIsStillAnError(t *testing.T) {
	if _, err := httpx.NewRouter().Table().URL("nope"); err == nil {
		t.Fatal("URL of an unnamed route returned no error")
	}
}

// The group prefix and the group middleware are what the old two-argument
// Group carried, and what the envelope has to put into routing.Group.
func TestAGroupStillAppliesItsPrefixAndItsMiddleware(t *testing.T) {
	var wrapped bool
	mark := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapped = true
			next.ServeHTTP(w, r)
		})
	}

	r := httpx.NewRouter()
	admin := r.Group("/admin", mark)
	admin.Get("/reports", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/reports", nil))

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d: the group prefix did not reach the mux", rec.Code, http.StatusTeapot)
	}
	if !wrapped {
		t.Error("the group middleware did not run")
	}
}

// ForModule tags the routes so `aru routes` can group them. The kernel calls it
// per module and reads Route.Module back.
func TestForModuleStillTagsTheRoutesItRegisters(t *testing.T) {
	r := httpx.NewRouter()
	r.ForModule("invoices").Get("/invoices", func(http.ResponseWriter, *http.Request) {})

	all := r.Routes()
	if len(all) != 1 {
		t.Fatalf("Routes returned %d, want 1", len(all))
	}
	if all[0].Module != "invoices" {
		t.Errorf("Module = %q, want %q", all[0].Module, "invoices")
	}
}

// A handler that returns validation.Errors is answered with the flash and the
// redirect back, and that branch lives in this package's adapter. This is the
// round trip: the framework's validation.Errors goes in, and the messages come
// back out of the cookie hesape/session wrote.
func TestAHandlerThatReturnsTheFrameworksErrorsIsFlashedAndRedirected(t *testing.T) {
	flash := security.NewFlash(make([]byte, 32), false)
	r := httpx.NewRouter().WithFlash(flash)
	r.Action(http.MethodPost, "/invoices", func(*httpx.Context) error {
		return validation.Errors{"title": {"this field is required"}}
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/invoices", nil)
	req.Header.Set("Referer", "http://example.test/invoices/create")
	req.Host = "example.test"
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if to := rec.Header().Get("Location"); to != "/invoices/create" {
		t.Errorf("Location = %q, want %q", to, "/invoices/create")
	}

	// The cookie the redirect carries is the one the next request spends. It is
	// spent only by a whole page somebody is about to read, so the Accept
	// header is part of the round trip rather than decoration.
	next := httptest.NewRequest(http.MethodGet, "/invoices/create", nil)
	next.Header.Set("Accept", "text/html")
	for _, c := range rec.Result().Cookies() {
		next.AddCookie(c)
	}
	errs, _, ok := flash.Take(httptest.NewRecorder(), next)
	if !ok {
		t.Fatal("the flash carried nothing back")
	}
	if got := errs["title"]; len(got) != 1 || got[0] != "this field is required" {
		t.Errorf("the messages came back as %v", errs)
	}
}

// An empty set of errors returned as an error is a defect in the handler, and
// it is answered like one rather than as a redirect to a blank form.
func TestAnEmptyErrorSetReturnedAsAnErrorIsStillRefusedLoudly(t *testing.T) {
	defer func() {
		v := recover()
		if v == nil {
			t.Fatal("an empty validation.Errors was accepted")
		}
		if msg, _ := v.(string); !strings.Contains(msg, "empty validation.Errors") {
			t.Errorf("panicked with %v", v)
		}
	}()

	r := httpx.NewRouter().WithFlash(security.NewFlash(make([]byte, 32), false))
	r.Action(http.MethodPost, "/invoices", func(*httpx.Context) error {
		return validation.Errors{}
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/invoices", nil))
}

// Reject takes the framework's validation.Errors and hesape's Reject takes
// hesape's. The conversion is only free while the two have the same underlying
// map, so it is asserted rather than assumed.
func TestTheTwoErrorTypesConvertBothWays(t *testing.T) {
	mine := validation.Errors{"email": {"we need an address"}}
	theirs := hvalidation.Errors(mine)
	back := validation.Errors(theirs)

	if len(back["email"]) != 1 || back["email"][0] != "we need an address" {
		t.Errorf("the round trip lost the message: %v", back)
	}
}

// State is an alias, so what middleware.Flash writes through the old name is
// what hesape/view reads through the new one -- the context key belongs to
// hesape and there is only one of it.
func TestStateWrittenThroughTheOldNameIsReadThroughTheNewOne(t *testing.T) {
	state := httpx.State{
		Errors: hvalidation.Errors{"email": {"we need an address"}},
		Old:    url.Values{"email": {"typed@example.test"}},
	}
	ctx := httpx.WithState(context.Background(), state)

	if got := hhttp.StateFrom(ctx); len(got.Errors["email"]) != 1 {
		t.Errorf("hesape read back %v", got.Errors)
	}
	if got := httpx.StateFrom(ctx); got.Old.Get("email") != "typed@example.test" {
		t.Errorf("the old input came back as %v", got.Old)
	}
}

// Chain is a wrapper because Go has no alias form for a generic function. The
// first in the list has to stay the outermost.
func TestChainStillNestsTheFirstMiddlewareOutermost(t *testing.T) {
	var order []string
	mark := func(name string) httpx.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	h := httpx.Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		order = append(order, "handler")
	}), mark("outer"), mark("inner"))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if strings.Join(order, ",") != "outer,inner,handler" {
		t.Errorf("order = %v", order)
	}
}

// Redirect, Refuse and Back kept their names and their signatures. One assertion
// each that the old name reaches the behaviour, and no more: the shapes
// themselves are hesape's tests.
func TestTheThreeAnsweringHelpersStillReachTheirBehaviour(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/invoices", nil)
	req.Header.Set("HX-Request", "true")
	httpx.Redirect(rec, req, "/invoices/1")
	if rec.Header().Get("HX-Redirect") != "/invoices/1" || rec.Code != http.StatusNoContent {
		t.Errorf("Redirect answered %d with HX-Redirect %q", rec.Code, rec.Header().Get("HX-Redirect"))
	}

	rec = httptest.NewRecorder()
	httpx.Refuse(rec, req, http.StatusForbidden, "not open to your account")
	if rec.Code != http.StatusForbidden || rec.Header().Get("HX-Refresh") != "true" {
		t.Errorf("Refuse answered %d with HX-Refresh %q", rec.Code, rec.Header().Get("HX-Refresh"))
	}

	away := httptest.NewRequest(http.MethodPost, "/invoices", nil)
	away.Header.Set("Referer", "https://evil.example/login")
	away.Host = "example.test"
	if got := httpx.Back(away); got != "/" {
		t.Errorf("Back accepted another origin: %q", got)
	}
}
