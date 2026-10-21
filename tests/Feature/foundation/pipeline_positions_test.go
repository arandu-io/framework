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
	"github.com/arandu-io/hesape/cache"
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

// TestBeforeThrottleRunsBetweenTheProxiesAndTheBudget: what BeforeThrottle
// added reads the visitor's address TrustProxies resolved, and its headers
// hold on the 429 the budget answers, because it ran first.
func TestBeforeThrottleRunsBetweenTheProxiesAndTheBudget(t *testing.T) {
	var prepended, before []string
	app := bootConfigured(t, func(b *foundation.ApplicationBuilder) {
		b.WithMiddleware(func(m *foundation.Middleware) {
			m.Throttle(cache.PerMinute(1)).
				Prepend(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						prepended = append(prepended, r.RemoteAddr)
						next.ServeHTTP(w, r)
					})
				}).
				BeforeThrottle(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						before = append(before, r.RemoteAddr)
						w.Header().Set("Cache-Control", "no-store")
						next.ServeHTTP(w, r)
					})
				})
		}).WithRouting(func(r *fhttp.Router) {
			r.Get("/checkout/abc", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		})
	}, "TRUSTED_PROXIES", "192.0.2.0/24")

	visit := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/checkout/abc", nil)
		req.RemoteAddr = "192.0.2.10:4000"
		req.Header.Set("X-Forwarded-For", "203.0.113.7")
		return serveConfigured(t, app, req)
	}
	if rec := visit(); rec.Code != http.StatusNoContent {
		t.Fatalf("the first visit answered %d", rec.Code)
	}
	rec := visit()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the second visit answered %d, want the budget's 429", rec.Code)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("the 429 carries Cache-Control %q: BeforeThrottle did not run before the budget", rec.Header().Get("Cache-Control"))
	}
	if len(before) == 0 || !strings.HasPrefix(before[0], "203.0.113.7") {
		t.Errorf("BeforeThrottle saw %v, want the address the trusted proxy forwarded", before)
	}
	if len(prepended) == 0 || !strings.HasPrefix(prepended[0], "192.0.2.10") {
		t.Errorf("Prepend saw %v, want the peer: it runs before the proxies are read", prepended)
	}
}

// TestCSRFExemptionsTakeExactPathsAndPrefixes: an entry without a trailing
// slash exempts exactly that path and nothing that merely starts with it; one
// with the slash exempts what is under it. An exempt write keeps the
// Cache-Control its handler chose.
func TestCSRFExemptionsTakeExactPathsAndPrefixes(t *testing.T) {
	app := bootConfigured(t, func(b *foundation.ApplicationBuilder) {
		b.WithMiddleware(func(m *foundation.Middleware) {
			m.ValidateCSRFTokensExcept("/v1/usage", "/webhooks/")
		}).WithRouting(func(r *fhttp.Router) {
			ok := func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusNoContent)
			}
			for _, p := range []string{"/v1/usage", "/v1/usage/batch", "/v1/usagex", "/webhooks/stripe"} {
				r.Post(p, ok)
			}
		})
	})

	for path, exempt := range map[string]bool{
		"/v1/usage": true, "/v1/usage/batch": false, "/v1/usagex": false, "/webhooks/stripe": true,
	} {
		rec := serveConfigured(t, app, httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}")))
		if got := rec.Code == http.StatusNoContent; got != exempt {
			t.Errorf("POST %s answered %d; exempt = %v, want %v", path, rec.Code, got, exempt)
		}
		if exempt && rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("POST %s answered Cache-Control %q, want the handler's own", path, rec.Header().Get("Cache-Control"))
		}
	}
}

// TestACSRFExemptionTakesAParameterSegment: a path with a {name} segment
// exempts that route at any value of the segment, and never its siblings, a
// shorter path or a deeper one.
func TestACSRFExemptionTakesAParameterSegment(t *testing.T) {
	app := bootConfigured(t, func(b *foundation.ApplicationBuilder) {
		b.WithMiddleware(func(m *foundation.Middleware) {
			m.ValidateCSRFTokensExcept("/checkout/{token}/sessions")
		}).WithRouting(func(r *fhttp.Router) {
			ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
			r.Post("/checkout/{token}/sessions", ok)
			r.Post("/checkout/{token}/refunds", ok)
			r.Post("/checkout/sessions", ok)
		})
	})

	for path, exempt := range map[string]bool{
		"/checkout/abc/sessions": true, "/checkout/xyz/sessions": true,
		"/checkout/abc/refunds": false, "/checkout/sessions": false, "/checkout/a/b/sessions": false,
	} {
		rec := serveConfigured(t, app, httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}")))
		if got := rec.Code == http.StatusNoContent; got != exempt {
			t.Errorf("POST %s answered %d; exempt = %v, want %v", path, rec.Code, got, exempt)
		}
	}
}
