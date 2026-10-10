package feature

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/view"
	"github.com/arandu-io/hesape/config"
	hhttp "github.com/arandu-io/hesape/http"
)

// brandModule draws a page the way a generated controller or a module's
// screen does: view.New and nothing else, with no name passed to it. Its routes
// sit outside every group, and the application mounts no middleware, so no CSRF
// protection runs on them either.
type brandModule struct {
	pages chan view.Page
	seen  chan string
}

func (brandModule) Name() string { return "notes" }

func (m brandModule) Routes(r *fhttp.Router) {
	r.Action(http.MethodGet, "/notes", func(ctx *fhttp.Context) error {
		m.pages <- view.New(ctx, "Notes")
		return ctx.Status(http.StatusOK)
	})
	// A route middleware reports what it found, which is the name being on
	// the request before the route's own pipeline starts.
	routeMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m.seen <- hhttp.AppNameFrom(r.Context())
			next.ServeHTTP(w, r)
		})
	}
	r.Get("/notes/guarded", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), routeMiddleware)
}

func serveBrand(t *testing.T, name string, use ...fhttp.Middleware) (http.Handler, brandModule) {
	t.Helper()
	m := brandModule{pages: make(chan view.Page, 1), seen: make(chan string, 2)}
	cfg := testConfig(config.EnvProd)
	cfg.App.Name = name
	app := foundation.New(cfg).Register(m).Use(use...)
	if err := app.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	return app.Handler(), m
}

// TestAPageOutsideEveryGroupCarriesTheConfiguredName is the property the
// pipeline exists for: a page whose controller never mentions the application
// name still draws it, on a route no group and no CSRF protection covers.
func TestAPageOutsideEveryGroupCarriesTheConfiguredName(t *testing.T) {
	handler, m := serveBrand(t, "Ledger")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.test/notes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	page := <-m.pages
	if got := page.BrandName(); got != "Ledger" {
		t.Errorf("BrandName = %q, want the configured APP_NAME %q", got, "Ledger")
	}
	if page.BrandName() == page.Title {
		t.Errorf("BrandName and Title are both %q: the brand has to be the name, not the title", page.Title)
	}
}

// TestTheNameIsOnTheRequestBeforeAnyMiddlewareRuns: the application's own
// middleware and a route's middleware both find it, so a guard that draws a
// page -- a sign-in redirect, a refusal -- draws the same brand.
func TestTheNameIsOnTheRequestBeforeAnyMiddlewareRuns(t *testing.T) {
	appSeen := make(chan string, 1)
	appMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			appSeen <- hhttp.AppNameFrom(r.Context())
			next.ServeHTTP(w, r)
		})
	}
	handler, m := serveBrand(t, "Ledger", appMiddleware)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.test/notes/guarded", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	if got := <-appSeen; got != "Ledger" {
		t.Errorf("the application's middleware found %q, want Ledger", got)
	}
	if got := <-m.seen; got != "Ledger" {
		t.Errorf("the route's middleware found %q, want Ledger", got)
	}
}

// TestAnEmptyNameDrawsNoBrand: an application configured with no name puts
// nothing on the request, and the page draws an empty brand rather than a
// placeholder.
func TestAnEmptyNameDrawsNoBrand(t *testing.T) {
	handler, m := serveBrand(t, "")
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://example.test/notes", nil))

	if got := (<-m.pages).BrandName(); got != "" {
		t.Errorf("BrandName = %q with no configured name, want empty", got)
	}
}
