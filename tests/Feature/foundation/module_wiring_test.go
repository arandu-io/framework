package feature

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/hesape/routing"
)

// noContent answers every request it is handed with 204.
func noContent(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

// hookModule receives a provider's callbacks: a framework module that declares
// the prefix they arrive under.
type hookModule struct{ exempt []string }

func (hookModule) Name() string { return "acme" }

func (hookModule) Routes(r *fhttp.Router) { r.Post("/hooks/acme", noContent) }

func (m hookModule) CSRFExemptPaths() []string { return m.exempt }

var _ foundation.CSRFExempt = hookModule{}

// pingModule is a hesape module on the handler-only router, registered through
// Component, that declares one exact path.
type pingModule struct{}

func (pingModule) Name() string { return "feed-ping" }

func (pingModule) Routes(r *routing.Router) { r.Post("/feed/ping", http.HandlerFunc(noContent)) }

func (pingModule) CSRFExemptPaths() []string { return []string{"/feed/ping"} }

// TestAModulesCSRFExemptionsNeedNoLineInTheApplication: what a provider
// declares is exempted from the CSRF check, through Component too, while every
// other write is still checked and the application's own exemptions still hold.
func TestAModulesCSRFExemptionsNeedNoLineInTheApplication(t *testing.T) {
	app := bootConfigured(t, func(b *foundation.ApplicationBuilder) {
		b.WithProviders(hookModule{exempt: []string{"/hooks/"}}, foundation.Component(pingModule{})).
			WithMiddleware(func(m *foundation.Middleware) { m.ValidateCSRFTokensExcept("/v1/usage") }).
			WithRouting(func(r *fhttp.Router) {
				r.Post("/notes", noContent)
				r.Post("/v1/usage", noContent)
				r.Post("/feed/ping/x", noContent)
			})
	})

	for path, exempt := range map[string]bool{
		"/hooks/acme": true, "/feed/ping": true, "/v1/usage": true, "/notes": false, "/feed/ping/x": false,
	} {
		rec := serveConfigured(t, app, httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}")))
		if got := rec.Code == http.StatusNoContent; got != exempt {
			t.Errorf("POST %s answered %d; exempt = %v, want %v", path, rec.Code, got, exempt)
		}
	}
}

// TestAModuleCannotTurnTheCSRFCheckOff: an exemption the check refuses stops
// Create naming the module, rather than panicking or exempting every route.
func TestAModuleCannotTurnTheCSRFCheckOff(t *testing.T) {
	for _, path := range []string{"/", "", "hooks/", "/hooks/../notes"} {
		_, err := configured(t, func(b *foundation.ApplicationBuilder) {
			b.WithProviders(hookModule{exempt: []string{path}})
		})
		if err == nil || !strings.Contains(err.Error(), `module "acme"`) || !strings.Contains(err.Error(), "CSRF") {
			t.Errorf("a module exempting %q answered %v, want the refusal naming it", path, err)
		}
	}
}
