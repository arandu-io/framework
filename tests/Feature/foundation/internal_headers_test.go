package feature

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/observability"
	"github.com/arandu-io/hesape/config"
)

// defaultSecurityHeaders is what middleware.SecurityHeaders writes, read off a
// response rather than spelled out, so the comparison follows the policy
// wherever it is defined.
func defaultSecurityHeaders(dev bool) http.Header {
	rec := httptest.NewRecorder()
	middleware.SecurityHeaders(dev)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec.Header()
}

// TestInternalRoutesCarryTheSecurityHeaders: the framework's own routes skip the
// application's pipeline, and with it the security headers an application
// mounts there. They are answered with the default headers all the same -- the
// default policy, not one an application widened -- and the application's own
// routes are left to the application.
func TestInternalRoutesCarryTheSecurityHeaders(t *testing.T) {
	for _, env := range []config.Env{config.EnvDev, config.EnvProd} {
		t.Run(string(env), func(t *testing.T) {
			dev := env == config.EnvDev
			k := foundation.New(testConfig(env)).
				Register(&stub{name: "billing"}).
				Use(middleware.SecurityHeaders(dev, "https://cdn.example.com"))
			if err := k.Boot(context.Background()); err != nil {
				t.Fatalf("Boot: %v", err)
			}
			handler := k.Handler()
			want := defaultSecurityHeaders(dev)

			for _, path := range []string{"/_arandu/health", "/_arandu/live", "/_arandu/nothing-here"} {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
				for name := range want {
					if got := rec.Header().Get(name); got != want.Get(name) {
						t.Errorf("%s %s = %q, want %q", path, name, got, want.Get(name))
					}
				}
				if got := rec.Header().Get("Strict-Transport-Security"); (got != "") == dev {
					t.Errorf("%s Strict-Transport-Security = %q in %s", path, got, env)
				}
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/billing", nil))
			if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "https://cdn.example.com") {
				t.Errorf("the application route's policy = %q, want the application's own", got)
			}
		})
	}
}

// TestTheConsolePolicyDiffersOnlyInInlineStyles: the debug console draws with an
// inline stylesheet and style attributes, so its policy admits inline styles.
// Everything else is the default policy and the default headers.
func TestTheConsolePolicyDiffersOnlyInInlineStyles(t *testing.T) {
	k := foundation.New(testConfig(config.EnvDev))
	if err := k.Boot(context.Background()); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	handler := k.Handler()
	want := defaultSecurityHeaders(true)
	defaultPolicy := want.Get("Content-Security-Policy")

	for _, path := range []string{observability.ConsolePath, observability.ConsolePath + "/missing"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		got := rec.Header().Get("Content-Security-Policy")
		widened := strings.Replace(defaultPolicy, "style-src 'self';", "style-src 'self' 'unsafe-inline';", 1)
		if widened == defaultPolicy {
			t.Fatalf("the default policy has no \"style-src 'self';\" to widen: %q", defaultPolicy)
		}
		if got != widened {
			t.Errorf("%s policy = %q, want the default with inline styles: %q", path, got, widened)
		}
		for name := range want {
			if name == "Content-Security-Policy" {
				continue
			}
			if got := rec.Header().Get(name); got != want.Get(name) {
				t.Errorf("%s %s = %q, want %q", path, name, got, want.Get(name))
			}
		}
	}
}
