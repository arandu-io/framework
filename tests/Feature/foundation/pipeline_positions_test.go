package feature

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/observability"
)

// serveConfigured boots a configured application and answers one request.
func serveConfigured(t *testing.T, app *foundation.Application, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	return rec
}

func bootConfigured(t *testing.T, adjust func(*foundation.ApplicationBuilder), env ...string) *foundation.Application {
	t.Helper()
	app := mustConfigure(t, adjust, env...)
	if err := app.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	t.Cleanup(func() { _ = app.Shutdown() })
	return app
}

// TestSentencesReachTheRefusalRecoverWrites: the words an application chose
// for a status are the ones a panic is answered with, because they are on
// the request before Recover is.
func TestSentencesReachTheRefusalRecoverWrites(t *testing.T) {
	app := bootConfigured(t, func(b *foundation.ApplicationBuilder) {
		b.WithMiddleware(func(m *foundation.Middleware) {
			m.Sentences(func(_ *http.Request, status int) string {
				if status == http.StatusInternalServerError {
					return "Algo deu errado do nosso lado."
				}
				return ""
			})
		}).WithRouting(func(r *fhttp.Router) {
			r.Get("/explode", func(http.ResponseWriter, *http.Request) { panic("boom") })
		})
	}, "APP_ENV", "prod")

	req := httptest.NewRequest(http.MethodGet, "/explode", nil)
	req.Header.Set("Accept", "application/json")
	rec := serveConfigured(t, app, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "Algo deu errado do nosso lado.") {
		t.Fatalf("a panic answered %d %q, want 500 in the application's words", rec.Code, rec.Body.String())
	}
}

// TestTheObserverIsReplaceable: an application's own observer runs where
// Observe would, handed what Observe is handed, and Observe does not run.
func TestTheObserverIsReplaceable(t *testing.T) {
	var handedRecorder, handedDev bool
	app := bootConfigured(t, func(b *foundation.ApplicationBuilder) {
		b.WithMiddleware(func(m *foundation.Middleware) {
			m.Observer(func(dev bool, _ string, recorder *observability.Recorder) func(http.Handler) http.Handler {
				handedDev, handedRecorder = dev, recorder != nil
				return func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("X-Observed-By", "application")
						next.ServeHTTP(w, r)
					})
				}
			})
		}).WithRouting(func(r *fhttp.Router) {
			r.Get("/notes", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		})
	})

	rec := serveConfigured(t, app, httptest.NewRequest(http.MethodGet, "/notes", nil))
	if rec.Header().Get("X-Observed-By") != "application" {
		t.Fatal("the application's observer did not run")
	}
	if rec.Header().Get("X-Request-ID") != "" {
		t.Error("Observe ran beside the observer that replaced it")
	}
	if !handedDev || !handedRecorder {
		t.Errorf("the observer was handed dev=%v recorder=%v, want what Observe is handed in development", handedDev, handedRecorder)
	}
}
