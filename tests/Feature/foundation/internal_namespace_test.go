package feature

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
)

// marked is an application middleware that says on the response that it ran.
func marked(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Application-Pipeline", "ran")
		next.ServeHTTP(w, r)
	})
}

// crossSite is a write another site's page makes the browser send.
func crossSite(method, target string) *http.Request {
	req := httptest.NewRequest(method, "https://x.example"+target, strings.NewReader("{}"))
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	return req
}

// TestAnApplicationRouteReachedUnderTheReservedPrefixRunsThePipeline: the
// application's middleware is skipped only for a request the framework's own
// route answers. A path that merely begins with /_arandu/ and is answered by an
// application route -- through a parameter segment, an escaped slash, a catch-all
// or a fallback -- goes through the whole pipeline, the origin check included.
func TestAnApplicationRouteReachedUnderTheReservedPrefixRunsThePipeline(t *testing.T) {
	reached := func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("reached")) }
	app := bootConfigured(t, func(b *foundation.ApplicationBuilder) {
		b.WithMiddleware(func(m *foundation.Middleware) { m.Prepend(marked) }).
			WithRouting(func(r *fhttp.Router) {
				r.Post("/{workspace}/projects", reached)
				r.Post("/{slug}", reached)
				r.Get("/files/{path...}", reached)
				r.Get("/{page...}", reached)
				r.Routing().Fallback(http.HandlerFunc(reached))
			})
	}, "APP_ENV", "prod", "APP_URL", "https://x.example", "MAIL_URL", "array://")

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/acme/projects"},
		{http.MethodPost, "/_arandu/projects"},
		{http.MethodPost, "/_arandu%2Fprojects"},
		{http.MethodPost, "/_arandu/x/y/z"},
		{http.MethodPut, "/_arandu/health"},
	} {
		rec := serveConfigured(t, app, crossSite(tc.method, tc.path))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s answered %d %q, want 403: a cross-site write reached an application route without the origin check",
				tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-Application-Pipeline") != "ran" {
			t.Errorf("%s %s skipped the application's middleware", tc.method, tc.path)
		}
	}

	for _, path := range []string{"/_arandu/console", "/_arandu%2Fhealth"} {
		rec := serveConfigured(t, app, httptest.NewRequest(http.MethodGet, "https://x.example"+path, nil))
		if rec.Code != http.StatusOK || rec.Body.String() != "reached" {
			t.Fatalf("GET %s answered %d %q, want the application's catch-all", path, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-Application-Pipeline") != "ran" {
			t.Errorf("GET %s reached an application route and skipped the application's middleware", path)
		}
	}

	// The framework's own routes still answer to the framework alone. The
	// readiness probe may report a module down -- the ping-only database holds
	// no jobs table -- and either way it is the probe that answers.
	for _, path := range []string{"/_arandu/health", "/_arandu/live"} {
		rec := serveConfigured(t, app, httptest.NewRequest(http.MethodGet, "https://x.example"+path, nil))
		if rec.Body.String() == "reached" {
			t.Fatalf("GET %s answered %d %q, want the framework's probe", path, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-Application-Pipeline") != "" {
			t.Errorf("GET %s ran the application's middleware", path)
		}
	}
}

// TestBootRefusesAnApplicationRouteThatAnswersAFrameworkPath: a route that
// answers a request for a path the framework mounts -- with a method the
// framework's route does not take -- is refused naming the module, so the
// reserved namespace holds no application code on any method. A fallback is
// what answers every miss, and is left alone.
func TestBootRefusesAnApplicationRouteThatAnswersAFrameworkPath(t *testing.T) {
	ok := func(http.ResponseWriter, *http.Request) {}
	for _, tc := range []struct {
		name     string
		register func(r *fhttp.Router)
		want     string
	}{
		{"POST /", func(r *fhttp.Router) { r.Post("/", ok) }, `"/"`},
		{"a remainder parameter", func(r *fhttp.Router) { r.Post("/{path...}", ok) }, `"/{path...}"`},
		{"a parameter segment", func(r *fhttp.Router) { r.Delete("/{tenant}/live", ok) }, `"/{tenant}/live"`},
		{"an escaped prefix", func(r *fhttp.Router) { r.Get("/%5Farandu/export", ok) }, `"/%5Farandu/export"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, err := configured(t, func(b *foundation.ApplicationBuilder) { b.WithRouting(tc.register) },
				"APP_ENV", "prod", "APP_URL", "https://x.example", "MAIL_URL", "array://")
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			t.Cleanup(func() { _ = app.Shutdown() })
			err = app.Boot(context.Background())
			if err == nil {
				t.Fatal("Boot accepted an application route that answers under the reserved /_arandu/ namespace")
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "/_arandu/") || !strings.Contains(err.Error(), "module") {
				t.Fatalf("Boot error = %q, want it to name the module, the route %s and the namespace", err, tc.want)
			}
		})
	}

	app := bootConfigured(t, func(b *foundation.ApplicationBuilder) {
		b.WithRouting(func(r *fhttp.Router) {
			r.Get("/{$}", ok)
			r.Post("/{workspace}/projects", ok)
			r.Get("/{page...}", ok)
			r.Routing().Fallback(http.HandlerFunc(ok))
		})
	}, "APP_ENV", "prod", "APP_URL", "https://x.example", "MAIL_URL", "array://")
	if app == nil {
		t.Fatal("an application with a fallback and a GET catch-all did not boot")
	}
}
