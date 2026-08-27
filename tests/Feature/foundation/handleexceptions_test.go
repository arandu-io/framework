package feature

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation/bootstrap"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/hesape/config"
	"github.com/arandu-io/hesape/exception"
)

// The official exception path, wired the way bootstrap/app.go wires it.
//
// HandleExceptions had no caller anywhere, which is the state in which a path
// stops being the official one and becomes a shape nobody has run. These tests
// are the caller: they build the handler from a Configuration, install
// exception.Recover with it, and send a request through.

// handled builds the pipeline an application builds: the handler from the
// bootstrapper, installed as the outermost middleware.
func handled(dev bool, register func(*exception.Handler), h http.Handler) http.Handler {
	cfg := bootstrap.Configuration{
		App:           config.App{Debug: dev},
		Observability: bootstrap.Observability{Editor: "vscode"},
	}
	handler := bootstrap.HandleExceptions(cfg, "example.test/loja", nil)
	if register != nil {
		register(handler)
	}
	return fhttp.Chain(h, exception.Recover(handler))
}

// TestHandleExceptionsCatchesAPanic is the baseline: the handler the
// bootstrapper returns, installed by the application, answers a panic.
func TestHandleExceptionsCatchesAPanic(t *testing.T) {
	h := handled(false, nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("connection to payments failed: token=secret-token")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/checkout", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "secret-token") {
		t.Errorf("the production response leaks the panic text: %s", body)
	}
}

// TestHandleExceptionsRendersTheDebugPage: Dev comes from the Configuration's
// App.Debug, so an application that is in debug gets the page without passing a
// second flag anywhere.
func TestHandleExceptionsRendersTheDebugPage(t *testing.T) {
	h := handled(true, nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("something the developer did not expect")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/invoices", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "something the developer did not expect") {
		t.Errorf("the debug page does not show the panic: %s", body)
	}
}

// TestHandleExceptionsKeepsTheHandlerTheApplicationRegistersOn is the reason the
// bootstrapper returns the handler rather than the middleware, and the whole
// difference from http/middleware.Recover: that one builds a handler from three
// fields and drops it, so there is nothing left to register an answer on.
//
// ShouldRenderJSONWhen is the one used here because it is read on the panic
// path itself: the same failure that draws a page for a browser answers an API
// with a document, decided by the application on the handler it kept.
func TestHandleExceptionsKeepsTheHandlerTheApplicationRegistersOn(t *testing.T) {
	h := handled(false, func(handler *exception.Handler) {
		handler.ShouldRenderJSONWhen(func(*http.Request, error) bool { return true })
	}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("a failure an API client must not receive as HTML")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/checkout", nil))

	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "json") {
		t.Errorf("Content-Type = %q, want a JSON document: the callback registered on the handler did not answer", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":500`) {
		t.Errorf("the document does not carry the status: %s", body)
	}
	// The panic text is not in it for the same reason it is not on the page.
	if strings.Contains(body, "must not receive as HTML") {
		t.Errorf("the document leaks the panic text: %s", body)
	}
}
